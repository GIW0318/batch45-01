package task

import (
	"errors"
	"math"
	"sync"
)

const (
	// maxKernelSamples bounds the per-pixel hemisphere cost. Callers asking
	// for a larger kernel are clamped instead of silently exploding the
	// per-frame sample budget.
	maxKernelSamples = 64

	// temporalWindow is the number of frames used by the stationary-camera
	// temporal filter. After warm-up the blend weight is 1/temporalWindow.
	temporalWindow = 10
)

var (
	errSampleIndex   = errors.New("ssao: kernel sample index out of range")
	errNegativeDepth = errors.New("ssao: negative or non-finite depth")
	errInvalidNormal = errors.New("ssao: normal has zero or non-finite length")
	errAOLocked      = errors.New("ssao: AO buffer is already locked")
	errAOUnlocked    = errors.New("ssao: AO buffer is not locked")
)

// Vec3 is a position or direction in view space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// KernelSample is one sample of the SSAO hemisphere kernel.
type KernelSample struct {
	Dir    Vec3
	Radius float64
}

// SsaoGuard owns the SSAO kernel and the AO buffer.
type SsaoGuard struct {
	sampleCount int

	kernelMu   sync.RWMutex
	kernel     []KernelSample
	cameraID   int
	generation uint64

	aoMu     sync.Mutex
	aoLocked bool

	accumMu     sync.Mutex
	aoAccum     float64
	aoFrames    int
	accumulated bool
}

// NewSsaoGuard returns a guard with sampleCount hemisphere samples.
func NewSsaoGuard(sampleCount int) *SsaoGuard {
	if sampleCount < 0 {
		sampleCount = 0
	}
	if sampleCount > maxKernelSamples {
		sampleCount = maxKernelSamples
	}

	g := &SsaoGuard{sampleCount: sampleCount}
	g.kernel = buildKernel(sampleCount, 0, 0)
	return g
}

// SampleCount returns the number of hemisphere samples.
func (g *SsaoGuard) SampleCount() int {
	return g.sampleCount
}

// KernelSample returns sample i oriented around normal and scaled by depth.
func (g *SsaoGuard) KernelSample(i int, normal Vec3, depth float64) (KernelSample, error) {
	if i < 0 || i >= g.sampleCount {
		return KernelSample{}, errSampleIndex
	}
	if !finite(depth) || depth < 0 {
		return KernelSample{}, errNegativeDepth
	}

	n, ok := normalize(normal)
	if !ok {
		return KernelSample{}, errInvalidNormal
	}

	g.kernelMu.RLock()
	base := g.kernel[i]
	g.kernelMu.RUnlock()

	tangent, bitangent := basis(n)
	dir := Vec3{
		X: tangent.X*base.Dir.X + bitangent.X*base.Dir.Y + n.X*base.Dir.Z,
		Y: tangent.Y*base.Dir.X + bitangent.Y*base.Dir.Y + n.Y*base.Dir.Z,
		Z: tangent.Z*base.Dir.X + bitangent.Z*base.Dir.Y + n.Z*base.Dir.Z,
	}
	if d, ok := normalize(dir); ok {
		dir = d
	}

	// The basis is built from the normal, so the hemisphere already points
	// the right way. Keep the explicit correction for degenerate bases and
	// floating point noise.
	if dot(dir, n) <= 0 {
		dir = Vec3{-dir.X, -dir.Y, -dir.Z}
	}

	return KernelSample{
		Dir:    dir,
		Radius: base.Radius * depth,
	}, nil
}

