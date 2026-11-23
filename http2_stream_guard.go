package task

import "errors"

// Sentinel errors of the stream guard.
var (
	ErrMaxConcurrentStreams = errors.New("max concurrent streams exceeded")
	ErrStreamIDWrapped      = errors.New("stream id reused or wrapped")
	ErrConnDraining         = errors.New("connection is draining after goaway")
	ErrHeaderListTooLarge   = errors.New("header list size exceeded")
	ErrContinuationOverflow = errors.New("continuation header block too large")
	ErrPriorityCycle        = errors.New("priority dependency cycle")
	ErrUnknownFrame         = errors.New("unknown frame type")
	ErrPushPromiseInvalid   = errors.New("push promise references an invalid stream")
	ErrStreamClosed         = errors.New("stream is closed")
	ErrFlowControl          = errors.New("stream flow control window exceeded")
	ErrConnMigrated         = errors.New("connection migrated, stream no longer writable")
)

// GuardConfig is the initial HTTP/2 connection configuration.
type GuardConfig struct {
	MaxConcurrentStreams uint32
	MaxHeaderListSize    int
	InitialStreamWindow  int
	InitialConnWindow    int
}

// SettingsValues is a received SETTINGS frame.
type SettingsValues struct {
	MaxConcurrentStreams uint32
	MaxFrameSize         int
	InitialWindow        int
}

// StreamState is the lifecycle state of one stream.
type StreamState int

const (
	StreamIdle StreamState = iota
	StreamOpen
	StreamHalfClosedLocal
	StreamClosed
)

// PriorityFrame is a PRIORITY frame.
type PriorityFrame struct {
	StreamID  uint32
	DependsOn uint32
	Weight    uint8
	Exclusive bool
}

// RawFrame is an unhandled frame.
type RawFrame struct {
	Type     uint8
	StreamID uint32
	Length   int
}

// StreamGuard enforces HTTP/2 stream limits on one connection.
type StreamGuard struct {
}

// NewStreamGuard creates a guard for one connection.
func NewStreamGuard(cfg GuardConfig) *StreamGuard {
	return &StreamGuard{}
}

// HandleHeaders processes a HEADERS frame and allocates a stream.
func (g *StreamGuard) HandleHeaders(streamID uint32, headerBlockBytes int, endHeaders bool) (StreamState, error) {
	return StreamIdle, errors.New("not implemented")
}

// HandleContinuation appends to an in-flight header block.
func (g *StreamGuard) HandleContinuation(streamID uint32, headerBlockBytes int) error {
	return errors.New("not implemented")
}

// HandleData forwards DATA and charges the per stream window.
func (g *StreamGuard) HandleData(streamID uint32, payload int) (int, error) {
	return 0, errors.New("not implemented")
}

// HandleRstStream resets a stream and reclaims its concurrency slot.
func (g *StreamGuard) HandleRstStream(streamID uint32) error {
	return errors.New("not implemented")
}

// HandleGoAway puts the connection into draining mode.
func (g *StreamGuard) HandleGoAway(lastStreamID uint32) error {
	return errors.New("not implemented")
}

// HandleSettings applies a SETTINGS frame to the live connection.
func (g *StreamGuard) HandleSettings(s SettingsValues) error {
	return errors.New("not implemented")
}

// HandlePriority validates a PRIORITY frame.
func (g *StreamGuard) HandlePriority(f PriorityFrame) error {
	return errors.New("not implemented")
}

// HandlePushPromise validates a PUSH_PROMISE frame.
func (g *StreamGuard) HandlePushPromise(streamID, promisedID uint32) error {
	return errors.New("not implemented")
}

// HandleUnknownFrame handles a frame type the gateway does not know.
func (g *StreamGuard) HandleUnknownFrame(f RawFrame) (bool, error) {
	return false, errors.New("not implemented")
}

// HandleWindowUpdate credits a stream flow control window.
func (g *StreamGuard) HandleWindowUpdate(streamID uint32, inc int) error {
	return errors.New("not implemented")
}

// Migrate moves the connection to a new remote address.
func (g *StreamGuard) Migrate(remote string) error {
	return errors.New("not implemented")
}

// OpenStreams reports the number of currently open streams.
func (g *StreamGuard) OpenStreams() int {
	return 0
}

// State reports the state of one stream.
func (g *StreamGuard) State(streamID uint32) (StreamState, bool) {
	return StreamIdle, false
}
