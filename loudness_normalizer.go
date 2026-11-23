package task

import (
	"errors"
	"math"
	"strings"
	"sync"
)

const (
	defaultSampleRate = 48000

	momentarySeconds = 0.4
	integrationHop   = 0.1

	absoluteGateLoudness = -70.0
	relativeGateLU       = 10.0

	measurementBlockLimit = 512

	limiterReleaseMS = 250.0

	kWeightCornerHz      = 50.0
	kWeightReferenceHz   = 1000.0
	downmixChannelWeight = 0.7071067811865476
)

var errEmptyMeasurementBlock = errors.New("empty measurement block")

type measurementBlock struct {
	energy  float64
	samples int
}

// LoudnessNormalizer measures and normalises programme loudness.
type LoudnessNormalizer struct {
	target     float64
	sampleRate int

	mu              sync.Mutex
	blocks          []measurementBlock
	measuredSamples int
	lastGain        float64
	hasLastGain     bool
}

// NewLoudnessNormalizer creates a normaliser for a target LUFS at a sample rate.
func NewLoudnessNormalizer(target float64, sampleRate int) *LoudnessNormalizer {
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	return &LoudnessNormalizer{
		target:     target,
		sampleRate: sampleRate,
	}
}

// KWeight returns the K-weighting gain applied at freqHz.
func (n *LoudnessNormalizer) KWeight(freqHz float64) float64 {
	if math.IsNaN(freqHz) || freqHz <= 0 {
		return 0
	}
	if math.IsInf(freqHz, 1) {
		return 1
	}

	highPass := func(freq float64) float64 {
		return freq / math.Hypot(freq, kWeightCornerHz)
	}
	return highPass(freqHz) / highPass(kWeightReferenceHz)
}

// IntegratedLUFS returns the gated integrated loudness of samples.
func (n *LoudnessNormalizer) IntegratedLUFS(samples []float32) float64 {
	if len(samples) == 0 {
		return negativeInfinity()
	}

	blockSamples := int(math.Round(float64(n.sampleRate) * momentarySeconds))
	if blockSamples <= 0 {
		blockSamples = len(samples)
	}
	hopSamples := int(math.Round(float64(n.sampleRate) * integrationHop))
	if hopSamples <= 0 {
		hopSamples = blockSamples
	}

	energies := make([]float64, 0, 1+len(samples)/hopSamples)
	if len(samples) < blockSamples {
		if energy, ok := blockEnergy(samples); ok {
			energies = append(energies, energy)
		}
	} else {
		for start := 0; start+blockSamples <= len(samples); start += hopSamples {
			if energy, ok := blockEnergy(samples[start : start+blockSamples]); ok {
				energies = append(energies, energy)
			}
		}
	}

	return gatedLoudness(energies)
}

// MomentaryLUFS returns the 400 ms momentary loudness of samples.
func (n *LoudnessNormalizer) MomentaryLUFS(samples []float32) float64 {
	if len(samples) == 0 {
		return negativeInfinity()
	}

	windowSamples := int(math.Round(float64(n.sampleRate) * momentarySeconds))
	if windowSamples <= 0 || windowSamples > len(samples) {
		windowSamples = len(samples)
	}
	energy, ok := blockEnergy(samples[len(samples)-windowSamples:])
	if !ok {
		return negativeInfinity()
	}
	return energyToLoudness(energy)
}

// LookaheadDelay delays samples by delay samples so the limiter can see peaks.
func (n *LoudnessNormalizer) LookaheadDelay(samples []float32, delay int) []float32 {
	if delay < 0 {
		delay = 0
	}
	if len(samples) == 0 {
		return make([]float32, delay)
	}
	if delay > int(^uint(0)>>1)-len(samples) {
		delay = int(^uint(0)>>1) - len(samples)
	}

	delayed := make([]float32, len(samples)+delay)
	copy(delayed[delay:], samples)
	return delayed
}

// ChainOrder returns the processing order of the normalisation chain.
func (n *LoudnessNormalizer) ChainOrder() []string {
	return []string{"normalize", "limit"}
}

// Downmix folds a stereo pair into mono using the weighted downmix.
func (n *LoudnessNormalizer) Downmix(left, right []float32) []float32 {
	length := len(left)
	if len(right) < length {
		length = len(right)
	}
	if length == 0 {
		return nil
	}

	downmixed := make([]float32, length)
	for i := 0; i < length; i++ {
		downmixed[i] = float32((float64(left[i]) + float64(right[i])) * downmixChannelWeight)
	}
	return downmixed
}

// GainForSampleRate returns the normalisation gain for a given sample rate.
func (n *LoudnessNormalizer) GainForSampleRate(base float64, sampleRate int) float64 {
	return base
}

// SmoothGain ramps prev towards target over rampSeconds.
func (n *LoudnessNormalizer) SmoothGain(prev, target, dtSeconds, rampSeconds float64) float64 {
	if rampSeconds <= 0 || dtSeconds >= rampSeconds {
		return target
	}
	if dtSeconds <= 0 || prev == target {
		return prev
	}

	next := prev + (target-prev)*(dtSeconds/rampSeconds)
	if (target >= prev && next > target) || (target < prev && next < target) {
		return target
	}
	return next
}

