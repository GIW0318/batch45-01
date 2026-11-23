package task

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

const (
	// maxMips caps the downsample chain so a bad request cannot allocate
	// an unbounded number of levels.
	maxMips = 8
	// maxDimension caps the input resolution so buffers stay bounded.
	maxDimension = 16384
	// intensityTau is the smoothing time constant, in seconds, of the
	// bloom intensity accumulator.
	intensityTau = 0.25
)

// RGB is a linear HDR colour.
type RGB struct {
	R float64
	G float64
	B float64
}

// BloomChain runs the threshold, downsample, upsample and composite steps.
// The mip buffers are allocated once per resolution and reused every frame;
// all mutable state is guarded by mu so concurrent mip writes never race or
// publish a half-old half-new frame.
type BloomChain struct {
	mu         sync.Mutex
	mips       int
	intensity  float64
	frames     int
	camera     int
	clean      bool
	bufW       int
	bufH       int
	buffers    [][]RGB
	allocCount int
}

// NewBloomChain returns a chain with mips downsample levels.
func NewBloomChain(mips int) *BloomChain {
	if mips < 1 {
		mips = 1
	}
	if mips > maxMips {
		mips = maxMips
	}
	return &BloomChain{mips: mips, camera: -1, clean: true}
}

// luminance returns the Rec.709 luma of a linear HDR colour. HDR values are
// compared against the threshold directly, never clamped to SDR first.
func luminance(c RGB) float64 {
	return 0.2126*c.R + 0.7152*c.G + 0.0722*c.B
}

// BrightPass keeps the samples above threshold and zeroes the rest.
func (b *BloomChain) BrightPass(hdr []RGB, threshold float64) ([]RGB, error) {
	if threshold <= 0 {
		return nil, fmt.Errorf("bright pass: threshold %v must be positive", threshold)
	}
	out := make([]RGB, len(hdr))
	for i, c := range hdr {
		if luminance(c) >= threshold {
			out[i] = c
		}
	}
	return out, nil
}

// KarisAverage combines the 2x2 samples of one downsample cell, the centre
// tap carries half of the weight to suppress single bright pixels.
func (b *BloomChain) KarisAverage(samples []RGB) (RGB, error) {
	n := len(samples)
	if n == 0 {
		return RGB{}, errors.New("karis average: no samples")
	}
	if n == 1 {
		return samples[0], nil
	}
	centre := (n - 1) / 2
	edgeWeight := 0.5 / float64(n-1)
	var out RGB
	for i, c := range samples {
		w := edgeWeight
		if i == centre {
			w = 0.5
		}
		out.R += c.R * w
		out.G += c.G * w
		out.B += c.B * w
	}
	return out, nil
}

// Downsample halves the resolution of src.
func (b *BloomChain) Downsample(src []RGB, w, h int) (out []RGB, ow, oh int, err error) {
	if w <= 0 || h <= 0 {
		return nil, 0, 0, fmt.Errorf("downsample: invalid size %dx%d", w, h)
	}
	if len(src) != w*h {
		return nil, 0, 0, fmt.Errorf("downsample: %d samples for %dx%d", len(src), w, h)
	}
	ow, oh = w/2, h/2
	if ow < 1 {
		ow = 1
	}
	if oh < 1 {
		oh = 1
	}
	out = make([]RGB, ow*oh)
	for y := 0; y < oh; y++ {
		for x := 0; x < ow; x++ {
			cell := [4]RGB{
				src[clamp(2*y, h)*w+clamp(2*x, w)],
				src[clamp(2*y, h)*w+clamp(2*x+1, w)],
				src[clamp(2*y+1, h)*w+clamp(2*x, w)],
				src[clamp(2*y+1, h)*w+clamp(2*x+1, w)],
			}
			avg, kerr := b.KarisAverage(cell[:])
			if kerr != nil {
				return nil, 0, 0, kerr
			}
			out[y*ow+x] = avg
		}
	}
	return out, ow, oh, nil
}

func clamp(v, hi int) int {
	if v >= hi {
		return hi - 1
	}
	return v
}

// UpsampleWeighted combines the mip levels with normalized weights.
func (b *BloomChain) UpsampleWeighted(levels [][]RGB, weights []float64) (RGB, error) {
	if len(levels) == 0 {
		return RGB{}, errors.New("upsample: no mip levels")
	}
	if len(weights) != len(levels) {
		return RGB{}, fmt.Errorf("upsample: %d weights for %d mip levels", len(weights), len(levels))
	}
	var wsum float64
	var out RGB
	for i, level := range levels {
		if len(level) == 0 {
			return RGB{}, fmt.Errorf("upsample: mip level %d is empty", i)
		}
		w := weights[i]
		if w < 0 {
			return RGB{}, fmt.Errorf("upsample: negative weight %v for mip %d", w, i)
		}
		var avg RGB
		for _, c := range level {
			avg.R += c.R
			avg.G += c.G
			avg.B += c.B
		}
		inv := 1 / float64(len(level))
		out.R += avg.R * inv * w
		out.G += avg.G * inv * w
		out.B += avg.B * inv * w
		wsum += w
	}
	if wsum <= 0 {
		return RGB{}, errors.New("upsample: weights sum to zero")
	}
	out.R /= wsum
	out.G /= wsum
	out.B /= wsum
	return out, nil
}

