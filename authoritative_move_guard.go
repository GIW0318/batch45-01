package task

import (
	"math"
	"sort"
	"sync"
)

// replayWindowMs bounds how far back unacknowledged inputs are replayed.
// Inputs older than the window are no longer replayed one by one: the
// authoritative server position is trusted instead, so a single stall cannot
// produce a long tail of catch-up movement.
const replayWindowMs = 200

// Vec is a 3d position in world units.
type Vec struct {
	X float64
	Y float64
	Z float64
}

// Input is a client movement input carrying its own sequence number.
type Input struct {
	Seq int
	DX  float64
	DY  float64
}

// HistoryEntry is a timestamped movement input kept for rollback, replay and
// restart recovery.
type HistoryEntry struct {
	Seq    int
	DX     float64
	DY     float64
	TimeMs int
}

// Snapshot is the durable confirmed state. Together with the retained input
// log it lets a restarted process reproduce the exact same state sequence.
type Snapshot struct {
	Seq     int
	Pos     Vec
	TimeMs  int
	Applied []int
	History []HistoryEntry
}

// MoveGuard arbitrates client prediction and server correction.
type MoveGuard struct {
	mu        sync.Mutex
	threshold float64

	// applied is the dedup set of sequence numbers that already took effect.
	applied map[int]struct{}
	// count is the cumulative number of inputs that were really executed.
	count int
	// ackedSeq is the highest confirmed sequence; any seq at or below it is
	// considered already executed, so late duplicate packets stay idempotent
	// without keeping every historical seq in memory.
	ackedSeq int

	// history is the timestamped input log, always trimmed to replayWindowMs.
	history []HistoryEntry

	// confirmed is the latest server-acknowledged state.
	confirmed Snapshot
}

// NewMoveGuard builds a guard with the given correction distance threshold.
func NewMoveGuard(threshold float64) *MoveGuard {
	return &MoveGuard{
		threshold: threshold,
		applied:   make(map[int]struct{}),
	}
}

// EncodeInput stamps a client input with its sequence number.
func (m *MoveGuard) EncodeInput(seq int, dx, dy float64) Input {
	return Input{Seq: seq, DX: dx, DY: dy}
}

// Apply executes an input once and reports how many inputs were applied.
// It is safe for concurrent network goroutines: a given sequence number takes
// effect exactly once even if its packet arrives several times.
func (m *MoveGuard) Apply(seq int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if seq <= m.ackedSeq {
		return m.count, nil
	}
	if _, ok := m.applied[seq]; ok {
		return m.count, nil
	}
	m.applied[seq] = struct{}{}
	m.count++
	return m.count, nil
}

// Blend interpolates between the predicted and the authoritative position.
// All three components are interpolated linearly by alpha.
func (m *MoveGuard) Blend(from, to Vec, alpha float64) Vec {
	return Vec{
		X: from.X + (to.X-from.X)*alpha,
		Y: from.Y + (to.Y-from.Y)*alpha,
		Z: from.Z + (to.Z-from.Z)*alpha,
	}
}

// Step is the distance travelled during dtMs at the given speed.
// Displacement is speed * milliseconds / 1000, so it is frame-rate independent.
func (m *MoveGuard) Step(speed float64, dtMs int) Vec {
	d := speed * float64(dtMs) / 1000
	return Vec{X: d}
}

// Integrate accumulates movement over several fixed steps.
func (m *MoveGuard) Integrate(speed float64, steps int, dtMs int) Vec {
	d := speed * float64(steps) * float64(dtMs) / 1000
	return Vec{X: d}
}

// ShouldCorrect reports whether the deviation warrants a server correction.
func (m *MoveGuard) ShouldCorrect(predicted, server Vec) bool {
	return distance(predicted, server) > m.threshold
}

// WithinTolerance reports whether a deviation is acceptable jitter.
func (m *MoveGuard) WithinTolerance(predicted, server Vec) bool {
	return distance(predicted, server) <= m.threshold
}

// LagCompensate rewinds the server to the time the input was produced.
// The rewind target is never earlier than nowMs-maxMs and never in the future.
func (m *MoveGuard) LagCompensate(nowMs, inputMs, maxMs int) int {
	earliest := nowMs - maxMs
	if inputMs < earliest {
		return earliest
	}
	if inputMs > nowMs {
		return nowMs
	}
	return inputMs
}

// ClampDrift pulls a prediction back to the server position when it drifted too far.
// Within the tolerance the prediction is kept; beyond it the whole server
// position is adopted instead of blending the stale prediction.
func (m *MoveGuard) ClampDrift(predicted, server Vec) Vec {
	if m.ShouldCorrect(predicted, server) {
		return server
	}
	return predicted
}

// OnVehicle offsets a player local position by the vehicle position.
func (m *MoveGuard) OnVehicle(local, vehicle Vec) Vec {
	return Vec{
		X: local.X + vehicle.X,
		Y: local.Y + vehicle.Y,
		Z: local.Z + vehicle.Z,
	}
}

// Vertical integrates vertical movement separately from horizontal movement.
// Only Z changes; horizontal components stay untouched.
func (m *MoveGuard) Vertical(base, vy float64, dtMs int) Vec {
	return Vec{Z: base + vy*float64(dtMs)/1000}
}

// AnimationAfterRollback is the animation state a rolled back client must reset to.
// With no rolled-back frames it returns the empty string.
func (m *MoveGuard) AnimationAfterRollback(frames int) string {
	if frames > 0 {
		return "idle_restart"
	}
	return ""
}

