package task

import "errors"

var errNotImplemented = errors.New("not implemented")

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
}

// NewSsaoGuard returns a guard with sampleCount hemisphere samples.
func NewSsaoGuard(sampleCount int) *SsaoGuard {
	return &SsaoGuard{}
}

// SampleCount returns the number of hemisphere samples.
func (g *SsaoGuard) SampleCount() int {
	return 0
}

// KernelSample returns sample i oriented around normal and scaled by depth.
func (g *SsaoGuard) KernelSample(i int, normal Vec3, depth float64) (KernelSample, error) {
	return KernelSample{}, errNotImplemented
}

// ReconstructViewPos rebuilds the view space position of a pixel.
func (g *SsaoGuard) ReconstructViewPos(depth float64, px, py int, invProj [16]float64) (Vec3, error) {
	return Vec3{}, errNotImplemented
}

// AppliesToTransparent reports whether transparent geometry receives AO.
func (g *SsaoGuard) AppliesToTransparent() bool {
	return true
}

// DenoiseWeight returns the blur weight of a neighbour sample.
func (g *SsaoGuard) DenoiseWeight(depthDiff, normalDiff float64) float64 {
	return 0
}

// RadiusInWorld converts a screen space radius into a world space radius.
func (g *SsaoGuard) RadiusInWorld(screenRadius, depth, projScale float64) float64 {
	return 0
}

// IndirectLightDoubleCounts reports whether AO also attenuates indirect light.
func (g *SsaoGuard) IndirectLightDoubleCounts(ssao float64) bool {
	return true
}

// ClampUV clamps a UV coordinate into the valid [0,1] range.
func (g *SsaoGuard) ClampUV(u, v float64) (float64, float64) {
	return 0, 0
}

// IntensityResolutionIndependent returns the AO intensity at a resolution.
func (g *SsaoGuard) IntensityResolutionIndependent(intensity float64, w, h int) float64 {
	return 0
}

// RebuildKernel regenerates the kernel for a new camera.
func (g *SsaoGuard) RebuildKernel(cameraID int) error {
	return errNotImplemented
}

// KernelGeneration returns how often the kernel was rebuilt.
func (g *SsaoGuard) KernelGeneration() uint64 {
	return 0
}

// LockAOBuffer takes the write lock of the AO buffer.
func (g *SsaoGuard) LockAOBuffer() error {
	return errNotImplemented
}

// UnlockAOBuffer releases the write lock of the AO buffer.
func (g *SsaoGuard) UnlockAOBuffer() error {
	return errNotImplemented
}

// RunsBeforeTonemap reports whether SSAO is applied before tonemapping.
func (g *SsaoGuard) RunsBeforeTonemap() bool {
	return false
}
