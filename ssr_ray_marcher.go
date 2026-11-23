package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Vec3 is a position or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// SsrMarcher marches screen space reflection rays through the depth buffer.
type SsrMarcher struct {
	maxSteps int
}

// NewSsrMarcher returns a marcher that spends at most maxSteps steps per ray.
func NewSsrMarcher(maxSteps int) *SsrMarcher {
	return &SsrMarcher{}
}

// MaxSteps returns the step budget of one ray.
func (m *SsrMarcher) MaxSteps() int {
	return 0
}

// StepSize returns the ray step length for a surface roughness.
func (m *SsrMarcher) StepSize(roughness float64) (float64, error) {
	return 0, errNotImplemented
}

// StepsForDistance returns how many steps a ray of the given length needs.
func (m *SsrMarcher) StepsForDistance(distance float64) (int, error) {
	return 0, errNotImplemented
}

// ConfirmHit decides whether a march sample is a real reflection hit.
func (m *SsrMarcher) ConfirmHit(sceneDepth, rayDepth, normalDot float64) bool {
	return false
}

// RayThickness returns the depth thickness used to test thin geometry.
func (m *SsrMarcher) RayThickness() float64 {
	return 0
}

// EdgeRayFallback returns the probe fallback for a ray that leaves the screen.
func (m *SsrMarcher) EdgeRayFallback(uv Vec3) (Vec3, bool) {
	return Vec3{}, false
}

// MipLevelForRoughness returns the reflection mip level of a rough surface.
func (m *SsrMarcher) MipLevelForRoughness(roughness float64) int {
	return -1
}

// BilateralWeight returns the spatial weight of a reflection sample.
func (m *SsrMarcher) BilateralWeight(depthDiff, normalDiff float64) float64 {
	return 0
}

// BlendWithProbe mixes the screen space reflection with the reflection probe.
func (m *SsrMarcher) BlendWithProbe(ssr, probe Vec3, ssrWeight float64) Vec3 {
	return Vec3{}
}

// SetCamera switches the camera that owns the reflection history.
func (m *SsrMarcher) SetCamera(id int) error {
	return errNotImplemented
}

// ReflectionGeneration returns how often the reflection history was reset.
func (m *SsrMarcher) ReflectionGeneration() uint64 {
	return 0
}

// LockRayBuffer takes the write lock of the shared ray buffer.
func (m *SsrMarcher) LockRayBuffer() error {
	return errNotImplemented
}

// UnlockRayBuffer releases the write lock of the shared ray buffer.
func (m *SsrMarcher) UnlockRayBuffer() error {
	return errNotImplemented
}

// BeforeTransparent reports whether SSR resolves before the transparent pass.
func (m *SsrMarcher) BeforeTransparent() bool {
	return false
}

// WorksInHDR reports whether reflections are computed before tonemapping.
func (m *SsrMarcher) WorksInHDR() bool {
	return false
}
