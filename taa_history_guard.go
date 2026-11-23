package task

import (
	"errors"
	"math"
	"sync"
)

var (
	errInvalidSize      = errors.New("taa: invalid history size")
	errHistoryLocked    = errors.New("taa: history is already locked")
	errHistoryNotLocked = errors.New("taa: history is not locked")
)

const (
	// History is allocated once for the largest common render target. A
	// dynamic-resolution change only moves the logical viewport inside this
	// allocation.
	defaultMaxHistoryPixels = 4096 * 2160
	hardMaxHistoryPixels    = 7680 * 4320

	depthOcclusionRelative = 0.10
	depthOcclusionEpsilon  = 1e-6

	historyDepthReject = 0.02
	historyColorReject = 0.10

	blendHalfLifeSeconds = 0.10
)

// Vec3 is a position or direction in world space. For reprojection the X and
// Y components are screen space UV coordinates.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// TaaGuard owns the TAA history buffer, the rejection rules and the history
// writer lock.
type TaaGuard struct {
	mu sync.Mutex

	width  int
	height int

	// history is allocated at the maximum supported pixel count. Resize never
	// reallocates while the requested target fits in this backing array.
	history        []float32
	capacityPixels int

	generation   uint64
	blendSeconds float64

	writerMu   sync.Mutex
	writerHeld bool
}

// NewTaaGuard returns a guard for a width x height history buffer.
func NewTaaGuard(width, height int) *TaaGuard {
	g := &TaaGuard{}
	pixels, ok := pixelCount(width, height)
	if !ok || pixels > hardMaxHistoryPixels {
		return g
	}

	g.width = width
	g.height = height
	g.capacityPixels = maxInt(pixels, defaultMaxHistoryPixels)
	g.history = make([]float32, g.capacityPixels)
	return g
}

// Reproject moves a screen space UV back along its motion vector.
func (g *TaaGuard) Reproject(uv, motion Vec3) (Vec3, error) {
	if !g.validSize() {
		return Vec3{}, errInvalidSize
	}
	return Vec3{
		X: uv.X - motion.X,
		Y: uv.Y - motion.Y,
		Z: uv.Z,
	}, nil
}

// MotionVectorTrusted reports whether the motion vector of a pixel may be used
// when its depth jumped in front of the history surface.
func (g *TaaGuard) MotionVectorTrusted(prevDepth, curDepth float64) bool {
	if !isFinite(prevDepth) || !isFinite(curDepth) ||
		prevDepth < 0 || curDepth < 0 {
		return false
	}

	depthDelta := math.Abs(curDepth - prevDepth)
	scale := math.Max(prevDepth, curDepth)
	tolerance := math.Max(depthOcclusionEpsilon, scale*depthOcclusionRelative)
	return depthDelta <= tolerance
}

// RejectHistory reports whether the history sample must be discarded.
func (g *TaaGuard) RejectHistory(depthDiff, colorDiff float64) bool {
	if math.IsNaN(depthDiff) || math.IsNaN(colorDiff) {
		return true
	}
	return math.Abs(depthDiff) > historyDepthReject ||
		math.Abs(colorDiff) > historyColorReject
}

// JitterOffset returns the sub pixel jitter of a frame. The eight sample
// sequence is deterministic and every sample lies exactly half a pixel from
// the centre.
func (g *TaaGuard) JitterOffset(frame int) (float64, float64) {
	const diagonal = 0.35355339059327373 // sqrt(0.5) / 2

	sequence := [8][2]float64{
		{0.5, 0},
		{0, 0.5},
		{-0.5, 0},
		{0, -0.5},
		{diagonal, diagonal},
		{-diagonal, diagonal},
		{-diagonal, -diagonal},
		{diagonal, -diagonal},
	}

	index := frame % len(sequence)
	if index < 0 {
		index += len(sequence)
	}
	return sequence[index][0], sequence[index][1]
}