// ReconstructViewPos rebuilds the view space position of a pixel.
//
// px and py are normalized device coordinates and depth is the NDC depth
// value. invProj must be the inverse of the matrix that produced the depth;
// the previous hard-coded projection is deliberately not used here.
func (g *SsaoGuard) ReconstructViewPos(depth float64, px, py int, invProj [16]float64) (Vec3, error) {
	if !finite(depth) || depth < 0 {
		return Vec3{}, errNegativeDepth
	}
	if !finite(float64(px)) || !finite(float64(py)) {
		return Vec3{}, errors.New("ssao: non-finite pixel coordinate")
	}

	// Column-major multiplication, matching the layout used by GL-style
	// projection matrices.
	x := invProj[0]*float64(px) + invProj[4]*float64(py) + invProj[8]*depth + invProj[12]
	y := invProj[1]*float64(px) + invProj[5]*float64(py) + invProj[9]*depth + invProj[13]
	z := invProj[2]*float64(px) + invProj[6]*float64(py) + invProj[10]*depth + invProj[14]
	w := invProj[3]*float64(px) + invProj[7]*float64(py) + invProj[11]*depth + invProj[15]

	if math.Abs(w) > 1e-12 {
		x /= w
		y /= w
		z /= w
	} else {
		return Vec3{}, errors.New("ssao: inverse projection produced an unprojectable point")
	}

	pos := Vec3{X: x, Y: y, Z: z}
	if !finite(pos.X) || !finite(pos.Y) || !finite(pos.Z) {
		return Vec3{}, errors.New("ssao: inverse projection produced a non-finite position")
	}
	return pos, nil
}

// AppliesToTransparent reports whether transparent geometry receives AO.
func (g *SsaoGuard) AppliesToTransparent() bool {
	return false
}

