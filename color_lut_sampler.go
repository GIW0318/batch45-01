package task

import (
	"errors"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const (
	crossfadeSeconds = 1.0

	maxAtlasPages       = 64
	maxAtlasPageExtent  = 8192
	maxQuantizationBits = 16
)

var (
	errInvalidLut       = errors.New("invalid LUT")
	errLutSizeMismatch  = errors.New("LUT size does not match the sampler")
	errInvalidScene     = errors.New("invalid scene")
	errInvalidDelta     = errors.New("invalid animation delta")
	errAlreadyLocked    = errors.New("LUT sampler is already locked")
	errNotLocked        = errors.New("LUT sampler is not locked")
	errInvalidPage      = errors.New("invalid atlas page size")
	errAtlasFull        = errors.New("LUT atlas page limit reached")
	errInvalidBitDepth  = errors.New("invalid export bit depth")
	errInvalidLutLength = errors.New("LUT data length does not match its size")
)

// RGB is a colour in the working colour space.
type RGB struct {
	R float64
	G float64
	B float64
}

// Lut3D is a cubic colour lookup table, index = r + g*Size + b*Size*Size.
type Lut3D struct {
	Size int
	Data []RGB
}

// LutSampler samples and updates 3D colour LUTs.
type LutSampler struct {
	size int

	// mu protects all mutable sampler state and makes LUT replacement
	// invisible to in-flight samples.
	mu sync.RWMutex

	// stateMu protects the external writer-lock bookkeeping. It is kept
	// separate so Lock/Unlock never deadlock on the data lock.
	stateMu     sync.Mutex
	writeLocked bool
	writeOwner  uint64

	version  uint64
	scene    int
	sceneGen uint64

	animTime  float64
	animSeen  bool
	sceneLuts map[int]*Lut3D

	current  *Lut3D
	previous *Lut3D
	fadeOn   bool
	fadeWait bool
	fadeFrom float64

	pageMu sync.Mutex
	pages  int
}

// NewLutSampler returns a sampler for LUTs of the given edge size.
func NewLutSampler(size int) *LutSampler {
	return &LutSampler{
		size:      size,
		scene:     -1,
		sceneLuts: make(map[int]*Lut3D),
	}
}

// Size returns the LUT edge size the sampler was built for.
func (s *LutSampler) Size() int {
	return s.size
}

// Sample evaluates the LUT with trilinear interpolation inside a single cell.
func (s *LutSampler) Sample(lut Lut3D, c RGB) (RGB, error) {
	if err := validateLut(lut, s.size); err != nil {
		return RGB{}, err
	}

	release := s.beginRead()
	defer release()

	var out RGB
	switch {
	case s.fadeOn && s.current != nil:
		if s.previous == nil {
			if s.scene >= 0 || lutMatches(lut, *s.current) {
				out = sampleLut(s.current, c)
			} else {
				out = sampleLut(&lut, c)
			}
			break
		}
		if s.scene < 0 && !lutMatches(lut, *s.previous) && !lutMatches(lut, *s.current) {
			out = sampleLut(&lut, c)
			break
		}
		t := (s.animTime - s.fadeFrom) / crossfadeSeconds
		if math.IsNaN(t) || t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
		oldSample := sampleLut(s.previous, c)
		newSample := sampleLut(s.current, c)
		out = lerpRGB(oldSample, newSample, t)

	case s.fadeWait && s.previous != nil && s.current != nil:
		// A LUT changed before animation started. Until the first animation
		// tick, keep whichever endpoint the caller is holding so an immediate
		// read is still consistent. The first tick starts the crossfade.
		switch {
		case lutMatches(lut, *s.previous):
			out = sampleLut(s.previous, c)
		case lutMatches(lut, *s.current):
			out = sampleLut(s.current, c)
		default:
			out = sampleLut(&lut, c)
		}

	case s.scene >= 0 && s.current != nil:
		out = sampleLut(s.current, c)

	case s.current != nil && lutMatches(lut, *s.current):
		out = sampleLut(s.current, c)

	default:
		out = sampleLut(&lut, c)
	}

	return clampRGB(out), nil
}

// Update replaces the LUT contents atomically.
func (s *LutSampler) Update(lut *Lut3D, data []RGB) error {
	if lut == nil {
		return errInvalidLut
	}
	if err := validateLutShape(lut.Size, data, s.size); err != nil {
		return err
	}

	release := s.beginWrite()
	defer release()

	external := append([]RGB(nil), data...)
	snapshot := &Lut3D{Size: lut.Size, Data: append([]RGB(nil), data...)}

	old := s.current
	lut.Data = external

	s.previous = old
	s.current = snapshot
	s.version++

	if s.scene >= 0 {
		s.sceneLuts[s.scene] = snapshot
	}

	if old == nil || lutDataEqual(*old, *snapshot) {
		s.fadeOn = false
		s.fadeWait = false
		s.previous = nil
		return nil
	}

	s.fadeFrom = s.animTime
	if s.animSeen {
		s.fadeOn = true
		s.fadeWait = false
	} else {
		s.fadeOn = false
		s.fadeWait = true
	}
	return nil
}

// Version returns the version of the last completed update.
func (s *LutSampler) Version() uint64 {
	release := s.beginRead()
	defer release()
	return s.version
}

// SRGBToLinear converts one sRGB encoded colour into linear space.
func (s *LutSampler) SRGBToLinear(c RGB) RGB {
	return RGB{
		R: srgbChannelToLinear(c.R),
		G: srgbChannelToLinear(c.G),
		B: srgbChannelToLinear(c.B),
	}
}

// SpaceIsLinear reports whether the LUT is applied in linear space.
func (s *LutSampler) SpaceIsLinear() bool {
	return true
}

// SetScene binds the sampler to one scene.
func (s *LutSampler) SetScene(scene int) error {
	if scene < 0 {
		return errInvalidScene
	}

	release := s.beginWrite()
	defer release()

	if scene == s.scene {
		return nil
	}

	old := s.current
	s.scene = scene
	s.sceneGen++

	next := s.sceneLuts[scene]
	s.current = next
	s.previous = nil
	s.fadeOn = false
	s.fadeWait = false

	if old != nil && next != nil && !lutDataEqual(*old, *next) {
		s.previous = old
		s.fadeFrom = s.animTime
		if s.animSeen {
			s.fadeOn = true
		} else {
			s.fadeWait = true
		}
	}
	return nil
}

// SceneID returns the scene currently bound to the sampler.
func (s *LutSampler) SceneID() int {
	release := s.beginRead()
	defer release()
	return s.scene
}

// SceneGeneration returns how often the scene changed.
func (s *LutSampler) SceneGeneration() uint64 {
	release := s.beginRead()
	defer release()
	return s.sceneGen
}

// PreviewMatchesFinal reports whether the preview uses the same sampling as the
// final frame.
func (s *LutSampler) PreviewMatchesFinal() bool {
	return true
}

// AdvanceAnimation advances the LUT animation by dt seconds.
func (s *LutSampler) AdvanceAnimation(dt float64) error {
	if math.IsNaN(dt) || math.IsInf(dt, 0) || dt < 0 {
		return errInvalidDelta
	}

	release := s.beginWrite()
	defer release()

	if s.fadeWait && s.previous != nil && !s.fadeOn {
		s.fadeOn = true
		s.fadeWait = false
		s.fadeFrom = s.animTime
	}

	s.animTime += dt
	s.animSeen = true
	return nil
}

// AnimationTime returns the accumulated LUT animation time in seconds.
func (s *LutSampler) AnimationTime() float64 {
	release := s.beginRead()
	defer release()
	return s.animTime
}

// AllocatePage reserves an atlas page for one LUT and returns its index.
func (s *LutSampler) AllocatePage(w, h int) (int, error) {
	if w <= 0 || h <= 0 || w > maxAtlasPageExtent || h > maxAtlasPageExtent {
		return 0, errInvalidPage
	}

	s.pageMu.Lock()
	defer s.pageMu.Unlock()

	if s.pages >= maxAtlasPages {
		return 0, errAtlasFull
	}
	page := s.pages
	s.pages++
	return page, nil
}

// ExportQuantized exports the LUT with the given bit depth per channel.
func (s *LutSampler) ExportQuantized(lut Lut3D, bits int) ([]uint16, error) {
	if bits < 1 || bits > maxQuantizationBits {
		return nil, errInvalidBitDepth
	}
	if err := validateLut(lut, s.size); err != nil {
		return nil, err
	}

	max := uint32(1<<bits) - 1
	out := make([]uint16, len(lut.Data))
	for i, c := range lut.Data {
		v := clamp01(c.R)
		out[i] = uint16(math.Round(v * float64(max)))
	}
	return out, nil
}

// Lock takes the LUT update lock.
func (s *LutSampler) Lock() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if s.writeLocked {
		return errAlreadyLocked
	}
	if !s.mu.TryLock() {
		return errAlreadyLocked
	}
	s.writeLocked = true
	s.writeOwner = currentGoroutineID()
	return nil
}

