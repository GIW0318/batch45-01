package task

import (
	"errors"
	"sync"
)

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
	ErrGuardStateLimit      = errors.New("guard per-connection state limit reached")
)

// defaultStateLimit bounds the total number of per-connection entries
// (stream states plus priority nodes) so a single connection cannot grow
// without limit.
const defaultStateLimit = 1 << 16

// GuardConfig is the initial HTTP/2 connection configuration.
type GuardConfig struct {
	MaxConcurrentStreams uint32
	MaxHeaderListSize    int
	InitialStreamWindow  int
	InitialConnWindow    int
	// StateLimit caps the total per-connection state entries (stream states
	// plus priority nodes). Zero keeps the built in default cap.
	StateLimit int
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
	mu sync.Mutex

	maxConcurrent uint32
	maxHeaderList int
	stateLimit    int
	maxFrameSize  int
	initialWindow int

	streams  map[uint32]*streamEntry
	priority map[uint32]*priorityNode
	lastID   uint32
	lastPush uint32

	draining bool
	goAwayID uint32

	migrated   bool
	tombstone  uint32
	generation uint64
}

type streamEntry struct {
	state       StreamState
	window      int
	headerBytes int
	generation  uint64
}

type priorityNode struct {
	parent    uint32
	weight    uint8
	exclusive bool
}

// NewStreamGuard creates a guard for one connection.
func NewStreamGuard(cfg GuardConfig) *StreamGuard {
	limit := cfg.MaxConcurrentStreams
	if int(limit) < 0 {
		limit = 0
	}
	stateLimit := cfg.StateLimit
	if stateLimit <= 0 {
		stateLimit = defaultStateLimit
	}
	return &StreamGuard{
		maxConcurrent: cfg.MaxConcurrentStreams,
		maxHeaderList: cfg.MaxHeaderListSize,
		stateLimit:    stateLimit,
		maxFrameSize:  16384,
		initialWindow: cfg.InitialStreamWindow,
		streams:       make(map[uint32]*streamEntry),
		priority:      make(map[uint32]*priorityNode),
	}
}

