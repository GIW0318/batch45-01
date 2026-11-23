package task

import (
	"sync"
	"testing"

	task "gsb/batch45-game-b-17"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestSetStateActivatesState(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.AddState("idle", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.SetState("idle"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if got := sm.CurrentState(); got != "idle" {
		t.Fatalf("CurrentState = %q, want idle", got)
	}
}

func TestTransitionInterruptsCurrentBlend(t *testing.T) {
	sm := task.NewAnimStateMachine()
	for _, name := range []string{"idle", "run", "jump"} {
		if err := sm.AddState(name, 1.0); err != nil {
			t.Fatalf("AddState(%s) returned error: %v", name, err)
		}
	}
	if err := sm.AddTransition("idle", "run", "go", 0.25); err != nil {
		t.Fatalf("AddTransition returned error: %v", err)
	}
	if err := sm.AddTransition("run", "jump", "attack", 0.25); err != nil {
		t.Fatalf("AddTransition returned error: %v", err)
	}
	if err := sm.SetState("idle"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if err := sm.Fire("go"); err != nil {
		t.Fatalf("Fire(go) returned error: %v", err)
	}
	if err := sm.Update(0.1, 1.0); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if err := sm.Fire("attack"); err != nil {
		t.Fatalf("Fire(attack) returned error: %v", err)
	}
	if got := sm.CurrentState(); got != "jump" {
		t.Fatalf("CurrentState after interrupt = %q, want jump", got)
	}
	if got := sm.TransitionProgress(); !near(got, 0, 1e-9) {
		t.Fatalf("TransitionProgress after interrupt = %v, want 0", got)
	}
}

func TestTransitionUsesAnimTime(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.AddState("idle", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddState("run", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddTransition("idle", "run", "go", 1.0); err != nil {
		t.Fatalf("AddTransition returned error: %v", err)
	}
	if err := sm.SetState("idle"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if err := sm.Fire("go"); err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}
	if err := sm.Update(0.1, 2.0); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if got := sm.TransitionProgress(); !near(got, 0.2, 1e-6) {
		t.Fatalf("TransitionProgress after dt=0.1 at 2x = %v, want 0.2", got)
	}
}

func TestSelectConditionDeterministic(t *testing.T) {
	sm := task.NewAnimStateMachine()
	got, err := sm.SelectCondition([]string{"z", "a", "m"})
	if err != nil {
		t.Fatalf("SelectCondition returned error: %v", err)
	}
	if got != "a" {
		t.Fatalf("SelectCondition = %q, want a", got)
	}
}

func TestEventsSurviveTransition(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.AddEvent(0.5, "hit"); err != nil {
		t.Fatalf("AddEvent returned error: %v", err)
	}
	if err := sm.AddState("idle", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddState("run", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddTransition("idle", "run", "go", 0.2); err != nil {
		t.Fatalf("AddTransition returned error: %v", err)
	}
	if err := sm.SetState("idle"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if err := sm.Fire("go"); err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}
	events := sm.DrainEvents()
	if !contains(events, "hit") {
		t.Fatalf("animation event dropped during transition: %v", events)
	}
}

func TestInterruptWindowBounds(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.SetInterruptWindow(0.3, 0.6); err != nil {
		t.Fatalf("SetInterruptWindow returned error: %v", err)
	}
	if !sm.InInterruptWindow(0.45) {
		t.Fatal("0.45 is inside the interrupt window")
	}
	if sm.InInterruptWindow(0.1) {
		t.Fatal("0.1 is outside the interrupt window")
	}
}

func TestComboInputBuffer(t *testing.T) {
	sm := task.NewAnimStateMachine()
	sm.BufferInput("combo2", 3)
	got, ok := sm.ConsumeBuffer(1)
	if !ok {
		t.Fatal("buffered combo input was dropped")
	}
	if got != "combo2" {
		t.Fatalf("consumed input = %q, want combo2", got)
	}
}

func TestNormalizeLayerWeights(t *testing.T) {
	sm := task.NewAnimStateMachine()
	got := sm.NormalizeLayerWeights([]float32{1, 3})
	if len(got) != 2 {
		t.Fatalf("NormalizeLayerWeights returned %d weights, want 2", len(got))
	}
	if !near(float64(got[0]), 0.25, 1e-6) || !near(float64(got[1]), 0.75, 1e-6) {
		t.Fatalf("NormalizeLayerWeights = %v, want [0.25 0.75]", got)
	}
}

func TestRootMotionResetOnStateSwitch(t *testing.T) {
	sm := task.NewAnimStateMachine()
	sm.AddRootMotion(1, 0)
	sm.AddRootMotion(1, 0)
	if got := sm.ConsumeRootMotion(); !near(got[0], 2, 1e-9) {
		t.Fatalf("accumulated root motion x = %v, want 2", got[0])
	}
	if err := sm.AddState("idle", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddState("run", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.SetState("idle"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if err := sm.SetState("run"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if got := sm.ConsumeRootMotion(); got[0] != 0 || got[1] != 0 {
		t.Fatalf("root motion not reset on state switch: %v", got)
	}
}

func TestColliderFollowsTransition(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.SetCollider("idle", 0.5, 1.0); err != nil {
		t.Fatalf("SetCollider(idle) returned error: %v", err)
	}
	if err := sm.SetCollider("run", 0.7, 2.0); err != nil {
		t.Fatalf("SetCollider(run) returned error: %v", err)
	}
	if err := sm.AddState("idle", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddState("run", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddTransition("idle", "run", "go", 1.0); err != nil {
		t.Fatalf("AddTransition returned error: %v", err)
	}
	if err := sm.SetState("idle"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if err := sm.Fire("go"); err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}
	w, h := sm.ColliderAt(0.5)
	if !near(w, 0.6, 1e-6) || !near(h, 1.5, 1e-6) {
		t.Fatalf("ColliderAt(0.5) = (%v, %v), want (0.6, 1.5)", w, h)
	}
}

func TestRestartPreservesState(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.AddState("attack", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.SetState("attack"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	if err := sm.Restart(); err != nil {
		t.Fatalf("Restart returned error: %v", err)
	}
	if got := sm.CurrentState(); got != "attack" {
		t.Fatalf("CurrentState after Restart = %q, want attack", got)
	}
}

func TestConcurrentTriggerSingleTransition(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.AddState("idle", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddState("run", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddTransition("idle", "run", "go", 0.2); err != nil {
		t.Fatalf("AddTransition returned error: %v", err)
	}
	if err := sm.SetState("idle"); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sm.Fire("go")
		}()
	}
	wg.Wait()
	if got := sm.CurrentState(); got != "run" {
		t.Fatalf("CurrentState after 50 concurrent triggers = %q, want run", got)
	}
	if got := sm.TransitionCount(); got != 1 {
		t.Fatalf("TransitionCount = %d, want exactly 1", got)
	}
}

func TestEmitOnceDeduplicates(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if !sm.EmitOnce("step-1", "footstep") {
		t.Fatal("first emission of an event was suppressed")
	}
	if sm.EmitOnce("step-1", "footstep") {
		t.Fatal("the same event key was emitted twice")
	}
}

func TestAddTransitionUnknownStateRejected(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.AddState("idle", 1.0); err != nil {
		t.Fatalf("AddState returned error: %v", err)
	}
	if err := sm.AddTransition("idle", "ghost", "go", 0.2); err == nil {
		t.Fatal("AddTransition accepted an unknown target state")
	}
}

func TestDrainEventsClearsQueue(t *testing.T) {
	sm := task.NewAnimStateMachine()
	if err := sm.AddEvent(0.1, "a"); err != nil {
		t.Fatalf("AddEvent returned error: %v", err)
	}
	if err := sm.AddEvent(0.2, "b"); err != nil {
		t.Fatalf("AddEvent returned error: %v", err)
	}
	first := sm.DrainEvents()
	if len(first) != 2 {
		t.Fatalf("first DrainEvents returned %d events, want 2", len(first))
	}
	if second := sm.DrainEvents(); len(second) != 0 {
		t.Fatalf("second DrainEvents returned %v, want empty", second)
	}
}
