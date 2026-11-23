package task

import (
	"testing"

	task "gsb/batch45-game-b-10"
)

func near(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestReprojectFollowsMotionVector(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	got, err := g.Reproject(task.Vec3{X: 0.5, Y: 0.5}, task.Vec3{X: 0.1, Y: -0.05})
	if err != nil {
		t.Fatalf("Reproject returned error: %v", err)
	}
	if !near(got.X, 0.4, 1e-9) || !near(got.Y, 0.55, 1e-9) {
		t.Fatalf("reprojected UV = %+v, want {0.4 0.55}, the history was not moved by the motion vector", got)
	}
}

func TestDisocclusionRejectsMotionVector(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if !g.MotionVectorTrusted(10, 10) {
		t.Fatalf("motion vector rejected on an unchanged surface")
	}
	if g.MotionVectorTrusted(10, 0.5) {
		t.Fatalf("motion vector trusted across an occlusion boundary, it would smear the new surface")
	}
}

func TestHistoryRejectUsesDepthNotOnlyColour(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if !g.RejectHistory(0.5, 0) {
		t.Fatalf("history accepted with a large depth difference and identical colour, ghosting appears")
	}
}

func TestHistoryKeepsSimilarSurface(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if g.RejectHistory(0.0001, 0.01) {
		t.Fatalf("history rejected for a surface with matching depth and small colour noise")
	}
	if !g.RejectHistory(0.5, 0.9) {
		t.Fatalf("history kept a sample that differs in both depth and colour")
	}
}

func TestJitterOffsetVariesPerFrame(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	x0, y0 := g.JitterOffset(0)
	x1, y1 := g.JitterOffset(1)
	if x0 == x1 && y0 == y1 {
		t.Fatalf("jitter offset is identical on frame 0 and 1: %v %v", x0, y0)
	}
}

func TestJitterStaysWithinHalfPixel(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	distinct := map[[2]float64]bool{}
	for frame := 0; frame < 8; frame++ {
		x, y := g.JitterOffset(frame)
		if x < -0.5 || x > 0.5 || y < -0.5 || y > 0.5 {
			t.Fatalf("jitter offset (%v,%v) on frame %d leaves the half pixel range", x, y, frame)
		}
		distinct[[2]float64{x, y}] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("the jitter sequence contains %d distinct offsets, the projection is not jittered", len(distinct))
	}
}

func TestClampHistoryBoundsToNeighbourhood(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if got := g.ClampHistory(2, 0, 1); got != 1 {
		t.Fatalf("history 2 clamped to %v, want 1", got)
	}
	if got := g.ClampHistory(-3, 0, 1); got != 0 {
		t.Fatalf("history -3 clamped to %v, want 0", got)
	}
	if got := g.ClampHistory(0.5, 0, 1); got != 0.5 {
		t.Fatalf("history 0.5 clamped to %v, want an untouched 0.5", got)
	}
}

func TestTransparentPassWritesMotionVectors(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if !g.TransparentHasMotionVectors() {
		t.Fatalf("transparent objects have no motion vectors, TAA smears them")
	}
}

func TestResetHistoryBumpsGeneration(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	before := g.HistoryGeneration()
	if err := g.ResetHistory(); err != nil {
		t.Fatalf("ResetHistory returned error: %v", err)
	}
	if after := g.HistoryGeneration(); after == before {
		t.Fatalf("history generation stayed %d after a reset", after)
	}
}

func TestResizeUpdatesHistorySize(t *testing.T) {
	g := task.NewTaaGuard(8, 8)
	if err := g.Resize(16, 9); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}
	w, h := g.HistorySize()
	if w != 16 || h != 9 {
		t.Fatalf("history size = %dx%d after resize to 16x9, the history and the render target disagree", w, h)
	}
}

func TestResizeRejectsNonPositiveSize(t *testing.T) {
	g := task.NewTaaGuard(8, 8)
	if err := g.Resize(16, 16); err != nil {
		t.Fatalf("Resize rejected a valid 16x16 target: %v", err)
	}
	if err := g.Resize(0, 16); err == nil {
		t.Fatalf("Resize accepted a zero width history buffer")
	}
}

func TestBlendWeightIsFrameRateIndependent(t *testing.T) {
	oneStep := task.NewTaaGuard(1920, 1080)
	twoSteps := task.NewTaaGuard(1920, 1080)
	at30 := oneStep.AdvanceBlend(1.0 / 30.0)
	at60 := twoSteps.AdvanceBlend(1.0 / 60.0)
	at60 = twoSteps.AdvanceBlend(1.0 / 60.0)
	if at30 <= 0 || at60 <= 0 {
		t.Fatalf("history weight is not rising: 30Hz=%v 60Hz=%v", at30, at60)
	}
	if !near(at30, at60, 0.02) {
		t.Fatalf("history weight depends on frame rate: 30Hz=%v 60Hz=%v", at30, at60)
	}
}

func TestStaticSceneDoesNotJitter(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if g.ShouldJitter(0) {
		t.Fatalf("jitter applied to a static camera, the image still wobbles")
	}
	if !g.ShouldJitter(5) {
		t.Fatalf("jitter skipped for a fast moving camera")
	}
}

func TestHistoryLockPreventsConcurrentWrite(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if err := g.LockHistory(); err != nil {
		t.Fatalf("LockHistory returned error: %v", err)
	}
	if err := g.LockHistory(); err == nil {
		t.Fatalf("history locked twice, two writers share the buffer")
	}
	if err := g.UnlockHistory(); err != nil {
		t.Fatalf("UnlockHistory returned error: %v", err)
	}
	if err := g.LockHistory(); err != nil {
		t.Fatalf("LockHistory after unlock returned error: %v", err)
	}
}

func TestTaaRunsBeforeUI(t *testing.T) {
	g := task.NewTaaGuard(1920, 1080)
	if !g.RunsBeforeUI() {
		t.Fatalf("TAA runs after the UI pass and smears the HUD")
	}
}
