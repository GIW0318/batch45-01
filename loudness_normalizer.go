package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// LoudnessNormalizer measures and normalises programme loudness.
type LoudnessNormalizer struct{}

// NewLoudnessNormalizer creates a normaliser for a target LUFS at a sample rate.
func NewLoudnessNormalizer(target float64, sampleRate int) *LoudnessNormalizer {
	return &LoudnessNormalizer{}
}

// KWeight returns the K-weighting gain applied at freqHz.
func (n *LoudnessNormalizer) KWeight(freqHz float64) float64 {
	return 0
}

// IntegratedLUFS returns the gated integrated loudness of samples.
func (n *LoudnessNormalizer) IntegratedLUFS(samples []float32) float64 {
	return 0
}

// MomentaryLUFS returns the 400 ms momentary loudness of samples.
func (n *LoudnessNormalizer) MomentaryLUFS(samples []float32) float64 {
	return 0
}

// LookaheadDelay delays samples by delay samples so the limiter can see peaks.
func (n *LoudnessNormalizer) LookaheadDelay(samples []float32, delay int) []float32 {
	return nil
}

// ChainOrder returns the processing order of the normalisation chain.
func (n *LoudnessNormalizer) ChainOrder() []string {
	return nil
}

// Downmix folds a stereo pair into mono using the weighted downmix.
func (n *LoudnessNormalizer) Downmix(left, right []float32) []float32 {
	return nil
}

// GainForSampleRate returns the normalisation gain for a given sample rate.
func (n *LoudnessNormalizer) GainForSampleRate(base float64, sampleRate int) float64 {
	return 0
}

// SmoothGain ramps prev towards target over rampSeconds.
func (n *LoudnessNormalizer) SmoothGain(prev, target, dtSeconds, rampSeconds float64) float64 {
	return 0
}

// SharedChain reports whether two contexts share one measurement chain.
func (n *LoudnessNormalizer) SharedChain(a, b string) bool {
	return false
}

// LimiterReleaseTime returns the limiter release time in milliseconds.
func (n *LoudnessNormalizer) LimiterReleaseTime() float64 {
	return 0
}

// LimiterLatency returns the latency the lookahead limiter introduces.
func (n *LoudnessNormalizer) LimiterLatency(delay int) int {
	return 0
}

// AddBlock feeds one measurement block into the integrated loudness meter.
func (n *LoudnessNormalizer) AddBlock(samples []float32) error {
	return errNotImplemented
}

// BlockCount returns how many measurement blocks are buffered.
func (n *LoudnessNormalizer) BlockCount() int {
	return 0
}

// ResetMeasurement clears the measurement buffers.
func (n *LoudnessNormalizer) ResetMeasurement() error {
	return errNotImplemented
}

// TargetForPlatform returns the loudness target used by a delivery platform.
func (n *LoudnessNormalizer) TargetForPlatform(platform string) float64 {
	return 0
}

// IsSecondPass reports whether gain already sits at target and must be left alone.
func (n *LoudnessNormalizer) IsSecondPass(gain, target float64) bool {
	return false
}
