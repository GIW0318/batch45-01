package task

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	errInvalidConfig    = errors.New("audio resampler: rates and channels must be positive")
	errInvalidInput     = errors.New("audio resampler: input is not a whole number of frames")
	errCapacityExceeded = errors.New("audio resampler: append would exceed capacity")
)

const historyFrames = 2

// AudioResampler converts interleaved frames between two sample rates.
type AudioResampler struct {
	mu sync.Mutex

	inRate   int
	outRate  int
	channels int

	// inFrameCount and outFrameCount are stream-global counters. They make the
	// position of every output frame a function of the integer expression in
	// Resample rather than of accumulated floating point error.
	inFrameCount  int64
	outFrameCount int64

	// hist holds the last input frames from previous Resample calls.
	hist []float32

	outputLen atomic.Int64
}

// NewAudioResampler creates a resampler for the given rates and channel count.
func NewAudioResampler(inRate, outRate, channels int) (*AudioResampler, error) {
	if inRate <= 0 || outRate <= 0 || channels <= 0 {
		return nil, errInvalidConfig
	}
	return &AudioResampler{
		inRate:   inRate,
		outRate:  outRate,
		channels: channels,
	}, nil
}

// Ratio returns outRate / inRate.
func (r *AudioResampler) Ratio() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inRate <= 0 {
		return 0
	}
	return float64(r.outRate) / float64(r.inRate)
}

// Resample converts interleaved input frames to the output rate.
//
// Output frame k is sampled from the input at
//
//	p(k) = inRate * (2k+1) / (2*outRate)
//
// The half-sample offset makes the filter causal: it only needs frames
// floor(p)-1 and floor(p), never a future frame. Both counters and the
// fractional position are kept as integers, so the result does not depend on
// how the input stream is split into calls.
func (r *AudioResampler) Resample(in []float32) ([]float32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.inRate <= 0 || r.outRate <= 0 || r.channels <= 0 {
		return nil, errInvalidConfig
	}
	if len(in)%r.channels != 0 {
		return nil, errInvalidInput
	}

	nFrames := len(in) / r.channels
	if nFrames == 0 {
		return nil, nil
	}

	ch := r.channels
	oldInCount := r.inFrameCount
	oldOutCount := r.outFrameCount
	histFrames := len(r.hist) / ch
	histLen := len(r.hist)
	histBase := oldInCount - int64(histFrames)
	endFrame := oldInCount + int64(nFrames) - 1

	out := make([]float32, 0, r.estimateOutputFrames(nFrames)*ch)
	num := int64(r.inRate) * (2*oldOutCount + 1)
	den := int64(2 * r.outRate)
	step := int64(2 * r.inRate)

	for {
		base := num / den
		if base > endFrame {
			break
		}

		frac := float64(num%den) / float64(den)
		prev := base - 1

		// Offsets below are relative to the virtual [history, input] slice.
		prevOff := -1
		switch {
		case prev < 0:
			// The left edge is zero padded.
		case prev >= oldInCount:
			prevOff = histLen + int(prev-oldInCount)*ch
		case prev >= histBase:
			prevOff = int(prev-histBase) * ch
		}

		curOff := -1
		switch {
		case base >= oldInCount:
			curOff = histLen + int(base-oldInCount)*ch
		case base >= histBase:
			curOff = int(base-histBase) * ch
		default:
			// History was dropped by ResetBuffer. Treat the missing sample as
			// silence while keeping the stream position and phase intact.
		}

		start := len(out)
		out = out[:start+ch]
		for c := 0; c < ch; c++ {
			var left, right float32
			if prevOff >= 0 {
				if prevOff+c < histLen {
					left = r.hist[prevOff+c]
				} else {
					left = in[prevOff+c-histLen]
				}
			}
			if curOff >= 0 {
				if curOff+c < histLen {
					right = r.hist[curOff+c]
				} else {
					right = in[curOff+c-histLen]
				}
			}
			out[start+c] = left*float32(1-frac) + right*float32(frac)
		}

		num += step
		oldOutCount++
	}

	r.inFrameCount = oldInCount + int64(nFrames)
	r.outFrameCount = oldOutCount
	r.updateHistory(in, nFrames)
	return out, nil
}

func (r *AudioResampler) estimateOutputFrames(nFrames int) int {
	n := int(int64(nFrames) * int64(r.outRate) / int64(r.inRate))
	if n < 0 {
		return 0
	}
	// A couple of slots absorb the half-sample offset and rounding.
	return n + 2
}

