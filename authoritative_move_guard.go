package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

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

// MoveGuard arbitrates client prediction and server correction.
type MoveGuard struct {
	threshold float64
}

// NewMoveGuard builds a guard with the given correction distance threshold.
func NewMoveGuard(threshold float64) *MoveGuard {
	return &MoveGuard{threshold: threshold}
}

// EncodeInput stamps a client input with its sequence number.
func (m *MoveGuard) EncodeInput(seq int, dx, dy float64) Input {
	return Input{}
}

// Apply executes an input once and reports how many inputs were applied.
func (m *MoveGuard) Apply(seq int) (int, error) {
	return 0, errNotImplemented
}

// Blend interpolates between the predicted and the authoritative position.
func (m *MoveGuard) Blend(from, to Vec, alpha float64) Vec {
	return Vec{}
}

// Step is the distance travelled during dtMs at the given speed.
func (m *MoveGuard) Step(speed float64, dtMs int) Vec {
	return Vec{}
}

// Integrate accumulates movement over several fixed steps.
func (m *MoveGuard) Integrate(speed float64, steps int, dtMs int) Vec {
	return Vec{}
}

// ShouldCorrect reports whether the deviation warrants a server correction.
func (m *MoveGuard) ShouldCorrect(predicted, server Vec) bool {
	return false
}

// WithinTolerance reports whether a deviation is acceptable jitter.
func (m *MoveGuard) WithinTolerance(predicted, server Vec) bool {
	return false
}

// LagCompensate rewinds the server to the time the input was produced.
func (m *MoveGuard) LagCompensate(nowMs, inputMs, maxMs int) int {
	return 0
}

// ClampDrift pulls a prediction back to the server position when it drifted too far.
func (m *MoveGuard) ClampDrift(predicted, server Vec) Vec {
	return Vec{}
}

// OnVehicle offsets a player local position by the vehicle position.
func (m *MoveGuard) OnVehicle(local, vehicle Vec) Vec {
	return Vec{}
}

// Vertical integrates vertical movement separately from horizontal movement.
func (m *MoveGuard) Vertical(base, vy float64, dtMs int) Vec {
	return Vec{}
}

// AnimationAfterRollback is the animation state a rolled back client must reset to.
func (m *MoveGuard) AnimationAfterRollback(frames int) string {
	return ""
}

// TickDelta is the frame gap between the server tick and the client tick.
func (m *MoveGuard) TickDelta(serverTick, clientTick int) int {
	return 0
}

// Blocked is the client side wall test used before sending a move.
func (m *MoveGuard) Blocked(pos, wall Vec) bool {
	return false
}

// Resimulate replays unacknowledged inputs on top of the last acknowledged frame.
func (m *MoveGuard) Resimulate(seqs []int, lastAcked int) int {
	return 0
}