// HandleHeaders processes a HEADERS frame and allocates a stream.
func (g *StreamGuard) HandleHeaders(streamID uint32, headerBlockBytes int, endHeaders bool) (StreamState, error) {
	if streamID == 0 {
		return StreamIdle, ErrStreamIDWrapped
	}
	if headerBlockBytes < 0 {
		return StreamIdle, ErrHeaderListTooLarge
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.migrated && streamID <= g.tombstone {
		return StreamIdle, ErrConnMigrated
	}
	// Stream identifiers are strictly increasing on one connection. A lower
	// id means a wrap/reuse attempt and an existing id means reuse.
	if streamID <= g.lastID {
		return StreamIdle, ErrStreamIDWrapped
	}
	if g.draining {
		return StreamIdle, ErrConnDraining
	}
	if g.maxHeaderList > 0 && headerBlockBytes > g.maxHeaderList {
		return StreamIdle, ErrHeaderListTooLarge
	}
	if uint32(len(g.streams)) >= g.maxConcurrent {
		return StreamIdle, ErrMaxConcurrentStreams
	}
	// Hard per-connection memory cap: reject immediately, never queue.
	if len(g.streams)+len(g.priority) >= g.stateLimit {
		return StreamIdle, ErrGuardStateLimit
	}

	g.streams[streamID] = &streamEntry{
		state:       StreamOpen,
		window:      g.initialWindow,
		headerBytes: headerBlockBytes,
		generation:  g.generation,
	}
	g.lastID = streamID
	return StreamOpen, nil
}

// HandleContinuation appends to an in-flight header block.
func (g *StreamGuard) HandleContinuation(streamID uint32, headerBlockBytes int) error {
	if headerBlockBytes < 0 {
		return ErrContinuationOverflow
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.migrated && streamID <= g.tombstone {
		return ErrConnMigrated
	}
	entry, ok := g.streams[streamID]
	if !ok {
		return ErrStreamClosed
	}
	if g.maxHeaderList > 0 && entry.headerBytes+headerBlockBytes > g.maxHeaderList {
		g.removeStreamLocked(streamID)
		return ErrContinuationOverflow
	}
	entry.headerBytes += headerBlockBytes
	return nil
}

// HandleData forwards DATA and charges the per stream window.
func (g *StreamGuard) HandleData(streamID uint32, payload int) (int, error) {
	if payload < 0 {
		return 0, ErrFlowControl
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.migrated && streamID <= g.tombstone {
		return 0, ErrConnMigrated
	}
	entry, ok := g.streams[streamID]
	if !ok {
		return 0, ErrStreamClosed
	}
	if payload > entry.window {
		return 0, ErrFlowControl
	}
	entry.window -= payload
	return payload, nil
}

// HandleRstStream resets a stream and reclaims its concurrency slot.
func (g *StreamGuard) HandleRstStream(streamID uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.migrated && streamID <= g.tombstone {
		return ErrConnMigrated
	}
	if _, ok := g.streams[streamID]; !ok {
		return ErrStreamClosed
	}
	// Immediate reclamation: no timeout, no lazy sweep.
	g.removeStreamLocked(streamID)
	return nil
}

// HandleGoAway puts the connection into draining mode.
func (g *StreamGuard) HandleGoAway(lastStreamID uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.draining = true
	g.goAwayID = lastStreamID
	return nil
}

// HandleSettings applies a SETTINGS frame to the live connection.
func (g *StreamGuard) HandleSettings(s SettingsValues) error {
	const maxWindowIncrement = 1<<31 - 1
	if s.InitialWindow < 0 || s.InitialWindow > maxWindowIncrement {
		return ErrFlowControl
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Applies immediately to the live connection, including existing streams.
	// SettingsValues carries no presence bit, so a zero value means the peer
	// did not include that setting and the current value is retained.
	if s.MaxConcurrentStreams > 0 {
		g.maxConcurrent = s.MaxConcurrentStreams
	}
	if s.MaxFrameSize > 0 {
		g.maxFrameSize = s.MaxFrameSize
	}
	if s.InitialWindow > 0 {
		delta := s.InitialWindow - g.initialWindow
		g.initialWindow = s.InitialWindow
		for _, entry := range g.streams {
			entry.window += delta
		}
	}
	return nil
}

// HandlePriority validates a PRIORITY frame.
func (g *StreamGuard) HandlePriority(f PriorityFrame) error {
	if f.StreamID == 0 {
		return ErrPriorityCycle
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	// Walk the dependency chain before mutating anything so a rejected frame
	// leaves the tree intact.
	seen := make(map[uint32]struct{})
	current := f.DependsOn
	for current != 0 {
		if current == f.StreamID {
			return ErrPriorityCycle
		}
		if _, dup := seen[current]; dup {
			return ErrPriorityCycle
		}
		seen[current] = struct{}{}
		node, ok := g.priority[current]
		if !ok {
			break
		}
		current = node.parent
	}

	if _, exists := g.priority[f.StreamID]; !exists {
		if len(g.streams)+len(g.priority) >= g.stateLimit {
			return ErrGuardStateLimit
		}
		g.priority[f.StreamID] = &priorityNode{}
	}
	node := g.priority[f.StreamID]
	node.parent = f.DependsOn
	node.weight = f.Weight
	node.exclusive = f.Exclusive
	return nil
}

// HandlePushPromise validates a PUSH_PROMISE frame.
func (g *StreamGuard) HandlePushPromise(streamID, promisedID uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Promised streams are server initiated and must use even, non zero ids.
	if promisedID == 0 || promisedID%2 != 0 {
		return ErrPushPromiseInvalid
	}
	if promisedID <= g.lastPush {
		return ErrPushPromiseInvalid
	}
	entry, ok := g.streams[streamID]
	if !ok || entry.generation != g.generation {
		return ErrPushPromiseInvalid
	}
	g.lastPush = promisedID
	return nil
}

// HandleUnknownFrame handles a frame type the gateway does not know.
func (g *StreamGuard) HandleUnknownFrame(f RawFrame) (bool, error) {
	return false, ErrUnknownFrame
}

// HandleWindowUpdate credits a stream flow control window.
func (g *StreamGuard) HandleWindowUpdate(streamID uint32, inc int) error {
	const maxWindowIncrement = 1<<31 - 1
	if inc <= 0 || inc > maxWindowIncrement {
		return ErrFlowControl
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	entry, ok := g.streams[streamID]
	if !ok {
		return ErrStreamClosed
	}
	if entry.window > maxWindowIncrement-inc {
		return ErrFlowControl
	}
	entry.window += inc
	return nil
}

// Migrate moves the connection to a new remote address.
func (g *StreamGuard) Migrate(remote string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Clear all old connection state first, then account the new generation.
	tombstone := g.lastID
	g.streams = make(map[uint32]*streamEntry)
	g.priority = make(map[uint32]*priorityNode)
	g.lastID = 0
	g.lastPush = 0
	g.draining = false
	g.goAwayID = 0
	g.generation++
	g.migrated = true
	g.tombstone = tombstone
	return nil
}

// OpenStreams reports the number of currently open streams.
func (g *StreamGuard) OpenStreams() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.streams)
}

// State reports the state of one stream.
func (g *StreamGuard) State(streamID uint32) (StreamState, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.streams[streamID]
	if !ok {
		return StreamIdle, false
	}
	return entry.state, true
}

func (g *StreamGuard) removeStreamLocked(streamID uint32) {
	delete(g.streams, streamID)
}