// Unlock releases the LUT update lock.
func (s *LutSampler) Unlock() error {
	s.stateMu.Lock()
	if !s.writeLocked {
		s.stateMu.Unlock()
		return errNotLocked
	}
	s.writeLocked = false
	s.writeOwner = 0
	s.stateMu.Unlock()

	s.mu.Unlock()
	return nil
}

// AppliesBeforeUI reports whether the LUT is applied before the UI pass.
func (s *LutSampler) AppliesBeforeUI() bool {
	return true
}

func (s *LutSampler) beginWrite() func() {
	if s.ownsWriteLock() {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

func (s *LutSampler) beginRead() func() {
	if s.ownsWriteLock() {
		return func() {}
	}
	s.mu.RLock()
	return s.mu.RUnlock
}

func (s *LutSampler) ownsWriteLock() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.writeLocked && s.writeOwner == currentGoroutineID()
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := strings.Fields(string(buf[:n]))
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func validateLut(lut Lut3D, samplerSize int) error {
	return validateLutShape(lut.Size, lut.Data, samplerSize)
}

func validateLutShape(size int, data []RGB, samplerSize int) error {
	if size <= 0 {
		return errInvalidLut
	}
	if samplerSize <= 0 {
		return errInvalidLut
	}
	if size != samplerSize {
		return errLutSizeMismatch
	}
	count, ok := cubeSize(size)
	if !ok {
		return errInvalidLut
	}
	if len(data) != count {
		return errInvalidLutLength
	}
	return nil
}

func cubeSize(size int) (int, bool) {
	if size <= 0 {
		return 0, false
	}
	n := size
	if n > math.MaxInt/size {
		return 0, false
	}
	n *= size
	if n > math.MaxInt/size {
		return 0, false
	}
	n *= size
	return n, true
}

func sampleLut(lut *Lut3D, c RGB) RGB {
	if lut == nil || lut.Size <= 0 || len(lut.Data) == 0 {
		return RGB{}
	}
	if lut.Size == 1 {
		return clampRGB(lut.Data[0])
	}

	size := lut.Size
	last := size - 1
	fr := clamp01(c.R) * float64(last)
	fg := clamp01(c.G) * float64(last)
	fb := clamp01(c.B) * float64(last)

	r0 := int(math.Floor(fr))
	g0 := int(math.Floor(fg))
	b0 := int(math.Floor(fb))

	// Keep the interpolation inside one cell. At the upper edge frac becomes
	// one and the second corner is the final grid point, never the first point
	// of the next atlas page.
	if r0 > size-2 {
		r0 = size - 2
	}
	if g0 > size-2 {
		g0 = size - 2
	}
	if b0 > size-2 {
		b0 = size - 2
	}
	if r0 < 0 {
		r0 = 0
	}
	if g0 < 0 {
		g0 = 0
	}
	if b0 < 0 {
		b0 = 0
	}

	r1 := r0 + 1
	g1 := g0 + 1
	b1 := b0 + 1
	tr := fr - float64(r0)
	tg := fg - float64(g0)
	tb := fb - float64(b0)

	idx := func(r, g, b int) int {
		return r + g*size + b*size*size
	}

	c000 := lut.Data[idx(r0, g0, b0)]
	c100 := lut.Data[idx(r1, g0, b0)]
	c010 := lut.Data[idx(r0, g1, b0)]
	c110 := lut.Data[idx(r1, g1, b0)]
	c001 := lut.Data[idx(r0, g0, b1)]
	c101 := lut.Data[idx(r1, g0, b1)]
	c011 := lut.Data[idx(r0, g1, b1)]
	c111 := lut.Data[idx(r1, g1, b1)]

	c00 := lerpRGB(c000, c100, tr)
	c10 := lerpRGB(c010, c110, tr)
	c01 := lerpRGB(c001, c101, tr)
	c11 := lerpRGB(c011, c111, tr)
	c0 := lerpRGB(c00, c10, tg)
	c1 := lerpRGB(c01, c11, tg)
	return lerpRGB(c0, c1, tb)
}

func lerpRGB(a, b RGB, t float64) RGB {
	return RGB{
		R: a.R + (b.R-a.R)*t,
		G: a.G + (b.G-a.G)*t,
		B: a.B + (b.B-a.B)*t,
	}
}

func clampRGB(c RGB) RGB {
	return RGB{
		R: clamp01(c.R),
		G: clamp01(c.G),
		B: clamp01(c.B),
	}
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func srgbChannelToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func lutMatches(lut Lut3D, snapshot Lut3D) bool {
	return lutDataEqual(lut, snapshot)
}

func lutDataEqual(a, b Lut3D) bool {
	if a.Size != b.Size || len(a.Data) != len(b.Data) {
		return false
	}
	for i := range a.Data {
		if a.Data[i] != b.Data[i] {
			return false
		}
	}
	return true
}
