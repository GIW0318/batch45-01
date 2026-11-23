package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// MixerGuard mixes voice and sound effect channels under a fixed voice budget.
type MixerGuard struct{}

// NewMixerGuard creates a mixer with at most maxVoices concurrent voices.
func NewMixerGuard(maxVoices int) *MixerGuard {
	return &MixerGuard{}
}

// Limit applies the pre-mix limiter to one sample.
func (m *MixerGuard) Limit(x float32) float32 {
	return 0
}

// SumVoices sums the given channels and clamps the result to [-1, 1].
func (m *MixerGuard) SumVoices(chans [][]float32) []float32 {
	return nil
}

// AllocateVoice reserves a voice slot of the given kind and priority.
func (m *MixerGuard) AllocateVoice(kind string, priority int) (int, error) {
	return 0, errNotImplemented
}

// SetVoiceAmplitude records the current amplitude of a voice.
func (m *MixerGuard) SetVoiceAmplitude(id int, a float32) error {
	return errNotImplemented
}

// StealVoice returns the id of the voice that should be stolen.
func (m *MixerGuard) StealVoice() (int, bool) {
	return 0, false
}

// PhaseOffset returns the start phase used to decorrelate simultaneous voices.
func (m *MixerGuard) PhaseOffset(index int) float64 {
	return 0
}

// ClearBuffer zeroes every sample of buf and returns it.
func (m *MixerGuard) ClearBuffer(buf []float32) []float32 {
	return nil
}

// ResampleChannelTo converts a mono channel between two sample rates.
func (m *MixerGuard) ResampleChannelTo(samples []float32, inRate, outRate int) []float32 {
	return nil
}

// Attenuation returns the distance attenuation for dist relative to ref.
func (m *MixerGuard) Attenuation(dist, ref, rolloff float64) float64 {
	return 0
}

// AddSample registers a loaded sample under id.
func (m *MixerGuard) AddSample(id string, s []float32) {
}

// SampleCount returns how many samples are registered.
func (m *MixerGuard) SampleCount() int {
	return 0
}

// DuckPriority returns the effective priority of a sound effect.
func (m *MixerGuard) DuckPriority(base int, voiceActive bool) int {
	return 0
}

// RenderSamples returns a preallocated render buffer of n samples.
func (m *MixerGuard) RenderSamples(n int) []float32 {
	return nil
}

// MixBudget reports how many frames fit into the budget, and errors when dropping.
func (m *MixerGuard) MixBudget(frames, budget int) (int, error) {
	return 0, errNotImplemented
}

// Advance moves the playback position forward by n frames.
func (m *MixerGuard) Advance(n int) {
}

// Pause stops playback.
func (m *MixerGuard) Pause() error {
	return errNotImplemented
}

// Resume continues playback from the paused position.
func (m *MixerGuard) Resume() error {
	return errNotImplemented
}

// Position returns the current playback position in frames.
func (m *MixerGuard) Position() int {
	return 0
}
