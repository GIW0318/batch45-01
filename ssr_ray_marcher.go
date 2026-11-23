package task

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
)

var (
	errNegativeRoughness  = errors.New("ssr: roughness must be in [0, 1]")
	errInvalidDistance    = errors.New("ssr: distance must be positive and finite")
	errInvalidCameraID    = errors.New("ssr: camera id must be non-negative")
	errRayBufferBusy      = errors.New("ssr: ray buffer is already locked by another writer")
	errRayBufferNotLocked = errors.New("ssr: ray buffer is not locked")
)

const (
	// baseStep is the march stride of a perfectly smooth surface.
	baseStep = 0.05
	// roughStepGain widens the stride as roughness grows, hiding banding.
	roughStepGain = 4.0
	// stepsPerUnit converts ray length into a step count before the budget clamp.
	stepsPerUnit = 2.0
	// rayThickness is the depth slab that still counts as a surface hit.
	rayThickness = 0.5
	// minNormalDot rejects hits on surfaces seen at a grazing angle.
	minNormalDot = 0.1
	// edgeMargin is the screen border band that falls back to the probe.
	edgeMargin = 0.02
	// maxMip is the coarsest reflection mip used for fully rough surfaces.
	maxMip = 5
	// depthSigma and normalSigma control the bilateral falloff.
	depthSigma  = 0.5
	normalSigma = 0.4
)

// Vec3 is a position or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// SsrMarcher marches screen space reflection rays through the depth buffer.
type SsrMarcher struct {
	maxSteps int

	bufferLock sync.Mutex
	bufferHeld atomic.Bool

	historyMu  sync.Mutex
	cameraID   int
	generation atomic.Uint64
	accum      Vec3
	accumCount uint64
}

// NewSsrMarcher returns a marcher that spends at most maxSteps steps per ray.
func NewSsrMarcher(maxSteps int) *SsrMarcher {
	if maxSteps < 1 {
		maxSteps = 1
	}
	return &SsrMarcher{cameraID: -1, maxSteps: maxSteps}
}

// MaxSteps returns the step budget of one ray.
func (m *SsrMarcher) MaxSteps() int {
	return m.maxSteps
}

// StepSize returns the ray step length for a surface roughness.
func (m *SsrMarcher) StepSize(roughness float64) (float64, error) {
	if math.IsNaN(roughness) || roughness < 0 || roughness > 1 {
		return 0, errNegativeRoughness
	}
	return baseStep * (1 + roughStepGain*roughness), nil
}

// StepsForDistance returns how many steps a ray of the given length needs.
func (m *SsrMarcher) StepsForDistance(distance float64) (int, error) {
	if math.IsNaN(distance) || math.IsInf(distance, 0) || distance <= 0 {
		return 0, errInvalidDistance
	}
	steps := int(math.Ceil(distance * stepsPerUnit))
	if steps < 1 {
		steps = 1
	}
	if steps > m.maxSteps {
		// Over budget: trade precision for cost by clamping the count,
		// which effectively lengthens the stride of far rays.
		steps = m.maxSteps
	}
	return steps, nil
}

// ConfirmHit decides whether a march sample is a real reflection hit.
func (m *SsrMarcher) ConfirmHit(sceneDepth, rayDepth, normalDot float64) bool {
	if normalDot < minNormalDot {
		return false
	}
	behind := rayDepth - sceneDepth
	return behind >= 0 && behind <= m.RayThickness()
}

// RayThickness returns the depth thickness used to test thin geometry.
func (m *SsrMarcher) RayThickness() float64 {
	return rayThickness
}

// EdgeRayFallback returns the probe fallback for a ray that leaves the screen.
func (m *SsrMarcher) EdgeRayFallback(uv Vec3) (Vec3, bool) {
	if uv.X > edgeMargin && uv.X < 1-edgeMargin &&
		uv.Y > edgeMargin && uv.Y < 1-edgeMargin {
		return Vec3{}, false
	}
	probe := Vec3{
		X: clamp(uv.X, 0, 1)*2 - 1,
		Y: clamp(uv.Y, 0, 1)*2 - 1,
		Z: 1,
	}
	return normalize(probe), true
}

// MipLevelForRoughness returns the reflection mip level of a rough surface.
func (m *SsrMarcher) MipLevelForRoughness(roughness float64) int {
	r := clamp(roughness, 0, 1)
	level := int(math.Round(r * maxMip))
	if level > maxMip {
		level = maxMip
	}
	return level
}

// BilateralWeight returns the spatial weight of a reflection sample.
func (m *SsrMarcher) BilateralWeight(depthDiff, normalDiff float64) float64 {
	d := depthDiff / depthSigma
	n := normalDiff / normalSigma
	return math.Exp(-0.5 * (d*d + n*n))
}

// BlendWithProbe mixes the screen space reflection with the reflection probe.
func (m *SsrMarcher) BlendWithProbe(ssr, probe Vec3, ssrWeight float64) Vec3 {
	w := clamp(ssrWeight, 0, 1)
	return Vec3{
		X: ssr.X*w + probe.X*(1-w),
		Y: ssr.Y*w + probe.Y*(1-w),
		Z: ssr.Z*w + probe.Z*(1-w),
	}
}

// SetCamera switches the camera that owns the reflection history.
func (m *SsrMarcher) SetCamera(id int) error {
	if id < 0 {
		return errInvalidCameraID
	}
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	if id != m.cameraID {
		m.cameraID = id
		m.generation.Add(1)
		m.accum = Vec3{}
		m.accumCount = 0
	}
	return nil
}

// ReflectionGeneration returns how often the reflection history was reset.
func (m *SsrMarcher) ReflectionGeneration() uint64 {
	return m.generation.Load()
}

// AccumulateHit folds a confirmed hit sample into the temporal history and
// returns the running average, so noise converges while the camera is still.
func (m *SsrMarcher) AccumulateHit(sample Vec3) Vec3 {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	m.accumCount++
	w := 1 / float64(m.accumCount)
	m.accum.X += (sample.X - m.accum.X) * w
	m.accum.Y += (sample.Y - m.accum.Y) * w
	m.accum.Z += (sample.Z - m.accum.Z) * w
	return m.accum
}

// LockRayBuffer takes the write lock of the shared ray buffer.
func (m *SsrMarcher) LockRayBuffer() error {
	if !m.bufferLock.TryLock() {
		return errRayBufferBusy
	}
	m.bufferHeld.Store(true)
	return nil
}

// UnlockRayBuffer releases the write lock of the shared ray buffer.
func (m *SsrMarcher) UnlockRayBuffer() error {
	if !m.bufferHeld.CompareAndSwap(true, false) {
		return errRayBufferNotLocked
	}
	m.bufferLock.Unlock()
	return nil
}

// BeforeTransparent reports whether SSR resolves before the transparent pass.
func (m *SsrMarcher) BeforeTransparent() bool {
	return true
}

// WorksInHDR reports whether reflections are computed before tonemapping.
func (m *SsrMarcher) WorksInHDR() bool {
	return true
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func normalize(v Vec3) Vec3 {
	l := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
	if l == 0 {
		return Vec3{Z: 1}
	}
	return Vec3{X: v.X / l, Y: v.Y / l, Z: v.Z / l}
}