// DenoiseWeight returns the blur weight of a neighbour sample.
func (g *SsaoGuard) DenoiseWeight(depthDiff, normalDiff float64) float64 {
	if math.IsNaN(depthDiff) || math.IsNaN(normalDiff) {
		return 0
	}
	depthDiff = math.Abs(depthDiff)
	normalDiff = math.Abs(normalDiff)

	// A bilateral-style falloff: neighbours on the far side of a depth or
	// normal edge contribute almost nothing, so geometry edges stay sharp.
	w := math.Exp(-8*depthDiff) * math.Exp(-4*normalDiff)
	if w < 0 || math.IsNaN(w) {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

// RadiusInWorld converts a screen space radius into a world space radius.
func (g *SsaoGuard) RadiusInWorld(screenRadius, depth, projScale float64) float64 {
	if math.IsNaN(screenRadius) || math.IsNaN(depth) || math.IsNaN(projScale) {
		return 0
	}
	screenRadius = math.Abs(screenRadius)
	depth = math.Abs(depth)
	projScale = math.Abs(projScale)
	if projScale == 0 {
		return 0
	}
	return screenRadius * depth / projScale
}

// IndirectLightDoubleCounts reports whether AO also attenuates indirect light.
func (g *SsaoGuard) IndirectLightDoubleCounts(ssao float64) bool {
	return false
}

// ClampUV clamps a UV coordinate into the valid [0,1] range.
func (g *SsaoGuard) ClampUV(u, v float64) (float64, float64) {
	return clamp01(u), clamp01(v)
}

// IntensityResolutionIndependent returns the AO intensity at a resolution.
func (g *SsaoGuard) IntensityResolutionIndependent(intensity float64, w, h int) float64 {
	if math.IsNaN(intensity) || math.IsInf(intensity, 0) || intensity < 0 {
		return 0
	}
	// Resolution belongs in the projection/radius conversion, never in the
	// intensity itself.
	return intensity
}

// RebuildKernel regenerates the kernel for a new camera.
func (g *SsaoGuard) RebuildKernel(cameraID int) error {
	g.kernelMu.Lock()
	g.cameraID = cameraID
	g.generation++
	g.kernel = buildKernel(g.sampleCount, cameraID, g.generation)
	g.kernelMu.Unlock()

	// History from the previous camera is not valid for the new kernel.
	g.ResetAOAccumulation()
	return nil
}

// KernelGeneration returns how often the kernel was rebuilt.
func (g *SsaoGuard) KernelGeneration() uint64 {
	g.kernelMu.RLock()
	defer g.kernelMu.RUnlock()
	return g.generation
}

// LockAOBuffer takes the write lock of the AO buffer.
func (g *SsaoGuard) LockAOBuffer() error {
	g.aoMu.Lock()
	defer g.aoMu.Unlock()
	if g.aoLocked {
		return errAOLocked
	}
	g.aoLocked = true
	return nil
}

// UnlockAOBuffer releases the write lock of the AO buffer.
func (g *SsaoGuard) UnlockAOBuffer() error {
	g.aoMu.Lock()
	defer g.aoMu.Unlock()
	if !g.aoLocked {
		return errAOUnlocked
	}
	g.aoLocked = false
	return nil
}

// AccumulateAO blends one AO frame into the temporal accumulation buffer and
// returns the filtered value. A stationary camera converges monotonically;
// after the warm-up window each additional frame changes the result by at
// most 1/10 of the frame-to-frame difference.
func (g *SsaoGuard) AccumulateAO(sample float64) float64 {
	if math.IsNaN(sample) || math.IsInf(sample, 0) {
		sample = 0
	}

	g.accumMu.Lock()
	defer g.accumMu.Unlock()

	if !g.accumulated {
		g.aoAccum = sample
		g.aoFrames = 1
		g.accumulated = true
		return g.aoAccum
	}

	if g.aoFrames < temporalWindow {
		g.aoFrames++
	}
	alpha := 1.0 / float64(g.aoFrames)
	g.aoAccum += (sample - g.aoAccum) * alpha
	return g.aoAccum
}

// AOAccumulated returns the current temporally accumulated AO value.
func (g *SsaoGuard) AOAccumulated() float64 {
	g.accumMu.Lock()
	defer g.accumMu.Unlock()
	return g.aoAccum
}

// ResetAOAccumulation drops temporal history, for example after a camera cut
// or when the old kernel is replaced.
func (g *SsaoGuard) ResetAOAccumulation() {
	g.accumMu.Lock()
	g.aoAccum = 0
	g.aoFrames = 0
	g.accumulated = false
	g.accumMu.Unlock()
}

// RunsBeforeTonemap reports whether SSAO is applied before tonemapping.
func (g *SsaoGuard) RunsBeforeTonemap() bool {
	return true
}

// buildKernel creates a deterministic cosine-weighted hemisphere around +Z.
// The camera and generation perturb the rotation so a rebuilt kernel is a
// genuinely new one, while repeated frame reads stay stable.
func buildKernel(n int, cameraID int, generation uint64) []KernelSample {
	if n <= 0 {
		return nil
	}

	const goldenAngle = 2.39996322972865332 // pi * (3 - sqrt(5))
	phase := float64(cameraID)*0.61803398875 + float64(generation)*0.38196601125

	kernel := make([]KernelSample, n)
	for i := 0; i < n; i++ {
		u := (float64(i) + 0.5) / float64(n)
		z := 0.05 + 0.95*u
		r := math.Sqrt(math.Max(0, 1-z*z))
		phi := phase + float64(i)*goldenAngle

		kernel[i] = KernelSample{
			Dir: Vec3{
				X: r * math.Cos(phi),
				Y: r * math.Sin(phi),
				Z: z,
			},
			// A stratified radius keeps expensive samples near the pixel
			// while still covering the full hemisphere.
			Radius: 0.1 + 0.9*math.Sqrt(u),
		}
	}
	return kernel
}

func basis(n Vec3) (Vec3, Vec3) {
	up := Vec3{Z: 1}
	if math.Abs(n.Z) > 0.999 {
		up = Vec3{Y: 1}
	}
	tangent, _ := normalize(cross(up, n))
	bitangent := cross(n, tangent)
	return tangent, bitangent
}

func normalize(v Vec3) (Vec3, bool) {
	length := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
	if length < 1e-12 || math.IsNaN(length) || math.IsInf(length, 0) {
		return Vec3{}, false
	}
	return Vec3{X: v.X / length, Y: v.Y / length, Z: v.Z / length}, true
}

func cross(a, b Vec3) Vec3 {
	return Vec3{
		X: a.Y*b.Z - a.Z*b.Y,
		Y: a.Z*b.X - a.X*b.Z,
		Z: a.X*b.Y - a.Y*b.X,
	}
}

func dot(a, b Vec3) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
