package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Packet is one encoded voice frame received from the network.
type Packet struct {
	Seq       int
	Timestamp int64
	Payload   []byte
}

// JitterGuard buffers, reorders and conceals voice packets.
type JitterGuard struct{}

// NewJitterGuard creates a jitter buffer with the given baseline depth in packets.
func NewJitterGuard(baseDepth int) *JitterGuard {
	return &JitterGuard{}
}

// AdaptiveDepth returns the buffer depth for the observed loss rate.
func (j *JitterGuard) AdaptiveDepth(lossRate float64) int {
	return 0
}

// Depth returns the currently configured buffer depth.
func (j *JitterGuard) Depth() int {
	return 0
}

// Conceal synthesises frameSize samples to hide a lost frame.
func (j *JitterGuard) Conceal(lastFrame []float32, frameSize int) ([]float32, error) {
	return nil, errNotImplemented
}

// ProcessOrder returns the order in which packets are processed.
func (j *JitterGuard) ProcessOrder() []string {
	return nil
}

// Push inserts a packet into the buffer.
func (j *JitterGuard) Push(p Packet) error {
	return errNotImplemented
}

// Drain removes and returns up to n packets in playout order.
func (j *JitterGuard) Drain(n int) []Packet {
	return nil
}

// Pending returns how many packets are buffered.
func (j *JitterGuard) Pending() int {
	return 0
}

// ComfortNoise returns a comfort noise frame of frameSize samples.
func (j *JitterGuard) ComfortNoise(frameSize int, energy float32) []float32 {
	return nil
}

// AlignReference delays the echo cancellation reference signal by delay samples.
func (j *JitterGuard) AlignReference(ref []float32, delay int) []float32 {
	return nil
}

// DriftCorrection returns the clock correction factor for a mixing channel.
func (j *JitterGuard) DriftCorrection(channel int, driftPerChannel float64) float64 {
	return 0
}

// FillUnderrun extends the last frame to need samples instead of going silent.
func (j *JitterGuard) FillUnderrun(last []float32, need int) []float32 {
	return nil
}

// PushOverflow inserts a packet and returns the packet evicted by the full buffer.
func (j *JitterGuard) PushOverflow(p Packet) (*Packet, error) {
	return nil, errNotImplemented
}

// ResetNetwork re-creates the buffer after a network switch.
func (j *JitterGuard) ResetNetwork(base int) error {
	return errNotImplemented
}

// SetDecoderState stores the decoder state that must survive across packets.
func (j *JitterGuard) SetDecoderState(b []byte) error {
	return errNotImplemented
}

// DecoderState returns the retained decoder state.
func (j *JitterGuard) DecoderState() []byte {
	return nil
}
