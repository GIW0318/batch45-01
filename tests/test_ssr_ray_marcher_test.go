package task

import (
	"testing"

	task "gsb/batch45-game-b-11"
)

func almost(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestStepSizeGrowsWithRoughness(t *testing.T) {
	m := task.NewSsrMarcher(32)
	smooth, err := m.StepSize(0.05)
	if err != nil {
		t.Fatalf("StepSize(0.05) returned error: %v", err)
	}
	rough, err := m.StepSize(0.8)
	if err != nil {
		t.Fatalf("StepSize(0.8) returned error: %v", err)
	}
	if smooth <= 0 || rough <= 0 {
		t.Fatalf("step size is not positive: smooth=%v rough=%v", smooth, rough)
	}
	if rough <= smooth {
		t.Fatalf("rough surface steps %v but smooth surface steps %v, roughness does not widen the stride", rough, smooth)
	}
}

func TestStepSizeRejectsNegativeRoughness(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if _, err := m.StepSize(0); err != nil {
		t.Fatalf("StepSize(0) returned error: %v", err)
	}
	if _, err := m.StepSize(-0.1); err == nil {
		t.Fatalf("StepSize accepted a negative roughness")
	}
}

func TestStepsScaleWithDistanceAndRespectBudget(t *testing.T) {
	m := task.NewSsrMarcher(8)
	budget := m.MaxSteps()
	if budget != 8 {
		t.Fatalf("MaxSteps() = %d, want 8", budget)
	}
	near, err := m.StepsForDistance(1)
	if err != nil {
		t.Fatalf("StepsForDistance(1) returned error: %v", err)
	}
	far, err := m.StepsForDistance(40)
	if err != nil {
		t.Fatalf("StepsForDistance(40) returned error: %v", err)
	}
	if near <= 0 || far <= 0 {
		t.Fatalf("step counts are not positive: near=%d far=%d", near, far)
	}
	if far <= near {
		t.Fatalf("far ray uses %d steps, near ray %d, the step count is fixed", far, near)
	}
	if far > budget {
		t.Fatalf("far ray uses %d steps, more than the budget of %d", far, budget)
	}
}

func TestConfirmHitAcceptsMatchingDepth(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if !m.ConfirmHit(10, 10.001, 0.9) {
		t.Fatalf("a march sample lying on the scene surface was rejected")
	}
}

func TestConfirmHitRejectsRayBehindSurface(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if !m.ConfirmHit(10, 10.001, 0.9) {
		t.Fatalf("a march sample on the surface was rejected")
	}
	if m.ConfirmHit(10, 20, 0.9) {
		t.Fatalf("a ray far behind the surface confirmed a hit, thin geometry would be shot through")
	}
}

func TestConfirmHitRequiresNormalAlignment(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if !m.ConfirmHit(10, 10.001, 0.9) {
		t.Fatalf("an aligned hit was rejected")
	}
	if m.ConfirmHit(10, 10.001, 0.0) {
		t.Fatalf("a hit on a grazing surface was accepted, the reflected ray direction is wrong")
	}
}

func TestRayThicknessIsPositive(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if m.RayThickness() <= 0 {
		t.Fatalf("ray thickness is %v, thin geometry cannot stop a ray", m.RayThickness())
	}
}

func TestEdgeRayFallsBackToProbe(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if _, ok := m.EdgeRayFallback(task.Vec3{X: 0.5, Y: 0.5}); ok {
		t.Fatalf("an on screen ray was treated as an edge ray")
	}
	if _, ok := m.EdgeRayFallback(task.Vec3{X: 0.0, Y: 0.5}); !ok {
		t.Fatalf("a ray that leaves the screen was dropped without a probe fallback")
	}
}

func TestMipLevelIncreasesWithRoughness(t *testing.T) {
	m := task.NewSsrMarcher(32)
	smooth := m.MipLevelForRoughness(0.05)
	rough := m.MipLevelForRoughness(0.9)
	if smooth < 0 || rough < 0 {
		t.Fatalf("mip levels are not mapped: smooth=%d rough=%d", smooth, rough)
	}
	if rough <= smooth {
		t.Fatalf("rough surface samples mip %d, smooth surface mip %d, reflections are not filtered by roughness", rough, smooth)
	}
}

func TestBilateralWeightFallsOffWithDepth(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if w := m.BilateralWeight(0, 0); !almost(w, 1, 1e-9) {
		t.Fatalf("bilateral weight of an identical sample is %v, want 1", w)
	}
	near := m.BilateralWeight(0.1, 0)
	far := m.BilateralWeight(2, 0)
	if far >= near {
		t.Fatalf("bilateral weight at depth difference 2 is %v, at 0.1 is %v, no falloff", far, near)
	}
}

func TestBlendWithProbeMixesBothSources(t *testing.T) {
	m := task.NewSsrMarcher(32)
	ssr := task.Vec3{X: 1, Y: 1, Z: 1}
	probe := task.Vec3{X: 0, Y: 0, Z: 0}
	got := m.BlendWithProbe(ssr, probe, 0.25)
	if !almost(got.X, 0.25, 1e-9) {
		t.Fatalf("blend of ssr=1, probe=0, weight=0.25 is %v, want 0.25", got.X)
	}
	all := m.BlendWithProbe(ssr, probe, 1)
	if !almost(all.X, 1, 1e-9) {
		t.Fatalf("blend with weight 1 is %v, want the pure ssr colour", all.X)
	}
}

func TestCameraSwitchResetsReflectionHistory(t *testing.T) {
	m := task.NewSsrMarcher(32)
	before := m.ReflectionGeneration()
	if err := m.SetCamera(1); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	if after := m.ReflectionGeneration(); after == before {
		t.Fatalf("reflection generation stayed %d after a camera switch, old reflections remain", after)
	}
}

func TestRayBufferLockPreventsConcurrentWrite(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if err := m.LockRayBuffer(); err != nil {
		t.Fatalf("LockRayBuffer returned error: %v", err)
	}
	if err := m.LockRayBuffer(); err == nil {
		t.Fatalf("ray buffer locked twice, two threads write the same rays")
	}
	if err := m.UnlockRayBuffer(); err != nil {
		t.Fatalf("UnlockRayBuffer returned error: %v", err)
	}
	if err := m.LockRayBuffer(); err != nil {
		t.Fatalf("LockRayBuffer after unlock returned error: %v", err)
	}
}

func TestSsrResolvesBeforeTransparent(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if !m.BeforeTransparent() {
		t.Fatalf("SSR runs after the transparent pass, sorting conflicts with transparent geometry")
	}
}

func TestSsrWorksInHDR(t *testing.T) {
	m := task.NewSsrMarcher(32)
	if !m.WorksInHDR() {
		t.Fatalf("SSR runs after tonemapping, bright reflections are clipped")
	}
}