// TickDelta is the frame gap between the server tick and the client tick.
func (m *MoveGuard) TickDelta(serverTick, clientTick int) int {
	return serverTick - clientTick
}

// Blocked is the client side wall test used before sending a move.
func (m *MoveGuard) Blocked(pos, wall Vec) bool {
	return distance(pos, wall) <= m.threshold
}

// Resimulate replays unacknowledged inputs on top of the last acknowledged frame.
// Only inputs whose sequence number is strictly after lastAcked are counted.
func (m *MoveGuard) Resimulate(seqs []int, lastAcked int) int {
	count := 0
	for _, seq := range seqs {
		if seq > lastAcked {
			count++
		}
	}
	return count
}

// LogInput records a timestamped input for rollback and restart recovery.
// Duplicate sequence numbers are ignored, and a repeated horizontal input in
// the same frame only produces a single displacement. The retained log is
// trimmed to the 200ms replay window, so memory does not grow with uptime.
func (m *MoveGuard) LogInput(in Input, timeMs int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.Seq <= m.ackedSeq {
		return false
	}
	if _, ok := m.applied[in.Seq]; ok {
		return false
	}
	if n := len(m.history); n > 0 {
		last := m.history[n-1]
		if last.TimeMs == timeMs && last.DX == in.DX && last.DY == in.DY {
			return false
		}
	}
	m.applied[in.Seq] = struct{}{}
	m.count++
	m.history = append(m.history, HistoryEntry{
		Seq:    in.Seq,
		DX:     in.DX,
		DY:     in.DY,
		TimeMs: timeMs,
	})
	m.trimLocked(timeMs)
	return true
}

// TrimHistory drops input-log entries outside the 200ms replay window.
func (m *MoveGuard) TrimHistory(nowMs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trimLocked(nowMs)
}

// HistoryLen reports how many input-log entries are currently retained.
func (m *MoveGuard) HistoryLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.history)
}

// Confirm marks a server-acknowledged frame. Confirmed inputs leave the dedup
// set and the replay log, keeping every retained structure window-bounded.
func (m *MoveGuard) Confirm(seq int, pos Vec, nowMs int) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if seq > m.ackedSeq {
		m.ackedSeq = seq
	}
	m.confirmed = Snapshot{Seq: seq, Pos: pos, TimeMs: nowMs}
	for old := range m.applied {
		if old <= seq {
			delete(m.applied, old)
		}
	}
	cutoff := nowMs - replayWindowMs
	kept := m.history[:0]
	for _, entry := range m.history {
		if entry.Seq > seq && entry.TimeMs >= cutoff {
			kept = append(kept, entry)
		}
	}
	m.history = kept
	return m.snapshotLocked()
}

// Snapshot exports a deep-copied durable state for restart recovery.
func (m *MoveGuard) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

// Restore rebuilds a guard from a confirmed snapshot plus its input log.
func (m *MoveGuard) Restore(s Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confirmed = Snapshot{Seq: s.Seq, Pos: s.Pos, TimeMs: s.TimeMs}
	m.ackedSeq = s.Seq
	m.applied = make(map[int]struct{}, len(s.Applied))
	for _, seq := range s.Applied {
		m.applied[seq] = struct{}{}
	}
	m.count = len(s.Applied)
	m.history = append([]HistoryEntry(nil), s.History...)
}

// ResimulateSince re-applies the unacknowledged inputs on top of the confirmed
// snapshot and returns the number of replayed inputs and the deterministic
// state sequence. Entries older than the 200ms window are not replayed: the
// confirmed server position is trusted as the starting point.
func (m *MoveGuard) ResimulateSince(nowMs int) (int, []Vec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, start := m.unackedLocked(nowMs)
	return len(entries), ReplayPath(entries, start)
}

// ReplayPath re-applies an input log on top of start. It is a pure function:
// the same entries and start position always yield the identical state
// sequence, which is what makes restart recovery bit-for-bit reproducible.
func ReplayPath(entries []HistoryEntry, start Vec) []Vec {
	path := make([]Vec, 0, len(entries)+1)
	pos := start
	path = append(path, pos)
	for _, entry := range entries {
		pos = Vec{X: pos.X + entry.DX, Y: pos.Y + entry.DY, Z: pos.Z}
		path = append(path, pos)
	}
	return path
}

func (m *MoveGuard) unackedLocked(nowMs int) ([]HistoryEntry, Vec) {
	cutoff := nowMs - replayWindowMs
	out := make([]HistoryEntry, 0, len(m.history))
	for _, entry := range m.history {
		if entry.Seq > m.confirmed.Seq && entry.TimeMs >= cutoff {
			out = append(out, entry)
		}
	}
	return out, m.confirmed.Pos
}

func (m *MoveGuard) trimLocked(nowMs int) {
	cutoff := nowMs - replayWindowMs
	idx := 0
	for idx < len(m.history) && m.history[idx].TimeMs < cutoff {
		idx++
	}
	if idx == 0 {
		return
	}
	trimmed := make([]HistoryEntry, len(m.history)-idx)
	copy(trimmed, m.history[idx:])
	m.history = trimmed
}

func (m *MoveGuard) snapshotLocked() Snapshot {
	applied := make([]int, 0, len(m.applied))
	for seq := range m.applied {
		applied = append(applied, seq)
	}
	sort.Ints(applied)
	return Snapshot{
		Seq:     m.confirmed.Seq,
		Pos:     m.confirmed.Pos,
		TimeMs:  m.confirmed.TimeMs,
		Applied: applied,
		History: append([]HistoryEntry(nil), m.history...),
	}
}

func distance(a, b Vec) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	dz := a.Z - b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}