func (r *AudioResampler) updateHistory(in []float32, nFrames int) {
	ch := r.channels
	if nFrames <= 0 {
		return
	}

	if nFrames >= historyFrames {
		need := historyFrames * ch
		if cap(r.hist) < need {
			r.hist = make([]float32, need)
		} else {
			r.hist = r.hist[:need]
		}
		copy(r.hist, in[(nFrames-historyFrames)*ch:nFrames*ch])
		return
	}

	// One new frame: keep the previous last frame, if it is still present.
	oldFrames := len(r.hist) / ch
	if oldFrames == 0 {
		if cap(r.hist) < ch {
			r.hist = make([]float32, ch)
		} else {
			r.hist = r.hist[:ch]
		}
		copy(r.hist, in[:ch])
		return
	}

	need := 2 * ch
	if cap(r.hist) < need {
		next := make([]float32, need)
		copy(next, r.hist[(oldFrames-1)*ch:oldFrames*ch])
		copy(next[ch:], in[:ch])
		r.hist = next
		return
	}

	r.hist = r.hist[:need]
	copy(r.hist[:ch], r.hist[(oldFrames-1)*ch:oldFrames*ch])
	copy(r.hist[ch:], in[:ch])
}

// NextFrameIndex returns the integer input frame index for output index i.
func (r *AudioResampler) NextFrameIndex(outIndex int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inRate <= 0 {
		return 0
	}
	return int(int64(outIndex) * int64(r.outRate) / int64(r.inRate))
}

// Phase returns the carried output phase in samples.
//
// The value is the exact integer numerator of the fractional input position
// of the next output frame. Before any output has been produced the carried
// phase is zero.
func (r *AudioResampler) Phase() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outFrameCount == 0 || r.inRate <= 0 || r.outRate <= 0 {
		return 0
	}
	num := int64(r.inRate) * (2*r.outFrameCount + 1)
	den := int64(2 * r.outRate)
	return int(num % den)
}

// ResetBuffer drops buffered samples but keeps the resampling phase.
func (r *AudioResampler) ResetBuffer() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hist = nil
	return nil
}

// FrameCount returns the number of whole interleaved frames in samples.
func (r *AudioResampler) FrameCount(samples []float32) int {
	r.mu.Lock()
	ch := r.channels
	r.mu.Unlock()
	if ch <= 0 {
		return 0
	}
	return len(samples) / ch
}

// PadBoundary zero pads both ends of in for a filter with taps coefficients.
func (r *AudioResampler) PadBoundary(in []float32, taps int) []float32 {
	if taps < 0 {
		taps = 0
	}
	pad := taps / 2
	out := make([]float32, len(in)+2*pad)
	copy(out[pad:], in)
	return out
}

// SmoothRatio ramps prev towards target at rate for a dt step.
func (r *AudioResampler) SmoothRatio(prev, target, dt, rate float64) float64 {
	if dt <= 0 || rate <= 0 {
		return prev
	}
	maxStep := rate * dt
	delta := target - prev
	if delta > maxStep {
		return prev + maxStep
	}
	if delta < -maxStep {
		return prev - maxStep
	}
	return target
}

// WriteOutput appends count silent samples to the shared output buffer.
func (r *AudioResampler) WriteOutput(count int) {
	if count <= 0 {
		return
	}
	r.outputLen.Add(int64(count))
}

// OutputLen returns the number of samples written to the output buffer.
func (r *AudioResampler) OutputLen() int {
	return int(r.outputLen.Load())
}

// TimestampAt returns the presentation timestamp in microseconds of an output frame.
func (r *AudioResampler) TimestampAt(outIndex int) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outRate <= 0 {
		return 0
	}
	return int64(outIndex) * 1_000_000 / int64(r.outRate)
}

// FillUnderrun extends buf to cover missing samples instead of going silent.
func (r *AudioResampler) FillUnderrun(buf []float32, missing int) []float32 {
	if missing <= 0 {
		return buf
	}
	out := make([]float32, len(buf)+missing)
	copy(out, buf)
	return out
}

// PipelineOrder names the processing order of the graph.
func (r *AudioResampler) PipelineOrder() string {
	return "mix_then_resample"
}

// StableRate picks the most frequent rate, breaking ties with the smallest value.
func (r *AudioResampler) StableRate(rates []int) int {
	counts := make(map[int]int, len(rates))
	bestRate := 0
	bestCount := 0
	for _, rate := range rates {
		counts[rate]++
		count := counts[rate]
		if count > bestCount || (count == bestCount && (bestCount == 0 || rate < bestRate)) {
			bestRate = rate
			bestCount = count
		}
	}
	return bestRate
}

// Rebuild re-creates the resampler after a device switch.
func (r *AudioResampler) Rebuild(inRate, outRate, channels int) error {
	if inRate <= 0 || outRate <= 0 || channels <= 0 {
		return errInvalidConfig
	}

	r.mu.Lock()
	r.inRate = inRate
	r.outRate = outRate
	r.channels = channels
	r.inFrameCount = 0
	r.outFrameCount = 0
	r.hist = nil
	r.mu.Unlock()

	r.outputLen.Store(0)
	return nil
}

// AppendClamped appends src to dst unless the result would exceed capacity.
func (r *AudioResampler) AppendClamped(dst, src []float32, capacity int) ([]float32, error) {
	if capacity < 0 || len(dst) > capacity || len(src) > capacity-len(dst) {
		return dst, errCapacityExceeded
	}
	if len(src) == 0 {
		return dst, nil
	}

	total := len(dst) + len(src)
	out := make([]float32, total)
	copy(out, dst)
	copy(out[len(dst):], src)
	return out, nil
}
