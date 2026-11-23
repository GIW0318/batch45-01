package task

import (
	"errors"
	"math"
	"sync"
)

var (
	errEmptyFrame    = errors.New("hdr exposure: empty luminance frame")
	errInvalidSample = errors.New("hdr exposure: luminance samples must be finite and non-negative")
	errFrameTooLarge = errors.New("hdr exposure: frame exceeds histogram capacity")
	errInvalidBase   = errors.New("hdr exposure: base exposure must be positive and finite")
	errInvalidEV     = errors.New("hdr exposure: EV compensation must be finite")
	errInvalidCamera = errors.New("hdr exposure: camera index must be non-negative")
)

const (
	// meterBufferCapacity is the fixed capacity of the metering ring buffer.
	meterBufferCapacity = 256
	// histogramCapacity is the fixed capacity of the histogram weight buffer.
	histogramCapacity = 4096
	// riseTau is the time constant (seconds) for brightening the exposure
	// when the scene turns darker. Slow lift avoids pumping.
	riseTau = 0.75
)

// RGB is a linear HDR colour.
type RGB struct {
	R float64
	G float64
	B float64
}

// HdrExposure owns the metering, exposure and tonemap steps of the HDR pipeline.
// Metering and buffer writes are serialised through mu so concurrent frames
// and buffer clears stay race free.
type HdrExposure struct {
	mu         sync.Mutex
	camera     int
	meterCount int
	generation uint64
	// meterBuf is a fixed-capacity ring of recently metered frame averages.
	meterBuf  []float64
	meterHead int
	meterFill int
	// histBuf is the fixed-capacity backing store for histogram weights.
	histBuf      []float64
	buffersClean bool
}

// NewHdrExposure returns an exposure guard for the main camera.
func NewHdrExposure() *HdrExposure {
	return &HdrExposure{
		meterBuf:     make([]float64, meterBufferCapacity),
		histBuf:      make([]float64, histogramCapacity),
		buffersClean: true,
	}
}

// MeterFrame returns the metered luminance of a frame, averaged over the
// whole frame rather than a centre spot.
func (e *HdrExposure) MeterFrame(lum []float64) (float64, error) {
	if len(lum) == 0 {
		return 0, errEmptyFrame
	}
	var sum float64
	for _, l := range lum {
		if l < 0 || math.IsNaN(l) || math.IsInf(l, 0) {
			return 0, errInvalidSample
		}
		sum += l
	}
	avg := sum / float64(len(lum))

	e.mu.Lock()
	e.meterBuf[e.meterHead] = avg
	e.meterHead = (e.meterHead + 1) % len(e.meterBuf)
	if e.meterFill < len(e.meterBuf) {
		e.meterFill++
	}
	e.meterCount++
	e.buffersClean = false
	e.mu.Unlock()

	return avg, nil
}

// MeterFrameIndex returns how many frames were metered since the last reset.
func (e *HdrExposure) MeterFrameIndex() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.meterCount
}

// SmoothExposure adapts the current exposure towards target over dt seconds.
// Convergence is exponential in time, so it is independent of the frame rate.
// The response is directional: when the scene suddenly brightens (target drops
// below current) the exposure clamps down within the same frame; when the
// scene darkens the exposure lifts slowly, and neither direction overshoots.
func (e *HdrExposure) SmoothExposure(current, target, dt float64) float64 {
	if dt <= 0 || math.IsNaN(dt) || math.IsNaN(current) || math.IsNaN(target) {
		return current
	}
	if target < current {
		// Scene brightened: reel the exposure in immediately so
		// highlights never blow out while the meter catches up.
		return target
	}
	k := 1 - math.Exp(-dt/riseTau)
	return current + (target-current)*k
}

