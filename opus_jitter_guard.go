package task

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

// ErrDuplicateSeq is returned by Push when a packet with the same
// sequence number has already been accepted into the buffer.
var ErrDuplicateSeq = errors.New("duplicate sequence number")

// ErrInvalidFrameSize is returned when a non-positive frame size is requested.
var ErrInvalidFrameSize = errors.New("invalid frame size")

const (
	// sampleRate is the Opus full-band rate used to convert frame sizes
	// into wall-clock durations for loss tracking.
	sampleRate = 48000
	// keyframeLossThreshold is how long consecutive loss may persist
	// before a keyframe is actively requested.
	keyframeLossThreshold = 2 * time.Second
)

// Packet is one encoded voice frame received from the network.
type Packet struct {
	Seq       int
	Timestamp int64
	Payload   []byte
}

// JitterGuard buffers, reorders and conceals voice packets. It is safe
// for concurrent use: Push and Drain may run on different goroutines
// without duplicating or losing packets.
type JitterGuard struct {
	mu sync.Mutex

	buf  []Packet // kept sorted by Timestamp (playout order)
	seqs map[int]struct{}

	base  int
	depth int

	consecutiveLoss int
	lossAccum       time.Duration
	awaitingKey     bool

	decoderState []byte

	// OnKeyframeRequest, if set, is invoked synchronously (after the
	// internal lock is released, so it may call back into the guard)
	// once each time consecutive loss exceeds keyframeLossThreshold.
	OnKeyframeRequest func()
}

// NewJitterGuard creates a jitter buffer with the given baseline depth in packets.
func NewJitterGuard(baseDepth int) *JitterGuard {
	if baseDepth < 1 {
		baseDepth = 1
	}
	return &JitterGuard{
		seqs:  make(map[int]struct{}),
		base:  baseDepth,
		depth: baseDepth,
	}
}

// AdaptiveDepth returns the buffer depth for the observed loss rate.
// The depth grows with loss (base * (1 + 2*lossRate)) and never drops
// below the configured baseline; the result becomes the active depth.
func (j *JitterGuard) AdaptiveDepth(lossRate float64) int {
	if lossRate < 0 {
		lossRate = 0
	}
	if lossRate > 1 {
		lossRate = 1
	}
	depth := int(math.Round(float64(j.base) * (1 + 2*lossRate)))
	if depth < j.base {
		depth = j.base
	}
	j.mu.Lock()
	j.depth = depth
	j.mu.Unlock()
	return depth
}

// Depth returns the currently configured buffer depth.
func (j *JitterGuard) Depth() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.depth
}

// Conceal synthesises frameSize samples to hide a lost frame.
// The first lost frame repeats the last good frame; each further
// consecutive loss halves the amplitude again (gains 1.0, 0.5, 0.25,
// ...) so sustained loss decays smoothly to silence
// instead of looping a robotic tone.
func (j *JitterGuard) Conceal(lastFrame []float32, frameSize int) ([]float32, error) {
	if frameSize <= 0 {
		return nil, ErrInvalidFrameSize
	}

	j.mu.Lock()
	j.consecutiveLoss++
	n := j.consecutiveLoss
	j.lossAccum += time.Duration(frameSize) * time.Second / sampleRate
	requestKey := j.lossAccum > keyframeLossThreshold && !j.awaitingKey
	if requestKey {
		j.awaitingKey = true
	}
	cb := j.OnKeyframeRequest
	j.mu.Unlock()

	if requestKey && cb != nil {
		cb()
	}

	gain := float32(math.Pow(0.5, float64(n-1)))
	out := make([]float32, frameSize)
	if len(lastFrame) == 0 {
		return out, nil
	}
	for i := range out {
		out[i] = lastFrame[i%len(lastFrame)] * gain
	}
	return out, nil
}

// ProcessOrder returns the order in which packets are processed.
// Opus decoding must run before packet-loss concealment so the PLC
// can use the decoder's updated state.
func (j *JitterGuard) ProcessOrder() []string {
	return []string{"decode", "plc"}
}

// Push inserts a packet into the buffer in timestamp order and rejects
// packets whose sequence number was already accepted.
func (j *JitterGuard) Push(p Packet) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, dup := j.seqs[p.Seq]; dup {
		return ErrDuplicateSeq
	}
	j.insertLocked(p)
	j.seqs[p.Seq] = struct{}{}
	// A real packet ended the concealment streak; the keyframe recovery
	// latch (awaitingKey) is only cleared by PushKeyframe.
	j.consecutiveLoss = 0
	return nil
}

// insertLocked inserts p keeping buf sorted by Timestamp. Ties keep
// arrival order (stable by Seq as a deterministic tie-break).
func (j *JitterGuard) insertLocked(p Packet) {
	idx := sort.Search(len(j.buf), func(i int) bool {
		if j.buf[i].Timestamp == p.Timestamp {
			return j.buf[i].Seq > p.Seq
		}
		return j.buf[i].Timestamp > p.Timestamp
	})
	j.buf = append(j.buf, Packet{})
	copy(j.buf[idx+1:], j.buf[idx:])
	j.buf[idx] = p
}

