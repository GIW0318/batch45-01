package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// RGB is a linear HDR colour.
type RGB struct {
	R float64
	G float64
	B float64
}

// HdrExposure owns the metering, exposure and tonemap steps of the HDR pipeline.
type HdrExposure struct {
	camera int
}

// NewHdrExposure returns an exposure guard for the main camera.
func NewHdrExposure() *HdrExposure {
	return &HdrExposure{}
}

// MeterFrame returns the metered luminance of a frame.
func (e *HdrExposure) MeterFrame(lum []float64) (float64, error) {
	return 0, errNotImplemented
}

// MeterFrameIndex returns how many frames were metered since the last reset.
func (e *HdrExposure) MeterFrameIndex() int {
	return 0
}

// SmoothExposure adapts the current exposure towards target over dt seconds.
func (e *HdrExposure) SmoothExposure(current, target, dt float64) float64 {
	return 0
}

// HistogramWeights returns the luminance weight of every histogram bucket.
func (e *HdrExposure) HistogramWeights(lum []float64) ([]float64, error) {
	return nil, errNotImplemented
}

// TonemapHighlight compresses an HDR highlight into displayable range.
func (e *HdrExposure) TonemapHighlight(c RGB) RGB {
	return RGB{}
}

// AppliesTonemapToUI reports whether the UI pass is tonemapped with the scene.
func (e *HdrExposure) AppliesTonemapToUI() bool {
	return true
}

// OutputGammaIsSRGB reports whether the SDR output uses the sRGB transfer curve.
func (e *HdrExposure) OutputGammaIsSRGB() bool {
	return false
}

// ExposureCompensation applies an EV offset to a base exposure.
func (e *HdrExposure) ExposureCompensation(ev, base float64) (float64, error) {
	return 0, errNotImplemented
}

// ResetMeter clears the metering history.
func (e *HdrExposure) ResetMeter() error {
	return errNotImplemented
}

// LutBeforeExposure reports whether the colour LUT runs before the exposure.
func (e *HdrExposure) LutBeforeExposure() bool {
	return true
}

// UseCameraExposure switches the exposure to another camera.
func (e *HdrExposure) UseCameraExposure(camera int) error {
	return errNotImplemented
}

// ExposureGeneration returns how often the exposure was switched or reset.
func (e *HdrExposure) ExposureGeneration() uint64 {
	return 0
}

// ClearBuffers clears the exposure accumulation buffers.
func (e *HdrExposure) ClearBuffers() error {
	return errNotImplemented
}

// BuffersClean reports whether the exposure buffers are empty.
func (e *HdrExposure) BuffersClean() bool {
	return false
}

// TonemapPerParticleLayer reports whether every transparent layer is tonemapped.
func (e *HdrExposure) TonemapPerParticleLayer() bool {
	return true
}
