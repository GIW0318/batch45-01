package task

import (
	"errors"
	"log"
	"math"
	"sync"
	"time"
)

var (
	errInvalidInput  = errors.New("invalid particle sorter input")
	errOutOfCapacity = errors.New("particle sorter capacity exceeded")
	errInvalidSlot   = errors.New("invalid particle slot")
	errStaleCamera   = errors.New("stale camera generation")
)

const sortTimeBudget = 2 * time.Millisecond

// Vec3 is a position or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// BlendMode is the blend state of a particle material.
type BlendMode int

const (
	// BlendAlpha is classic alpha blending.
	BlendAlpha BlendMode = iota
	// BlendAdditive is additive blending.
	BlendAdditive
)

// Particle is one live particle of the simulation.
type Particle struct {
	ID     int
	Pos    Vec3
	Radius float64
	Blend  BlendMode
	Alive  bool
}

type sortEntry struct {
	key uint64
	idx int
}

// ParticleSorter sorts and batches particles for the transparent pass.
type ParticleSorter struct {
	capacity int

	camMu      sync.RWMutex
	camera     Vec3
	generation uint64

	sortMu      sync.Mutex
	scratch     []Particle
	entries     []sortEntry
	entryTmp    []sortEntry
	sortWarns   uint64
	lastSortDur time.Duration

	slotMu     sync.Mutex
	free       []int
	used       []bool
	active     int
	frame      []Particle
	frameSlots []int
}

// NewParticleSorter returns a sorter with a fixed particle capacity.
func NewParticleSorter(capacity int) *ParticleSorter {
	if capacity < 0 {
		capacity = 0
	}
	s := &ParticleSorter{
		capacity: capacity,
		free:     make([]int, capacity),
		used:     make([]bool, capacity),
		frame:    make([]Particle, capacity),
		scratch:  make([]Particle, capacity),
		entries:  make([]sortEntry, capacity),
		entryTmp: make([]sortEntry, capacity),
	}
	for i := range s.free {
		s.free[i] = capacity - 1 - i
	}
	// Touch the reusable sort buffers once so the first render frame does not
	// pay for faulting in a large freshly allocated arena.
	for i := range s.scratch {
		s.scratch[i].ID = i
	}
	for i := range s.entries {
		s.entries[i] = sortEntry{idx: i}
		s.entryTmp[i] = sortEntry{idx: i}
	}
	return s
}

// Capacity returns the number of particle slots.
func (s *ParticleSorter) Capacity() int {
	return s.capacity
}

// SetCamera records the camera used for the following sorts.
func (s *ParticleSorter) SetCamera(cam Vec3) error {
	if !finiteVec(cam) {
		return errInvalidInput
	}
	s.camMu.Lock()
	if s.generation == math.MaxUint64 {
		s.generation = 1
	} else {
		s.generation++
	}
	s.camera = cam
	s.camMu.Unlock()
	return nil
}

// CameraGeneration returns how many times the camera was moved.
func (s *ParticleSorter) CameraGeneration() uint64 {
	s.camMu.RLock()
	generation := s.generation
	s.camMu.RUnlock()
	return generation
}

// SortBackToFront orders particles from the farthest to the nearest.
func (s *ParticleSorter) SortBackToFront(parts []Particle) ([]Particle, error) {
	s.sortMu.Lock()
	defer s.sortMu.Unlock()

	s.camMu.RLock()
	cam := s.camera
	s.camMu.RUnlock()
	return s.sortLocked(parts, cam)
}

// SortKey returns the depth key of a particle, measured at its farthest extent.
func (s *ParticleSorter) SortKey(p Particle) float64 {
	s.camMu.RLock()
	cam := s.camera
	s.camMu.RUnlock()
	return particleSortKey(p, cam)
}

// SortWithCameraStamp sorts with the camera of the given generation and
// rejects a stamp that no longer matches the current camera.
func (s *ParticleSorter) SortWithCameraStamp(parts []Particle, stamp uint64) ([]Particle, error) {
	s.sortMu.Lock()
	defer s.sortMu.Unlock()

	s.camMu.RLock()
	defer s.camMu.RUnlock()
	if stamp != s.generation {
		return nil, errStaleCamera
	}
	return s.sortLocked(parts, s.camera)
}

// SortStableTies keeps the emission order of particles with equal depth.
func (s *ParticleSorter) SortStableTies(parts []Particle) ([]Particle, error) {
	return s.SortBackToFront(parts)
}