// Drain removes and returns up to n packets in playout order.
// While keyframe recovery is pending (loss exceeded two seconds and no
// keyframe has arrived yet) Drain returns nothing, so packets that
// arrived after the outage are held back instead of being sent to the
// decoder ahead of the keyframe.
func (j *JitterGuard) Drain(n int) []Packet {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.awaitingKey || n <= 0 || len(j.buf) == 0 {
		return nil
	}
	if n > len(j.buf) {
		n = len(j.buf)
	}
	out := make([]Packet, n)
	copy(out, j.buf[:n])
	for _, p := range out {
		delete(j.seqs, p.Seq)
	}
	j.buf = append([]Packet(nil), j.buf[n:]...)
	return out
}

// Pending returns how many packets are buffered.
func (j *JitterGuard) Pending() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.buf)
}

// ComfortNoise returns a comfort noise frame of frameSize samples.
// During DTX the receiver keeps playing low-level noise shaped to the
// background energy instead of going dead silent.
func (j *JitterGuard) ComfortNoise(frameSize int, energy float32) []float32 {
	if frameSize <= 0 {
		return nil
	}
	if energy < 0 {
		energy = -energy
	}
	out := make([]float32, frameSize)
	// Deterministic LCG so comfort noise is reproducible and cheap.
	state := uint32(0x9E3779B9) ^ uint32(frameSize)
	amp := energy * 0.5
	for i := range out {
		state = state*1664525 + 1013904223
		// Map to [-1, 1).
		u := float32(state>>8)/float32(1<<23) - 1
		out[i] = u * amp
	}
	return out
}

// AlignReference delays the echo cancellation reference signal by delay samples.
// Zero samples are prepended so the reference lines up with the far-end
// signal that actually reached the microphone.
func (j *JitterGuard) AlignReference(ref []float32, delay int) []float32 {
	if delay < 0 {
		delay = 0
	}
	out := make([]float32, delay+len(ref))
	copy(out[delay:], ref)
	return out
}

// DriftCorrection returns the clock correction factor for a mixing channel.
// Each channel accumulates drift proportionally to its index, so the
// resample factor is 1 - channel*driftPerChannel.
func (j *JitterGuard) DriftCorrection(channel int, driftPerChannel float64) float64 {
	return 1 - float64(channel)*driftPerChannel
}

// FillUnderrun extends the last frame to need samples instead of going silent.
// The last frame is tiled so a buffer underrun keeps playing audio.
func (j *JitterGuard) FillUnderrun(last []float32, need int) []float32 {
	if need <= 0 {
		return nil
	}
	out := make([]float32, need)
	if len(last) == 0 {
		return out
	}
	for i := range out {
		out[i] = last[i%len(last)]
	}
	return out
}

// PushOverflow inserts a packet and returns the packet evicted by the full buffer.
// The buffer is capped at the current depth; when full, the oldest packet
// (earliest timestamp) is evicted and handed back to the caller — the
// newest packet is never dropped.
func (j *JitterGuard) PushOverflow(p Packet) (*Packet, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	cap := j.depth
	if cap < 1 {
		cap = j.base
	}
	var evicted *Packet
	if len(j.buf) >= cap {
		old := j.buf[0]
		evicted = &old
		delete(j.seqs, old.Seq)
		j.buf = j.buf[1:]
	}
	j.insertLocked(p)
	j.seqs[p.Seq] = struct{}{}
	return evicted, nil
}

// PushKeyframe delivers the keyframe that was requested after a prolonged
// outage. It clears the recovery latch so buffered packets that arrived
// during the outage are released only after the keyframe, and resets the
// loss accounting.
func (j *JitterGuard) PushKeyframe(p Packet) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	// The keyframe must be played before anything buffered during the
	// outage, so it goes to the head of the queue.
	j.buf = append([]Packet{p}, j.buf...)
	j.seqs[p.Seq] = struct{}{}
	j.awaitingKey = false
	j.lossAccum = 0
	j.consecutiveLoss = 0
	return nil
}

// NeedsKeyframe reports whether consecutive loss has exceeded two seconds
// and a keyframe is being requested before normal playout resumes.
func (j *JitterGuard) NeedsKeyframe() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.awaitingKey
}

// ResetNetwork re-creates the buffer after a network switch.
// Buffered packets, sequence bookkeeping and loss state are dropped so a
// stale queue from the old path cannot leak into the new one; the decoder
// state is retained so the ongoing session is not disturbed.
func (j *JitterGuard) ResetNetwork(base int) error {
	if base < 1 {
		base = 1
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = nil
	j.seqs = make(map[int]struct{})
	j.base = base
	j.depth = base
	j.consecutiveLoss = 0
	j.lossAccum = 0
	j.awaitingKey = false
	return nil
}

// SetDecoderState stores the decoder state that must survive across packets.
// The bytes are deep-copied so later caller mutations cannot corrupt it.
func (j *JitterGuard) SetDecoderState(b []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.decoderState = append([]byte(nil), b...)
	return nil
}

// DecoderState returns the retained decoder state.
// A copy is returned so callers cannot mutate the stored state.
func (j *JitterGuard) DecoderState() []byte {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]byte(nil), j.decoderState...)
}
