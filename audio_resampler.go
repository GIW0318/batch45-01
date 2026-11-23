package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// AudioResampler converts interleaved frames between two sample rates.
type AudioResampler struct{}

// NewAudioResampler creates a resampler for the given rates and channel count.
func NewAudioResampler(inRate, outRate, channels int) (*AudioResampler, error) {
	return &AudioResampler{}, errNotImplemented
}

// Ratio returns outRate / inRate.
func (r *AudioResampler) Ratio() float64 {
	return 0
}

// Resample converts interleaved input frames to the output rate.
func (r *AudioResampler) Resample(in []float32) ([]float32, error) {
	return nil, errNotImplemented
}

// NextFrameIndex returns the integer input frame index for output index i.
func (r *AudioResampler) NextFrameIndex(outIndex int) int {
	return 0
}

// Phase returns the carried output phase in samples.
func (r *AudioResampler) Phase() int {
	return 0
}

// ResetBuffer drops buffered samples but keeps the resampling phase.
func (r *AudioResampler) ResetBuffer() error {
	return errNotImplemented
}

// FrameCount returns the number of whole interleaved frames in samples.
func (r *AudioResampler) FrameCount(samples []float32) int {
	return 0
}

// PadBoundary zero pads both ends of in for a filter with taps coefficients.
func (r *AudioResampler) PadBoundary(in []float32, taps int) []float32 {
	return nil
}

// SmoothRatio ramps prev towards target at rate for a dt step.
func (r *AudioResampler) SmoothRatio(prev, target, dt, rate float64) float64 {
	return 0
}

// WriteOutput appends count silent samples to the shared output buffer.
func (r *AudioResampler) WriteOutput(count int) {
}

// OutputLen returns the number of samples written to the output buffer.
func (r *AudioResampler) OutputLen() int {
	return 0
}

// TimestampAt returns the presentation timestamp in microseconds of an output frame.
func (r *AudioResampler) TimestampAt(outIndex int) int64 {
	return 0
}

// FillUnderrun extends buf to cover missing samples instead of going silent.
func (r *AudioResampler) FillUnderrun(buf []float32, missing int) []float32 {
	return nil
}

// PipelineOrder names the processing order of the graph.
func (r *AudioResampler) PipelineOrder() string {
	return ""
}

// StableRate picks the most frequent rate, breaking ties with the smallest value.
func (r *AudioResampler) StableRate(rates []int) int {
	return 0
}

// Rebuild re-creates the resampler after a device switch.
func (r *AudioResampler) Rebuild(inRate, outRate, channels int) error {
	return errNotImplemented
}

// AppendClamped appends src to dst unless the result would exceed capacity.
func (r *AudioResampler) AppendClamped(dst, src []float32, capacity int) ([]float32, error) {
	return nil, errNotImplemented
}