// BatchByBlend splits particles into per blend state batches.
func (s *ParticleSorter) BatchByBlend(parts []Particle) ([][]Particle, error) {
	var alpha, additive []Particle
	for _, p := range parts {
		if err := validateParticle(p); err != nil {
			return nil, err
		}
		switch p.Blend {
		case BlendAlpha:
			alpha = append(alpha, p)
		case BlendAdditive:
			additive = append(additive, p)
		default:
			return nil, errInvalidInput
		}
	}

	batches := make([][]Particle, 0, 2)
	if len(alpha) > 0 {
		batches = append(batches, alpha)
	}
	if len(additive) > 0 {
		batches = append(batches, additive)
	}
	return batches, nil
}

// SampleSoftDepth returns the average depth around texel (x, y) for soft particles.
func (s *ParticleSorter) SampleSoftDepth(depth []float32, w, h, x, y int) (float32, error) {
	if w <= 0 || h <= 0 || x < 0 || x >= w || y < 0 || y >= h {
		return 0, errInvalidInput
	}
	if w > math.MaxInt/h || len(depth) != w*h {
		return 0, errInvalidInput
	}

	var sum float64
	var count int
	for yy := y - 1; yy <= y+1; yy++ {
		if yy < 0 || yy >= h {
			continue
		}
		row := yy * w
		for xx := x - 1; xx <= x+1; xx++ {
			if xx < 0 || xx >= w {
				continue
			}
			d := depth[row+xx]
			if math.IsNaN(float64(d)) || math.IsInf(float64(d), 0) {
				return 0, errInvalidInput
			}
			sum += float64(d)
			count++
		}
	}
	if count == 0 {
		return 0, errInvalidInput
	}
	return float32(sum / float64(count)), nil
}

// AcquireSlot returns a free particle slot.
func (s *ParticleSorter) AcquireSlot() (int, error) {
	s.slotMu.Lock()
	defer s.slotMu.Unlock()

	if len(s.free) == 0 {
		return 0, errOutOfCapacity
	}
	last := len(s.free) - 1
	id := s.free[last]
	s.free = s.free[:last]
	s.used[id] = true
	s.active++
	return id, nil
}

// ReleaseSlot frees a particle slot for reuse.
func (s *ParticleSorter) ReleaseSlot(id int) error {
	s.slotMu.Lock()
	defer s.slotMu.Unlock()

	if id < 0 || id >= s.capacity || !s.used[id] {
		return errInvalidSlot
	}
	s.used[id] = false
	s.frame[id] = Particle{}
	s.free = append(s.free, id)
	s.active--
	return nil
}

// ActiveSlots returns the number of slots currently in use.
func (s *ParticleSorter) ActiveSlots() int {
	s.slotMu.Lock()
	active := s.active
	s.slotMu.Unlock()
	return active
}

// EmitBatch emits a batch of particles for this frame.
func (s *ParticleSorter) EmitBatch(parts []Particle) error {
	for _, p := range parts {
		if err := validateParticle(p); err != nil {
			return err
		}
	}
	if len(parts) == 0 {
		return nil
	}

	s.slotMu.Lock()
	defer s.slotMu.Unlock()

	if len(parts) > len(s.free) {
		return errOutOfCapacity
	}

	start := len(s.free) - len(parts)
	ids := s.free[start:]
	for i, p := range parts {
		id := ids[len(ids)-1-i]
		s.used[id] = true
		s.frame[id] = p
		s.frameSlots = append(s.frameSlots, id)
	}
	s.free = s.free[:start]
	s.active += len(parts)
	return nil
}

// BeginFrame clears the per frame particle buffer.
func (s *ParticleSorter) BeginFrame() error {
	s.slotMu.Lock()
	defer s.slotMu.Unlock()

	for _, id := range s.frameSlots {
		if !s.used[id] {
			continue
		}
		s.used[id] = false
		s.frame[id] = Particle{}
		s.free = append(s.free, id)
		s.active--
	}
	s.frameSlots = s.frameSlots[:0]
	return nil
}

// BlendOrderIndex returns the draw order rank of a blend mode.
func (s *ParticleSorter) BlendOrderIndex(m BlendMode) int {
	switch m {
	case BlendAlpha:
		return 0
	case BlendAdditive:
		return 1
	default:
		return -1
	}
}

// OverflowOverwritesActive reports whether a full buffer recycles live particles.
func (s *ParticleSorter) OverflowOverwritesActive() bool {
	return false
}

