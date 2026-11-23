package task

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"math/big"
	"sort"
)

const (
	fixedScale       = 1000
	fixedScale64     = int64(fixedScale)
	serverScale      = 1 << 16
	snapshotInterval = 30
)

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
	out := append([]Input(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Frame != out[j].Frame {
			return out[i].Frame < out[j].Frame
		}
		if out[i].PlayerID != out[j].PlayerID {
			return out[i].PlayerID < out[j].PlayerID
		}
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Payload < out[j].Payload
	})
	return out
}

// MergeFrame keeps exactly one input per player for the given frame.
func (g *LockstepGuard) MergeFrame(frame int, in []Input) map[int]Input {
	merged := make(map[int]Input)
	for _, input := range in {
		if input.Frame != frame {
			continue
		}
		if input.PlayerID < 0 || (g.players > 0 && input.PlayerID >= g.players) {
			continue
		}
		current, ok := merged[input.PlayerID]
		if !ok || input.Seq < current.Seq ||
			(input.Seq == current.Seq && input.Payload < current.Payload) {
			merged[input.PlayerID] = input
		}
	}
	return merged
}

// ContiguousFrames returns the leading run of consecutive frames from the first one.
func (g *LockstepGuard) ContiguousFrames(frames []int) []int {
	if len(frames) == 0 {
		return nil
	}

	sorted := append([]int(nil), frames...)
	sort.Ints(sorted)

	out := make([]int, 0, len(sorted))
	out = append(out, sorted[0])
	for _, frame := range sorted[1:] {
		last := out[len(out)-1]
		if frame == last {
			continue
		}
		if frame != last+1 {
			break
		}
		out = append(out, frame)
	}
	return out
}

// Fixed converts a float64 into the guard fixed point scale.
func (g *LockstepGuard) Fixed(v float64) int64 {
	scaled := math.Round(v * float64(fixedScale))
	switch {
	case math.IsNaN(scaled):
		return 0
	case scaled >= float64(math.MaxInt64):
		return math.MaxInt64
	case scaled <= float64(math.MinInt64):
		return math.MinInt64
	default:
		return int64(scaled)
	}
}

// FixedMul multiplies two fixed point values without losing the scale.
func (g *LockstepGuard) FixedMul(a, b int64) int64 {
	product := a * b
	if a != 0 && product/a != b {
		return fixedMulBig(a, b)
	}
	return product / fixedScale64
}

// Precision is the fixed point scale used by the simulation.
func (g *LockstepGuard) Precision() int {
	return fixedScale
}

// Seed derives the battle rng seed from the match id.
func (g *LockstepGuard) Seed(matchID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(matchID))
	seed := h.Sum64()
	if seed == 0 {
		return 0x9e3779b97f4a7c15
	}
	return seed
}

// CooldownTicks converts a cooldown given in seconds into logical frames.
func (g *LockstepGuard) CooldownTicks(seconds int, tickRate int) int {
	if seconds <= 0 || tickRate <= 0 {
		return 0
	}
	return seconds * tickRate
}

// EntityOrder is the deterministic iteration order for entity ids.
func (g *LockstepGuard) EntityOrder(ids []int) []int {
	out := append([]int(nil), ids...)
	sort.Ints(out)
	return out
}

// ReplayEntry builds a replay record that keeps the frame number.
func (g *LockstepGuard) ReplayEntry(frame int, checksum uint64) ReplayRecord {
	return ReplayRecord{Frame: frame, Checksum: checksum}
}

// ResumeFrame is the frame a reconnecting client must resume from.
func (g *LockstepGuard) ResumeFrame(lastAcked int) int {
	if lastAcked <= 0 {
		return 0
	}
	return (lastAcked / snapshotInterval) * snapshotInterval
}

// PushInput appends an input unless the buffer already reached capacity.
func (g *LockstepGuard) PushInput(buf []Input, capacity int, in Input) ([]Input, bool) {
	if capacity <= 0 || len(buf) >= capacity {
		return buf, false
	}
	out := make([]Input, len(buf), len(buf)+1)
	copy(out, buf)
	out = append(out, in)
	return out, true
}

// RollingChecksum covers every intermediate frame state, not only the last one.
func (g *LockstepGuard) RollingChecksum(states []uint64) uint64 {
	if len(states) == 0 {
		return 0
	}

	h := fnv.New64a()
	var encoded [8]byte
	for _, state := range states {
		binary.LittleEndian.PutUint64(encoded[:], state)
		_, _ = h.Write(encoded[:])
	}
	sum := h.Sum64()
	if sum == 0 {
		return 1
	}
	return sum
}

// NormalizePrecision clamps a peer fixed point scale to the server scale.
func (g *LockstepGuard) NormalizePrecision(peerScale int) int {
	server := g.ServerScale()
	if peerScale <= 0 || peerScale > server {
		return server
	}
	return peerScale
}

// DesyncFrame reports the first frame index where two checksum streams differ.
func (g *LockstepGuard) DesyncFrame(a, b []uint64) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return limit
	}
	return -1
}

// ServerScale is the fixed point scale mandated by the server.
func (g *LockstepGuard) ServerScale() int {
	return serverScale
}

func fixedMulBig(a, b int64) int64 {
	var product big.Int
	var x, y big.Int
	x.SetInt64(a)
	y.SetInt64(b)
	product.Mul(&x, &y)
	product.Quo(&product, big.NewInt(fixedScale64))
	if product.IsInt64() {
		return product.Int64()
	}
	if product.Sign() < 0 {
		return math.MinInt64
	}
	return math.MaxInt64
}
