package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Vec3 is a position or direction in world space. For reprojection the X and
// Y components are screen space UV coordinates.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// TaaGuard owns the TAA history buffer and the rejection rules.
type TaaGuard struct {
	width  int
	height int
}

// NewTaaGuard returns a guard for a width x height history buffer.
func NewTaaGuard(width, height int) *TaaGuard {
	return &TaaGuard{}
}

// Reproject moves a screen space UV back along its motion vector.
func (g *TaaGuard) Reproject(uv, motion Vec3) (Vec3, error) {
	return Vec3{}, errNotImplemented
}

// MotionVectorTrusted reports whether the motion vector of a pixel may be used
// when its depth jumped in front of the history surface.
func (g *TaaGuard) MotionVectorTrusted(prevDepth, curDepth float64) bool {
	return true
}

// RejectHistory reports whether the history sample must be discarded.
func (g *TaaGuard) RejectHistory(depthDiff, colorDiff float64) bool {
	return false
}

// JitterOffset returns the sub pixel jitter of a frame.
func (g *TaaGuard) JitterOffset(frame int) (float64, float64) {
	return 0, 0
}

// ClampHistory clamps a history sample into the neighbourhood colour bounds.
func (g *TaaGuard) ClampHistory(history, neighbourhoodMin, neighbourhoodMax float64) float64 {
	return 0
}

// TransparentHasMotionVectors reports whether the transparent pass writes
// motion vectors.
func (g *TaaGuard) TransparentHasMotionVectors() bool {
	return false
}

// ResetHistory drops the history after a camera cut.
func (g *TaaGuard) ResetHistory() error {
	return errNotImplemented
}

// HistoryGeneration returns how often the history was reset.
func (g *TaaGuard) HistoryGeneration() uint64 {
	return 0
}

// Resize reallocates the history buffer for a dynamic resolution change.
func (g *TaaGuard) Resize(width, height int) error {
	return errNotImplemented
}

// HistorySize returns the size of the history buffer.
func (g *TaaGuard) HistorySize() (int, int) {
	return 0, 0
}

// AdvanceBlend advances the temporal blend by dt seconds and returns the
// current history weight.
func (g *TaaGuard) AdvanceBlend(dt float64) float64 {
	return 0
}

// ShouldJitter reports whether jitter must be applied for this frame.
func (g *TaaGuard) ShouldJitter(motionMagnitude float64) bool {
	return true
}

// LockHistory takes the history write lock.
func (g *TaaGuard) LockHistory() error {
	return errNotImplemented
}

// UnlockHistory releases the history write lock.
func (g *TaaGuard) UnlockHistory() error {
	return errNotImplemented
}

// RunsBeforeUI reports whether TAA runs before the UI pass.
func (g *TaaGuard) RunsBeforeUI() bool {
	return false
}