// SortWarningCount reports how many sorts exceeded the per frame time budget.
func (s *ParticleSorter) SortWarningCount() uint64 {
	s.sortMu.Lock()
	count := s.sortWarns
	s.sortMu.Unlock()
	return count
}

// LastSortDuration reports the duration of the most recent sort.
func (s *ParticleSorter) LastSortDuration() time.Duration {
	s.sortMu.Lock()
	duration := s.lastSortDur
	s.sortMu.Unlock()
	return duration
}

func (s *ParticleSorter) sortLocked(parts []Particle, cam Vec3) ([]Particle, error) {
	started := time.Now()
	defer func() {
		d := time.Since(started)
		s.lastSortDur = d
		if d > sortTimeBudget {
			s.sortWarns++
			log.Printf("particle sorter: sort took %s, budget is %s", d, sortTimeBudget)
		}
	}()

	n := len(parts)
	if n == 0 {
		return []Particle{}, nil
	}
	if n > s.capacity {
		return nil, errOutOfCapacity
	}
	s.ensureSortBuffers(n)

	for i, p := range parts {
		if err := validateParticle(p); err != nil {
			return nil, err
		}
		key := particleSortKey(p, cam)
		if math.IsNaN(key) || math.IsInf(key, 0) {
			return nil, errInvalidInput
		}
		s.entries[i] = sortEntry{key: descendingFloatBits(key), idx: i}
	}

	entries := s.entries[:n]
	if n == 1 {
		s.scratch[0] = parts[0]
	} else {
		radixSortTo(entries, s.entryTmp[:n], s.scratch[:n], parts)
	}
	return s.scratch[:n], nil
}

func (s *ParticleSorter) ensureSortBuffers(n int) {
	if cap(s.scratch) < n {
		s.scratch = make([]Particle, n)
	}
	s.scratch = s.scratch[:n]
	if cap(s.entries) < n {
		s.entries = make([]sortEntry, n)
	}
	s.entries = s.entries[:n]
	if cap(s.entryTmp) < n {
		s.entryTmp = make([]sortEntry, n)
	}
	s.entryTmp = s.entryTmp[:n]
}

func particleSortKey(p Particle, cam Vec3) float64 {
	dx := p.Pos.X - cam.X
	dy := p.Pos.Y - cam.Y
	dz := p.Pos.Z - cam.Z
	return math.Sqrt(dx*dx+dy*dy+dz*dz) - p.Radius
}

func finiteVec(v Vec3) bool {
	return !math.IsNaN(v.X) && !math.IsInf(v.X, 0) &&
		!math.IsNaN(v.Y) && !math.IsInf(v.Y, 0) &&
		!math.IsNaN(v.Z) && !math.IsInf(v.Z, 0)
}

func validateParticle(p Particle) error {
	if !finiteVec(p.Pos) || math.IsNaN(p.Radius) || math.IsInf(p.Radius, 0) || p.Radius < 0 {
		return errInvalidInput
	}
	if p.Blend != BlendAlpha && p.Blend != BlendAdditive {
		return errInvalidInput
	}
	return nil
}

// descendingFloatBits maps a larger float to a smaller unsigned integer while
// preserving equality, so an ascending radix pass yields far-to-near order.
func descendingFloatBits(v float64) uint64 {
	bits := math.Float64bits(v)
	if bits&(uint64(1)<<63) != 0 {
		bits = ^bits
	} else {
		bits |= uint64(1) << 63
	}
	return ^bits
}

func radixSortTo(items, tmp []sortEntry, out, parts []Particle) {
	src, dst := items, tmp
	var counts [256]int

	for shift := uint(0); shift < 56; shift += 8 {
		for i := range counts {
			counts[i] = 0
		}
		for i := range src {
			counts[byte(src[i].key>>shift)]++
		}

		same := false
		for _, c := range counts {
			if c == len(src) {
				same = true
				break
			}
		}
		if same {
			continue
		}

		var next int
		for i, c := range counts {
			counts[i] = next
			next += c
		}
		for i := range src {
			b := byte(src[i].key >> shift)
			dst[counts[b]] = src[i]
			counts[b]++
		}
		src, dst = dst, src
	}

	// The most significant byte is written straight into the output. This
	// avoids a final entry pass and keeps the stable LSD ordering.
	for i := range counts {
		counts[i] = 0
	}
	for i := range src {
		counts[byte(src[i].key>>56)]++
	}
	var next int
	for i, c := range counts {
		counts[i] = next
		next += c
	}
	for i := range src {
		b := byte(src[i].key >> 56)
		out[counts[b]] = parts[src[i].idx]
		counts[b]++
	}
}
