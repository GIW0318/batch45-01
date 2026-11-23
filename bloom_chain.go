package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// RGB is a linear HDR colour.
type RGB struct {
	R float64
	G float64
	B float64
}

// BloomChain runs the threshold, downsample, upsample and composite steps.
type BloomChain struct {
	mips int
}

// NewBloomChain returns a chain with mips downsample levels.
func NewBloomChain(mips int) *BloomChain {
	return &BloomChain{}
}

// BrightPass keeps the samples above threshold and zeroes the rest.
func (b *BloomChain) BrightPass(hdr []RGB, threshold float64) ([]RGB, error) {
	return nil, errNotImplemented
}

// KarisAverage combines the 2x2 samples of one downsample cell, the centre
// tap carries half of the weight to suppress single bright pixels.
func (b *BloomChain) KarisAverage(samples []RGB) (RGB, error) {
	return RGB{}, errNotImplemented
}

// Downsample halves the resolution of src.
func (b *BloomChain) Downsample(src []RGB, w, h int) (out []RGB, ow, oh int, err error) {
	return nil, 0, 0, errNotImplemented
}

// UpsampleWeighted combines the mip levels with normalized weights.
func (b *BloomChain) UpsampleWeighted(levels [][]RGB, weights []float64) (RGB, error) {
	return RGB{}, errNotImplemented
}

// IncludesUI reports whether UI pixels take part in the bloom chain.
func (b *BloomChain) IncludesUI() bool {
	return true
}

// MipCount returns the number of downsample levels.
func (b *BloomChain) MipCount() int {
	return 0
}

// AccumulateIntensity advances the smoothed bloom intensity by dt seconds.
func (b *BloomChain) AccumulateIntensity(dt, base float64) float64 {
	return 0
}

// TonemapHighlight compresses an HDR highlight into displayable range.
func (b *BloomChain) TonemapHighlight(c RGB) RGB {
	return RGB{}
}

// ClearBuffers clears the bloom buffers before the next frame.
func (b *BloomChain) ClearBuffers() error {
	return errNotImplemented
}

// BuffersClean reports whether the bloom buffers hold no stale frame.
func (b *BloomChain) BuffersClean() bool {
	return false
}

// SetCamera switches the camera that feeds the chain.
func (b *BloomChain) SetCamera(id int) error {
	return errNotImplemented
}

// AdvanceFrame records that one frame was composited.
func (b *BloomChain) AdvanceFrame() error {
	return errNotImplemented
}

// AccumulatedFrame returns the frames composited since the last reset.
func (b *BloomChain) AccumulatedFrame() int {
	return 0
}

// ExposureBeforeBloom reports whether exposure runs before the bloom pass.
func (b *BloomChain) ExposureBeforeBloom() bool {
	return false
}

// CompositeAfterTransparent reports whether bloom is composited after the
// transparent pass.
func (b *BloomChain) CompositeAfterTransparent() bool {
	return true
}