// SharedChain reports whether two contexts share one measurement chain.
func (n *LoudnessNormalizer) SharedChain(a, b string) bool {
	return a == b
}

// LimiterReleaseTime returns the limiter release time in milliseconds.
func (n *LoudnessNormalizer) LimiterReleaseTime() float64 {
	return limiterReleaseMS
}

// LimiterLatency returns the latency the lookahead limiter introduces.
func (n *LoudnessNormalizer) LimiterLatency(delay int) int {
	if delay < 0 {
		return 0
	}
	return delay
}

// AddBlock feeds one measurement block into the integrated loudness meter.
func (n *LoudnessNormalizer) AddBlock(samples []float32) error {
	if len(samples) == 0 {
		return errEmptyMeasurementBlock
	}
	energy, _ := blockEnergy(samples)

	n.mu.Lock()
	defer n.mu.Unlock()

	block := measurementBlock{energy: energy, samples: len(samples)}
	if len(n.blocks) == measurementBlockLimit {
		copy(n.blocks, n.blocks[1:])
		n.blocks[len(n.blocks)-1] = block
	} else {
		n.blocks = append(n.blocks, block)
	}
	n.measuredSamples += len(samples)
	n.updateGainLocked()
	return nil
}

// BlockCount returns how many measurement blocks are buffered.
func (n *LoudnessNormalizer) BlockCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.blocks)
}

// ResetMeasurement clears the measurement buffers.
func (n *LoudnessNormalizer) ResetMeasurement() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.blocks = nil
	n.measuredSamples = 0
	n.lastGain = 0
	n.hasLastGain = false
	return nil
}

// TargetForPlatform returns the loudness target used by a delivery platform.
func (n *LoudnessNormalizer) TargetForPlatform(platform string) float64 {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "ebu", "ebu r128", "ebu-r128", "ebu_r128", "r128", "broadcast", "tv":
		return -23
	case "atsc", "atsc a/85", "atsc-a85", "atsc_a85", "a85", "a/85":
		return -24
	case "netflix":
		return -27
	case "apple music", "apple", "podcast":
		return -16
	case "spotify", "youtube", "streaming":
		return -14
	default:
		return n.target
	}
}

// IsSecondPass reports whether gain already sits at target and must be left alone.
func (n *LoudnessNormalizer) IsSecondPass(gain, target float64) bool {
	if math.IsNaN(gain) || math.IsNaN(target) {
		return false
	}
	return math.Abs(gain-target) <= 1e-6
}

func (n *LoudnessNormalizer) updateGainLocked() {
	if n.measuredSamples < n.sampleRate || math.IsNaN(n.target) || math.IsInf(n.target, 0) {
		return
	}

	energies := make([]float64, 0, len(n.blocks))
	for _, block := range n.blocks {
		if block.samples > 0 && block.energy > 0 {
			energies = append(energies, block.energy)
		}
	}
	loudness := gatedLoudness(energies)
	if math.IsInf(loudness, 0) || math.IsNaN(loudness) {
		return
	}

	n.lastGain = n.target - loudness
	n.hasLastGain = true
}

func blockEnergy(samples []float32) (float64, bool) {
	if len(samples) == 0 {
		return 0, false
	}

	var sumSquares float64
	var valid int
	for _, sample := range samples {
		value := float64(sample)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		sumSquares += value * value
		valid++
	}
	if valid == 0 {
		return 0, false
	}

	energy := sumSquares / float64(valid)
	if energy <= 0 || math.IsNaN(energy) || math.IsInf(energy, 0) {
		return 0, false
	}
	return energy, true
}

func gatedLoudness(energies []float64) float64 {
	if len(energies) == 0 {
		return negativeInfinity()
	}

	absoluteGate := loudnessToEnergy(absoluteGateLoudness)
	aboveAbsolute := make([]float64, 0, len(energies))
	var absoluteSum float64
	for _, energy := range energies {
		if energy > absoluteGate {
			aboveAbsolute = append(aboveAbsolute, energy)
			absoluteSum += energy
		}
	}
	if len(aboveAbsolute) == 0 {
		return negativeInfinity()
	}

	relativeGate := loudnessToEnergy(energyToLoudness(absoluteSum/float64(len(aboveAbsolute))) - relativeGateLU)
	var gatedSum float64
	var gatedCount int
	for _, energy := range aboveAbsolute {
		if energy > relativeGate {
			gatedSum += energy
			gatedCount++
		}
	}
	if gatedCount == 0 {
		return negativeInfinity()
	}
	return energyToLoudness(gatedSum / float64(gatedCount))
}

func energyToLoudness(energy float64) float64 {
	if energy <= 0 || math.IsNaN(energy) || math.IsInf(energy, 0) {
		return negativeInfinity()
	}
	return 10 * math.Log10(energy)
}

func loudnessToEnergy(loudness float64) float64 {
	return math.Pow(10, loudness/10)
}

func negativeInfinity() float64 {
	return math.Inf(-1)
}
