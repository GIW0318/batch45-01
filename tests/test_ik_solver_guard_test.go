package task

import (
	"sync"
	"testing"

	task "gsb/batch45-game-b-18"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func vecNear(a, b task.Vec3, eps float64) bool {
	return near(a.X, b.X, eps) && near(a.Y, b.Y, eps) && near(a.Z, b.Z, eps)
}

func TestProjectTargetToGroundPlane(t *testing.T) {
	s := task.NewIkSolver()
	got := s.ProjectToPlane(task.Vec3{X: 1, Y: 1}, task.Vec3{Y: 1})
	if !vecNear(got, task.Vec3{X: 1}, 1e-9) {
		t.Fatalf("ProjectToPlane = %+v, want {1 0 0}", got)
	}
}

func TestKneePolePerpendicular(t *testing.T) {
	s := task.NewIkSolver()
	root := task.Vec3{}
	target := task.Vec3{Y: 1}
	forward := task.Vec3{Z: 1}
	got := s.KneePole(root, target, forward)
	if !vecNear(got, task.Vec3{X: 1}, 1e-9) {
		t.Fatalf("KneePole = %+v, want {1 0 0}", got)
	}
	dot := got.X*forward.X + got.Y*forward.Y + got.Z*forward.Z
	if !near(dot, 0, 1e-9) {
		t.Fatalf("knee pole is not perpendicular to forward: dot = %v", dot)
	}
}

func TestClampJointAngleUpper(t *testing.T) {
	s := task.NewIkSolver()
	if got := s.ClampJointAngle(200, -90, 90); !near(got, 90, 1e-9) {
		t.Fatalf("ClampJointAngle(200) = %v, want 90", got)
	}
}

func TestClampJointAngleLower(t *testing.T) {
	s := task.NewIkSolver()
	if got := s.ClampJointAngle(-200, -90, 90); !near(got, -90, 1e-9) {
		t.Fatalf("ClampJointAngle(-200) = %v, want -90", got)
	}
}

func TestFootPitchOn45DegreeSlope(t *testing.T) {
	s := task.NewIkSolver()
	got := s.FootPitch(task.Vec3{Y: 0.7071067811865476, Z: 0.7071067811865476})
	if !near(got, 45, 1e-6) {
		t.Fatalf("FootPitch(45deg slope) = %v, want 45", got)
	}
}

func TestFootPitchOn60DegreeSlope(t *testing.T) {
	s := task.NewIkSolver()
	got := s.FootPitch(task.Vec3{Y: 0.5, Z: 0.8660254037844386})
	if !near(got, 60, 1e-6) {
		t.Fatalf("FootPitch(60deg slope) = %v, want 60", got)
	}
}

func TestRootMotionBlend(t *testing.T) {
	s := task.NewIkSolver()
	got := s.ResolveRootMotion(task.Vec3{}, task.Vec3{X: 2}, 0.5)
	if !vecNear(got, task.Vec3{X: 1}, 1e-9) {
		t.Fatalf("ResolveRootMotion = %+v, want {1 0 0}", got)
	}
}

func TestClampToReachOutOfRange(t *testing.T) {
	s := task.NewIkSolver()
	got, ok := s.ClampToReach(task.Vec3{}, task.Vec3{Y: 3}, 2)
	if !vecNear(got, task.Vec3{Y: 2}, 1e-9) {
		t.Fatalf("ClampToReach = %+v, want {0 2 0}", got)
	}
	if ok {
		t.Fatal("ClampToReach reported an out of reach target as reachable")
	}
}

func TestClampToReachInRange(t *testing.T) {
	s := task.NewIkSolver()
	got, ok := s.ClampToReach(task.Vec3{}, task.Vec3{Y: 1}, 2)
	if !vecNear(got, task.Vec3{Y: 1}, 1e-9) {
		t.Fatalf("ClampToReach changed an in range target: %+v", got)
	}
	if !ok {
		t.Fatal("ClampToReach reported an in range target as unreachable")
	}
}

func TestSolveOrderRootToLeaf(t *testing.T) {
	s := task.NewIkSolver()
	hierarchy := map[string]string{"hand": "forearm", "forearm": "upperarm"}
	got := s.SolveOrder([]string{"hand", "upperarm", "forearm"}, hierarchy)
	want := []string{"upperarm", "forearm", "hand"}
	if len(got) != len(want) {
		t.Fatalf("SolveOrder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SolveOrder = %v, want %v", got, want)
		}
	}
}

func TestConcurrentOffsetUpdates(t *testing.T) {
	s := task.NewIkSolver()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.AddOffset("foot_l", 1)
		}()
	}
	wg.Wait()
	if got := s.Offset("foot_l"); !near(got, 100, 1e-9) {
		t.Fatalf("accumulated offset = %v, want 100", got)
	}
}

func TestPlatformFollowsMovingBase(t *testing.T) {
	s := task.NewIkSolver()
	got := s.PlatformTarget(task.Vec3{X: 1}, 0.5)
	if !vecNear(got, task.Vec3{X: 0.5}, 1e-9) {
		t.Fatalf("PlatformTarget = %+v, want {0.5 0 0}", got)
	}
}

func TestSmoothWeightRamp(t *testing.T) {
	s := task.NewIkSolver()
	if got := s.SmoothWeight(0, 1, 0.05, 10); !near(got, 0.5, 1e-9) {
		t.Fatalf("SmoothWeight(0, 1, dt=0.05, rate=10) = %v, want 0.5", got)
	}
}

func TestFallbackPoseOnFailure(t *testing.T) {
	s := task.NewIkSolver()
	prev := []task.Vec3{{X: 1}, {X: 2}}
	next := []task.Vec3{{X: 9}, {X: 9}}
	got := s.FallbackPose(prev, next, false)
	if len(got) != 2 {
		t.Fatalf("FallbackPose returned %d bones, want 2", len(got))
	}
	if !vecNear(got[0], prev[0], 1e-9) || !vecNear(got[1], prev[1], 1e-9) {
		t.Fatalf("failed solve did not keep the previous pose: %+v", got)
	}
}

func TestLODWeightAtLOD1(t *testing.T) {
	s := task.NewIkSolver()
	if got := s.LODWeight(1, 0.8); !near(got, 0.4, 1e-9) {
		t.Fatalf("LODWeight(1, 0.8) = %v, want 0.4", got)
	}
}
