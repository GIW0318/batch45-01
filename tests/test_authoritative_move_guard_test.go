package task

import (
	"testing"

	impl "gsb/batch45-game-a-03"
)

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestEncodeInputKeepsSequenceNumber(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	in := m.EncodeInput(42, 0.5, -0.25)
	if in.Seq != 42 {
		t.Fatalf("input sequence = %d, want 42", in.Seq)
	}
	if !almostEqual(in.DX, 0.5) {
		t.Fatalf("input dx = %v, want 0.5", in.DX)
	}
}

func TestDuplicateInputAppliedOnlyOnce(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	first, err := m.Apply(7)
	if err != nil {
		t.Fatalf("applying input 7 failed: %v", err)
	}
	if first != 1 {
		t.Fatalf("first apply count = %d, want 1", first)
	}
	second, err := m.Apply(7)
	if err != nil {
		t.Fatalf("re-applying input 7 failed: %v", err)
	}
	if second != 1 {
		t.Fatalf("duplicated input executed %d times in total, want 1", second)
	}
}

func TestCorrectionBlendsTowardsServerPosition(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	got := m.Blend(impl.Vec{}, impl.Vec{X: 10}, 0.5)
	if !almostEqual(got.X, 5) {
		t.Fatalf("blended x = %v, want 5 (halfway between prediction and server)", got.X)
	}
}

func TestStepDistanceFollowsDeltaTime(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	got := m.Step(4, 50)
	if !almostEqual(got.X, 0.2) {
		t.Fatalf("step at 4 m/s for 50ms = %v, want 0.2", got.X)
	}
}

func TestIntegrateIndependentOfFrameRate(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	fast := m.Integrate(4, 25, 10)
	slow := m.Integrate(4, 5, 50)
	if !almostEqual(fast.X, 1.0) {
		t.Fatalf("25 steps of 10ms at 4 m/s = %v, want 1.0", fast.X)
	}
	if !almostEqual(slow.X, 1.0) {
		t.Fatalf("5 steps of 50ms at 4 m/s = %v, want 1.0", slow.X)
	}
}

func TestShouldCorrectLargeDeviation(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	if !m.ShouldCorrect(impl.Vec{X: 100}, impl.Vec{}) {
		t.Fatalf("a 100 unit deviation must trigger a correction")
	}
}

func TestWithinToleranceForSmallJitter(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	if !m.WithinTolerance(impl.Vec{X: 0.2}, impl.Vec{}) {
		t.Fatalf("0.2 units of jitter must be inside the 1.5 tolerance")
	}
}

func TestLagCompensateRewindsToInputTime(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	if got := m.LagCompensate(1000, 900, 250); got != 900 {
		t.Fatalf("lag compensation rewound to %d, want the input time 900", got)
	}
}

func TestClampDriftSnapsBackBeyondTolerance(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	got := m.ClampDrift(impl.Vec{X: 10, Y: 5, Z: 3}, impl.Vec{X: 2, Y: 3})
	if !almostEqual(got.X, 2) || !almostEqual(got.Y, 3) || !almostEqual(got.Z, 0) {
		t.Fatalf("clamped drift = %+v, want the server position {2 3 0}", got)
	}
}

func TestOnVehicleAddsLocalOffset(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	got := m.OnVehicle(impl.Vec{X: 1, Y: 2}, impl.Vec{X: 10})
	if !almostEqual(got.X, 11) || !almostEqual(got.Y, 2) {
		t.Fatalf("player on vehicle at %+v, want {11 2 0}", got)
	}
}

func TestVerticalUsesDeltaTime(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	got := m.Vertical(1.0, 2.0, 500)
	if !almostEqual(got.Z, 2.0) {
		t.Fatalf("vertical z after 500ms at 2 m/s = %v, want 2.0", got.Z)
	}
}

func TestAnimationResetsAfterRollback(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	if got := m.AnimationAfterRollback(3); got != "idle_restart" {
		t.Fatalf("animation after rolling back 3 frames = %q, want idle_restart", got)
	}
}

func TestTickDeltaComparesServerAndClient(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	if got := m.TickDelta(101, 98); got != 3 {
		t.Fatalf("tick delta = %d, want 3", got)
	}
}

func TestBlockedByWallCheckOnClient(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	if !m.Blocked(impl.Vec{X: 5}, impl.Vec{X: 5}) {
		t.Fatalf("client must treat a position inside the wall as blocked")
	}
	if m.Blocked(impl.Vec{X: 10}, impl.Vec{X: 0}) {
		t.Fatalf("client must allow a position far away from the wall")
	}
}

func TestResimulateUnacknowledgedInputs(t *testing.T) {
	m := impl.NewMoveGuard(1.5)
	if got := m.Resimulate([]int{1, 2, 3, 4, 5}, 3); got != 2 {
		t.Fatalf("resimulated %d inputs, want the 2 inputs after the acked frame", got)
	}
}
