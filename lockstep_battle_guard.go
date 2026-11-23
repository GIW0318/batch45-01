package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Input is a single player command for one logical frame.
type Input struct {
	PlayerID int
	Frame    int
	Seq      int
	Payload  string
}

// ReplayRecord is one frame entry of a replay file.
type ReplayRecord struct {
	Frame    int
	Checksum uint64
}

// LockstepGuard keeps frame synchronous battle simulation deterministic.
type LockstepGuard struct {
	players int
}

// NewLockstepGuard builds a guard for the given player count.
func NewLockstepGuard(players int) *LockstepGuard {
	return &LockstepGuard{players: players}
}

// SortInputs orders inputs by frame, then player id, then per player sequence.
func (g *LockstepGuard) SortInputs(in []Input) []Input {
	return nil
}

// MergeFrame keeps exactly one input per player for the given frame.
func (g *LockstepGuard) MergeFrame(frame int, in []Input) map[int]Input {
	return nil
}

// ContiguousFrames returns the leading run of consecutive frames from the first one.
func (g *LockstepGuard) ContiguousFrames(frames []int) []int {
	return nil
}

// Fixed converts a float64 into the guard fixed point scale.
func (g *LockstepGuard) Fixed(v float64) int64 {
	return 0
}

// FixedMul multiplies two fixed point values without losing the scale.
func (g *LockstepGuard) FixedMul(a, b int64) int64 {
	return 0
}

// Precision is the fixed point scale used by the simulation.
func (g *LockstepGuard) Precision() int {
	return 0
}

// Seed derives the battle rng seed from the match id.
func (g *LockstepGuard) Seed(matchID string) uint64 {
	return 0
}

// CooldownTicks converts a cooldown given in seconds into logical frames.
func (g *LockstepGuard) CooldownTicks(seconds int, tickRate int) int {
	return 0
}

// EntityOrder is the deterministic iteration order for entity ids.
func (g *LockstepGuard) EntityOrder(ids []int) []int {
	return nil
}

// ReplayEntry builds a replay record that keeps the frame number.
func (g *LockstepGuard) ReplayEntry(frame int, checksum uint64) ReplayRecord {
	return ReplayRecord{}
}

// ResumeFrame is the frame a reconnecting client must resume from.
func (g *LockstepGuard) ResumeFrame(lastAcked int) int {
	return 0
}

// PushInput appends an input unless the buffer already reached capacity.
func (g *LockstepGuard) PushInput(buf []Input, capacity int, in Input) ([]Input, bool) {
	return nil, false
}

// RollingChecksum covers every intermediate frame state, not only the last one.
func (g *LockstepGuard) RollingChecksum(states []uint64) uint64 {
	return 0
}

// NormalizePrecision clamps a peer fixed point scale to the server scale.
func (g *LockstepGuard) NormalizePrecision(peerScale int) int {
	return 0
}

// DesyncFrame reports the first frame index where two checksum streams differ.
func (g *LockstepGuard) DesyncFrame(a, b []uint64) int {
	return 0
}

// ServerScale is the fixed point scale mandated by the server.
func (g *LockstepGuard) ServerScale() int {
	return 1 << 16
}
