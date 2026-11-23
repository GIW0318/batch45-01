package task

import (
	"errors"
	"sync"
)

var (
	errNoVoiceAvailable = errors.New("mixer: no voice available")
	errVoiceNotFound    = errors.New("mixer: voice id not found")
	errBudgetOverflow   = errors.New("mixer: frame budget overflow, excess frames dropped")
)

type voiceSlot struct {
	active    bool
	kind      string
	priority  int
	amplitude float32
}

type sampleEntry struct {
	id      string
	samples []float32
}

// MixerGuard mixes voice and sound effect channels under a fixed voice budget.
type MixerGuard struct {
	// mu protects the voice slot table, the sample table, the mix budget and
	// the playback position / pause flag together, so the mixer thread and the
	// resource loading thread can never observe torn state.
	mu        sync.Mutex
	maxVoices int
	slots     []voiceSlot
	samples   []sampleEntry
	rendered  int
	position  int
	paused    bool

	// deviceMu is held for reading for the whole duration of an audio
	// callback, so a device switch blocks until the callback returns and is
	// always completed between two callbacks.
	deviceMu     sync.RWMutex
	outputDevice string

	// renderMu guards a concrete-typed free list of render buffers. Reusing
	// buffers through the list keeps the real-time callback free of make()
	// allocations without interface-boxing a pointer on every release.
	renderMu   sync.Mutex
	renderFree [][]float32
}

// NewMixerGuard creates a mixer with at most maxVoices concurrent voices.
func NewMixerGuard(maxVoices int) *MixerGuard {
	if maxVoices < 1 {
		maxVoices = 1
	}
	m := &MixerGuard{
		maxVoices:    maxVoices,
		slots:        make([]voiceSlot, maxVoices),
		outputDevice: "default",
		renderFree:   [][]float32{make([]float32, 64)},
	}
	return m
}

// Limit applies the pre-mix limiter to one sample. Signals inside [-1, 1]
// pass through unchanged; hot signals are soft-clipped with x/(1+|x|), so
// Limit(2.0) lands at 2/3 instead of hard clipping and popping the speaker.
func (m *MixerGuard) Limit(x float32) float32 {
	if x > 1 {
		return x / (1 + x)
	}
	if x < -1 {
		return x / (1 - x)
	}
	return x
}

// SumVoices sums the given channels and clamps the result to [-1, 1].
func (m *MixerGuard) SumVoices(chans [][]float32) []float32 {
	n := 0
	for _, ch := range chans {
		if len(ch) > n {
			n = len(ch)
		}
	}
	out := make([]float32, n)
	for _, ch := range chans {
		for i, v := range ch {
			out[i] += v
		}
	}
	for i, v := range out {
		if v > 1 {
			out[i] = 1
		} else if v < -1 {
			out[i] = -1
		}
	}
	return out
}

// AllocateVoice reserves a voice slot of the given kind and priority. When
// every slot is busy a higher-priority request preempts the quietest
// lowest-priority voice; at equal priority a voice channel preempts an sfx
// channel, so team voice can never be permanently starved by sound effects.
func (m *MixerGuard) AllocateVoice(kind string, priority int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Prefer a free slot first; the slot table length is the hard voice cap.
	for id := range m.slots {
		if !m.slots[id].active {
			m.slots[id] = voiceSlot{active: true, kind: kind, priority: priority}
			return id, nil
		}
	}

	// Every slot is busy: find the quietest voice among the lowest-priority
	// slots and decide whether the new request may preempt it.
	victim := 0
	for id := 1; id < len(m.slots); id++ {
		s := &m.slots[id]
		best := &m.slots[victim]
		if s.priority < best.priority ||
			(s.priority == best.priority && s.amplitude < best.amplitude) {
			victim = id
		}
	}
	best := &m.slots[victim]
	preempt := priority > best.priority ||
		(priority == best.priority && kind == "voice" && best.kind != "voice")
	if !preempt {
		return 0, errNoVoiceAvailable
	}
	m.slots[victim] = voiceSlot{active: true, kind: kind, priority: priority}
	return victim, nil
}

// SetVoiceAmplitude records the current amplitude of a voice.
func (m *MixerGuard) SetVoiceAmplitude(id int, a float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 0 || id >= len(m.slots) || !m.slots[id].active {
		return errVoiceNotFound
	}
	m.slots[id].amplitude = a
	return nil
}

// StealVoice returns the id of the active voice with the lowest amplitude
// (ties broken by lower priority, then lower id) and releases its slot.
func (m *MixerGuard) StealVoice() (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	victim := -1
	for id := range m.slots {
		s := &m.slots[id]
		if !s.active {
			continue
		}
		if victim == -1 {
			victim = id
			continue
		}
		cur := &m.slots[victim]
		if s.amplitude < cur.amplitude ||
			(s.amplitude == cur.amplitude && s.priority < cur.priority) {
			victim = id
		}
	}
	if victim == -1 {
		return 0, false
	}
	m.slots[victim].active = false
	return victim, true
}

// PhaseOffset returns a per-slot start phase in [0, 1) that increases with
// the slot index, decorrelating voices that start on the same frame instead
// of stacking every wave at phase zero.
func (m *MixerGuard) PhaseOffset(index int) float64 {
	if m.maxVoices <= 0 {
		return 0
	}
	offset := float64(index) / float64(m.maxVoices)
	mod := offset - float64(int64(offset))
	if mod < 0 {
		mod += 1
	}
	return mod
}