// ClampHistory clamps a history sample into the neighbourhood colour bounds.
func (g *TaaGuard) ClampHistory(history, neighbourhoodMin, neighbourhoodMax float64) float64 {
	if neighbourhoodMin > neighbourhoodMax {
		neighbourhoodMin, neighbourhoodMax = neighbourhoodMax, neighbourhoodMin
	}
	if history < neighbourhoodMin {
		return neighbourhoodMin
	}
	if history > neighbourhoodMax {
		return neighbourhoodMax
	}
	return history
}

// TransparentHasMotionVectors reports whether the transparent pass writes
// motion vectors.
func (g *TaaGuard) TransparentHasMotionVectors() bool {
	return true
}

// ResetHistory drops the history after a camera cut.
func (g *TaaGuard) ResetHistory() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.history != nil {
		end := g.width * g.height
		if end > len(g.history) {
			end = len(g.history)
		}
		clear(g.history[:end])
	}
	g.generation++
	g.blendSeconds = 0
	return nil
}

// HistoryGeneration returns how often the history was reset.
func (g *TaaGuard) HistoryGeneration() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.generation
}

// Resize reallocates the history buffer for a dynamic resolution change.
func (g *TaaGuard) Resize(width, height int) error {
	pixels, ok := pixelCount(width, height)
	if !ok || pixels > hardMaxHistoryPixels {
		return errInvalidSize
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.width == width && g.height == height {
		return nil
	}

	// The backing array is only replaced when a target exceeds the initial
	// upper bound. Normal dynamic-resolution oscillation stays allocation
	// free and only changes the logical dimensions.
	if pixels > g.capacityPixels {
		capacity := maxInt(pixels, defaultMaxHistoryPixels)
		g.history = make([]float32, capacity)
		g.capacityPixels = capacity
	}

	g.width = width
	g.height = height
	g.generation++ // invalidate samples captured at the old resolution
	return nil
}

// HistorySize returns the size of the history buffer.
func (g *TaaGuard) HistorySize() (int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.width, g.height
}

// AdvanceBlend advances the temporal blend by dt seconds and returns the
// current history weight. The weight is a function of accumulated time, so
// 30 Hz and 60 Hz reach the same value at the same wall-clock time.
func (g *TaaGuard) AdvanceBlend(dt float64) float64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	if dt > 0 && !math.IsNaN(dt) {
		g.blendSeconds += dt
		if math.IsInf(g.blendSeconds, 1) {
			g.blendSeconds = math.MaxFloat64
		}
	}

	weight := 1 - math.Exp(-g.blendSeconds/blendHalfLifeSeconds)
	switch {
	case weight < 0:
		return 0
	case weight > 1:
		return 1
	default:
		return weight
	}
}

// ShouldJitter reports whether jitter must be applied for this frame.
func (g *TaaGuard) ShouldJitter(motionMagnitude float64) bool {
	if math.IsNaN(motionMagnitude) {
		return false
	}
	return math.Abs(motionMagnitude) > 1e-6
}

// LockHistory takes the history write lock.
func (g *TaaGuard) LockHistory() error {
	if !g.validSize() {
		return errInvalidSize
	}
	if !g.writerMu.TryLock() {
		return errHistoryLocked
	}

	g.mu.Lock()
	g.writerHeld = true
	g.mu.Unlock()
	return nil
}

// UnlockHistory releases the history write lock.
func (g *TaaGuard) UnlockHistory() error {
	g.mu.Lock()
	if !g.writerHeld {
		g.mu.Unlock()
		return errHistoryNotLocked
	}
	g.writerHeld = false
	g.mu.Unlock()

	g.writerMu.Unlock()
	return nil
}

// RunsBeforeUI reports whether TAA runs before the UI pass.
func (g *TaaGuard) RunsBeforeUI() bool {
	return true
}

func (g *TaaGuard) validSize() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.width > 0 && g.height > 0
}

func pixelCount(width, height int) (int, bool) {
	if width <= 0 || height <= 0 {
		return 0, false
	}
	const maxInt = int(^uint(0) >> 1)
	if width > maxInt/height {
		return 0, false
	}
	return width * height, true
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