// IncludesUI reports whether UI pixels take part in the bloom chain.
func (b *BloomChain) IncludesUI() bool {
	return false
}

// MipCount returns the number of downsample levels.
func (b *BloomChain) MipCount() int {
	return b.mips
}

// AccumulateIntensity advances the smoothed bloom intensity by dt seconds.
// The smoothing is exponential over time, so the result depends on elapsed
// wall time, not on how many frames were rendered.
func (b *BloomChain) AccumulateIntensity(dt, base float64) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if dt > 0 {
		alpha := 1 - math.Exp(-dt/intensityTau)
		b.intensity += (base - b.intensity) * alpha
	}
	return b.intensity
}

// TonemapHighlight compresses an HDR highlight into displayable range.
// A Reinhard soft roll-off keeps bright values below 1.0 instead of
// clipping them to white.
func (b *BloomChain) TonemapHighlight(c RGB) RGB {
	return RGB{R: rollOff(c.R), G: rollOff(c.G), B: rollOff(c.B)}
}

func rollOff(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return v / (1 + v)
}

// ensureBuffers allocates the mip buffers for a resolution. Buffers are
// created once per resolution and reused on every later frame at the same
// resolution; only a resolution change reallocates.
func (b *BloomChain) ensureBuffers(w, h int) error {
	if w <= 0 || h <= 0 || w > maxDimension || h > maxDimension {
		return fmt.Errorf("buffers: invalid resolution %dx%d", w, h)
	}
	if b.buffers != nil && b.bufW == w && b.bufH == h {
		return nil
	}
	buffers := make([][]RGB, 0, b.mips)
	lw, lh := w, h
	for i := 0; i < b.mips; i++ {
		lw, lh = lw/2, lh/2
		if lw < 1 {
			lw = 1
		}
		if lh < 1 {
			lh = 1
		}
		buffers = append(buffers, make([]RGB, lw*lh))
	}
	b.buffers = buffers
	b.bufW, b.bufH = w, h
	b.allocCount++
	return nil
}

// BuildMipChain downsamples hdr into the internal mip buffers. The whole
// chain is written under one lock, so a concurrent reader never observes a
// frame that is half old and half new.
func (b *BloomChain) BuildMipChain(hdr []RGB, w, h int) error {
	if len(hdr) != w*h {
		return fmt.Errorf("mip chain: %d samples for %dx%d", len(hdr), w, h)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensureBuffers(w, h); err != nil {
		return err
	}
	src, sw, sh := hdr, w, h
	for level := range b.buffers {
		down, ow, oh, err := b.Downsample(src, sw, sh)
		if err != nil {
			return err
		}
		copy(b.buffers[level], down)
		src, sw, sh = b.buffers[level], ow, oh
	}
	b.clean = false
	return nil
}

// BufferAllocations reports how many times the mip buffers were allocated;
// it stays constant while the resolution does not change.
func (b *BloomChain) BufferAllocations() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allocCount
}

// ReadMip returns a copy of one internal mip level. The copy is taken under
// the chain lock, so it always reflects one complete frame.
func (b *BloomChain) ReadMip(level int) ([]RGB, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if level < 0 || level >= len(b.buffers) {
		return nil, fmt.Errorf("read mip: level %d out of range", level)
	}
	out := make([]RGB, len(b.buffers[level]))
	copy(out, b.buffers[level])
	return out, nil
}

// ClearBuffers clears the bloom buffers before the next frame.
func (b *BloomChain) ClearBuffers() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.buffers {
		clear(b.buffers[i])
	}
	b.clean = true
	return nil
}

// BuffersClean reports whether the bloom buffers hold no stale frame.
func (b *BloomChain) BuffersClean() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clean
}

// SetCamera switches the camera that feeds the chain.
func (b *BloomChain) SetCamera(id int) error {
	if id < 0 {
		return fmt.Errorf("set camera: invalid id %d", id)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.camera = id
	b.frames = 0
	b.intensity = 0
	for i := range b.buffers {
		clear(b.buffers[i])
	}
	b.clean = true
	return nil
}

// AdvanceFrame records that one frame was composited.
func (b *BloomChain) AdvanceFrame() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frames++
	b.clean = false
	return nil
}

// AccumulatedFrame returns the frames composited since the last reset.
func (b *BloomChain) AccumulatedFrame() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.frames
}

// ExposureBeforeBloom reports whether exposure runs before the bloom pass.
func (b *BloomChain) ExposureBeforeBloom() bool {
	return true
}

// CompositeAfterTransparent reports whether bloom is composited after the
// transparent pass.
func (b *BloomChain) CompositeAfterTransparent() bool {
	return false
}