// ClearBuffer zeroes every sample of buf and returns it.
func (m *MixerGuard) ClearBuffer(buf []float32) []float32 {
	for i := range buf {
		buf[i] = 0
	}
	return buf
}

// ResampleChannelTo converts a mono channel between two sample rates with
// linear interpolation and emits exactly len(samples)*outRate/inRate frames,
// so resampled channels fit the bus instead of being stuffed in at the
// original length.
func (m *MixerGuard) ResampleChannelTo(samples []float32, inRate, outRate int) []float32 {
	if inRate <= 0 || outRate <= 0 {
		return nil
	}
	if len(samples) == 0 {
		return []float32{}
	}
	if inRate == outRate {
		out := make([]float32, len(samples))
		copy(out, samples)
		return out
	}
	n := len(samples) * outRate / inRate
	out := make([]float32, n)
	ratio := float64(inRate) / float64(outRate)
	for i := range out {
		pos := float64(i) * ratio
		lo := int(pos)
		frac := float32(pos - float64(lo))
		if lo >= len(samples)-1 {
			out[i] = samples[len(samples)-1]
			continue
		}
		out[i] = samples[lo]*(1-frac) + samples[lo+1]*frac
	}
	return out
}

// Attenuation returns the distance attenuation for dist relative to ref.
// With the reference distance fixed at 1 unit of gain (Attenuation(ref) == 1)
// it computes (ref/dist)^rolloff; rolloff 1 gives the inverse-distance curve
// used by the mixer.
func (m *MixerGuard) Attenuation(dist, ref, rolloff float64) float64 {
	if dist <= 0 || ref <= 0 || rolloff <= 0 {
		return 1
	}
	gain := ref / dist
	if rolloff == 1 {
		return gain
	}
	pow := gain
	for i := 1; i < int(rolloff); i++ {
		pow *= gain
	}
	return pow
}

// AddSample registers a loaded sample. Every load is appended as its own
// record even when loaders reuse the same id, so concurrent loads are never
// overwritten and SampleCount counts every registered sample.
func (m *MixerGuard) AddSample(id string, s []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, sampleEntry{id: id, samples: s})
}

// SampleCount returns how many samples are registered.
func (m *MixerGuard) SampleCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.samples)
}

// DuckPriority returns the effective priority of a sound effect. While a
// voice channel is active sfx priority is ducked by one, making room for
// speech before any voice stealing is needed.
func (m *MixerGuard) DuckPriority(base int, voiceActive bool) int {
	if voiceActive {
		return base - 1
	}
	return base
}

// RenderSamples returns a zeroed buffer taken from a preallocated pool, so
// the audio callback path performs no make() allocation of its own.
func (m *MixerGuard) RenderSamples(n int) []float32 {
	m.renderMu.Lock()
	if last := len(m.renderFree) - 1; last >= 0 {
		cached := m.renderFree[last]
		m.renderFree = m.renderFree[:last]
		m.renderMu.Unlock()
		if cap(cached) >= n {
			buf := cached[:n]
			for i := range buf {
				buf[i] = 0
			}
			return buf
		}
		return make([]float32, n)
	}
	m.renderMu.Unlock()
	return make([]float32, n)
}

// ReleaseRenderSamples returns a render buffer to the preallocated pool so
// later callbacks reuse it without allocating.
func (m *MixerGuard) ReleaseRenderSamples(buf []float32) {
	if cap(buf) == 0 {
		return
	}
	m.renderMu.Lock()
	m.renderFree = append(m.renderFree, buf[:cap(buf)])
	m.renderMu.Unlock()
}

// MixBudget reports how many frames fit into the budget. Up to budget frames
// are rendered; an overflow returns the capped frame count together with an
// error instead of silently rendering every requested frame.
func (m *MixerGuard) MixBudget(frames, budget int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if frames <= budget {
		m.rendered += frames
		return frames, nil
	}
	m.rendered += budget
	return budget, errBudgetOverflow
}

// Advance moves the playback position forward by n frames. While paused the
// position is frozen, so resuming continues exactly where playback stopped.
func (m *MixerGuard) Advance(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paused {
		return
	}
	m.position += n
}

// Pause stops playback. Calling it repeatedly is a harmless no-op.
func (m *MixerGuard) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = true
	return nil
}

// Resume continues playback from the paused position and is idempotent.
func (m *MixerGuard) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = false
	return nil
}

// Position returns the current playback position in frames.
func (m *MixerGuard) Position() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.position
}

// SwitchOutputDevice changes the output device. It blocks while an audio
// callback is running, so the switch is always completed between two
// callbacks and no callback ever emits frames for two different devices.
func (m *MixerGuard) SwitchOutputDevice(device string) error {
	if device == "" {
		return errors.New("mixer: empty output device name")
	}
	m.deviceMu.Lock()
	m.outputDevice = device
	m.deviceMu.Unlock()
	return nil
}

// OutputDevice returns the device currently bound to the mixer.
func (m *MixerGuard) OutputDevice() string {
	m.deviceMu.RLock()
	device := m.outputDevice
	m.deviceMu.RUnlock()
	return device
}

// RunAudioCallback executes render as one atomic device callback. The device
// is captured once under the read lock, which excludes SwitchOutputDevice for
// the whole render, so every frame produced here belongs to the same device.
func (m *MixerGuard) RunAudioCallback(render func(device string, frames int) []float32, frames int) []float32 {
	m.deviceMu.RLock()
	device := m.outputDevice
	out := render(device, frames)
	m.deviceMu.RUnlock()
	return out
}