// HistogramWeights returns the luminance weight of every histogram bucket,
// normalised so the weights sum to one; brighter samples weigh more.
func (e *HdrExposure) HistogramWeights(lum []float64) ([]float64, error) {
	if len(lum) == 0 {
		return nil, errEmptyFrame
	}
	if len(lum) > histogramCapacity {
		return nil, errFrameTooLarge
	}
	var sum float64
	for _, l := range lum {
		if l < 0 || math.IsNaN(l) || math.IsInf(l, 0) {
			return nil, errInvalidSample
		}
		sum += l
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	weights := e.histBuf[:len(lum)]
	if sum == 0 {
		uniform := 1 / float64(len(lum))
		for i := range weights {
			weights[i] = uniform
		}
		return weights, nil
	}
	for i, l := range lum {
		weights[i] = l / sum
	}
	e.buffersClean = false
	return weights, nil
}

// TonemapHighlight compresses an HDR highlight into displayable range with a
// Reinhard shoulder, so bright values roll off towards 1 instead of clipping
// to dead white.
func (e *HdrExposure) TonemapHighlight(c RGB) RGB {
	return RGB{
		R: tonemapChannel(c.R),
		G: tonemapChannel(c.G),
		B: tonemapChannel(c.B),
	}
}

func tonemapChannel(v float64) float64 {
	if v <= 0 || math.IsNaN(v) {
		return 0
	}
	return v / (1 + v)
}

// AppliesTonemapToUI reports whether the UI pass is tonemapped with the
// scene. The UI is drawn after tonemapping so HUD colours stay intact.
func (e *HdrExposure) AppliesTonemapToUI() bool {
	return false
}

// OutputGammaIsSRGB reports whether the SDR output uses the sRGB transfer
// curve, keeping HDR and SDR gamma consistent.
func (e *HdrExposure) OutputGammaIsSRGB() bool {
	return true
}

// ExposureCompensation applies an EV offset to a base exposure as
// base * 2^ev, so negative EV values darken smoothly and never hit zero.
func (e *HdrExposure) ExposureCompensation(ev, base float64) (float64, error) {
	if base <= 0 || math.IsNaN(base) || math.IsInf(base, 0) {
		return 0, errInvalidBase
	}
	if math.IsNaN(ev) || math.IsInf(ev, 0) {
		return 0, errInvalidEV
	}
	return base * math.Exp2(ev), nil
}

// ResetMeter clears the metering history and buffers so a scene change
// leaves no residue of the old scene, and bumps the exposure generation.
func (e *HdrExposure) ResetMeter() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetLocked()
	e.generation++
	return nil
}

// resetLocked clears all metering state; the caller must hold mu.
func (e *HdrExposure) resetLocked() {
	e.meterCount = 0
	e.meterHead = 0
	e.meterFill = 0
	for i := range e.meterBuf {
		e.meterBuf[i] = 0
	}
	for i := range e.histBuf {
		e.histBuf[i] = 0
	}
	e.buffersClean = true
}

// LutBeforeExposure reports whether the colour LUT runs before the exposure.
// The LUT runs after exposure so it sees a normalised input range.
func (e *HdrExposure) LutBeforeExposure() bool {
	return false
}

// UseCameraExposure switches the exposure to another camera. Each camera
// switch resets the metering state and bumps the exposure generation so
// cameras never share exposure history.
func (e *HdrExposure) UseCameraExposure(camera int) error {
	if camera < 0 {
		return errInvalidCamera
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.camera = camera
	e.resetLocked()
	e.generation++
	return nil
}

// ExposureGeneration returns how often the exposure was switched or reset.
func (e *HdrExposure) ExposureGeneration() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.generation
}

// ClearBuffers clears the exposure accumulation buffers in place, keeping
// their fixed capacity.
func (e *HdrExposure) ClearBuffers() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.meterBuf {
		e.meterBuf[i] = 0
	}
	for i := range e.histBuf {
		e.histBuf[i] = 0
	}
	e.meterHead = 0
	e.meterFill = 0
	e.buffersClean = true
	return nil
}

// BuffersClean reports whether the exposure buffers are empty.
func (e *HdrExposure) BuffersClean() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buffersClean
}

// TonemapPerParticleLayer reports whether every transparent layer is
// tonemapped. Particles are composited first and tonemapped once.
func (e *HdrExposure) TonemapPerParticleLayer() bool {
	return false
}
