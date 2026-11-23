package task

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	task "gsb/batch45-safety-c-01"
)

// TestStateLimitHardCap verifies per-connection state is memory bounded: once
// the cap is reached new streams and priority nodes are rejected immediately
// (no queuing), while RST reclamation frees a slot right away.
func TestStateLimitHardCap(t *testing.T) {
	g := task.NewStreamGuard(task.GuardConfig{
		MaxConcurrentStreams: 1 << 20,
		MaxHeaderListSize:    4096,
		InitialStreamWindow:  65535,
		StateLimit:           4,
	})

	opened := 0
	for id := uint32(1); id <= 9; id += 2 {
		if _, err := g.HandleHeaders(id, 10, true); err != nil {
			if !errors.Is(err, task.ErrGuardStateLimit) {
				t.Fatalf("expected ErrGuardStateLimit, got %v", err)
			}
			break
		}
		opened++
	}
	if opened != 4 {
		t.Fatalf("state cap must bound live streams at 4, opened %d", opened)
	}
	if g.OpenStreams() != 4 {
		t.Fatalf("OpenStreams = %d, want 4", g.OpenStreams())
	}

	// Priority nodes share the same bounded budget.
	err := g.HandlePriority(task.PriorityFrame{StreamID: 101, DependsOn: 0})
	if !errors.Is(err, task.ErrGuardStateLimit) {
		t.Fatalf("priority state must share the cap, got %v", err)
	}

	// Immediate reclamation makes room again.
	if err := g.HandleRstStream(1); err != nil {
		t.Fatalf("HandleRstStream: %v", err)
	}
	if g.OpenStreams() != 3 {
		t.Fatalf("OpenStreams after RST = %d, want 3", g.OpenStreams())
	}
	if _, err := g.HandleHeaders(11, 10, true); err != nil {
		t.Fatalf("a freed slot must accept a new stream immediately, got %v", err)
	}
}

// TestConcurrentFrameAccounting hammers the guard from many goroutines with
// interleaved HEADERS/DATA/WINDOW_UPDATE/RST/SETTINGS and asserts that the
// concurrency cap, accounting and lifecycle invariants never drift. Run under
// -race in addition to the normal run.
func TestConcurrentFrameAccounting(t *testing.T) {
	const maxConcurrent = 32
	const goroutines = 16
	const rounds = 1000

	g := task.NewStreamGuard(task.GuardConfig{
		MaxConcurrentStreams: maxConcurrent,
		MaxHeaderListSize:    4096,
		InitialStreamWindow:  65535,
		StateLimit:           1 << 15,
	})

	var nextID uint32 = 1
	var wg sync.WaitGroup
	var rejected uint64

	for r := 0; r < goroutines; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]uint32, 0, 16)
			for i := 0; i < rounds; i++ {
				id := atomic.AddUint32(&nextID, 2)
				_, err := g.HandleHeaders(id, 10, true)
				switch {
				case err == nil:
					local = append(local, id)
					if _, derr := g.HandleData(id, 8); derr != nil {
						t.Errorf("HandleData on fresh stream %d: %v", id, derr)
						return
					}
				case errors.Is(err, task.ErrMaxConcurrentStreams),
					errors.Is(err, task.ErrGuardStateLimit),
					errors.Is(err, task.ErrStreamIDWrapped):
					atomic.AddUint64(&rejected, 1)
				default:
					t.Errorf("unexpected HandleHeaders error: %v", err)
					return
				}

				if len(local) > 0 && i%3 == 0 {
					victim := local[0]
					local = local[1:]
					if err := g.HandleRstStream(victim); err != nil {
						t.Errorf("HandleRstStream %d: %v", victim, err)
						return
					}
					if _, err := g.HandleData(victim, 1); !errors.Is(err, task.ErrStreamClosed) {
						t.Errorf("DATA after RST must be ErrStreamClosed, got %v", err)
						return
					}
				}
			}
			for _, id := range local {
				_ = g.HandleRstStream(id)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = g.HandleSettings(task.SettingsValues{
				MaxConcurrentStreams: maxConcurrent,
				InitialWindow:        65535,
			})
			_ = g.HandleWindowUpdate(1, 100)
		}
	}()

	wg.Wait()

	if g.OpenStreams() < 0 || g.OpenStreams() > maxConcurrent {
		t.Fatalf("OpenStreams out of bounds: %d", g.OpenStreams())
	}
	if rejected == 0 {
		t.Fatalf("expected backpressure under concurrent load, none observed")
	}

	// Every remaining stream is still usable until RST; counts must agree.
	remaining := g.OpenStreams()
	t.Logf("concurrent stress: rejections=%d remainingOpen=%d", rejected, remaining)
	for g.OpenStreams() > 0 {
		// Sweep via observed ids: State lookup confirms lifecycle consistency.
		for id := uint32(1); id < nextID; id += 2 {
			if _, ok := g.State(id); ok {
				if err := g.HandleRstStream(id); err != nil {
					t.Fatalf("cleanup RST %d: %v", id, err)
				}
			}
		}
	}
	if g.OpenStreams() != 0 {
		t.Fatalf("OpenStreams after full sweep = %d, want 0", g.OpenStreams())
	}
}

// TestMigrationClearsStateThenAccounts verifies migration wipes old state first
// and old stream ids can never write again, while new streams work normally.
func TestMigrationClearsStateThenAccounts(t *testing.T) {
	g := task.NewStreamGuard(task.GuardConfig{
		MaxConcurrentStreams: 8,
		MaxHeaderListSize:    4096,
		InitialStreamWindow:  100,
	})

	for _, id := range []uint32{1, 3, 5} {
		if _, err := g.HandleHeaders(id, 10, true); err != nil {
			t.Fatalf("HandleHeaders %d: %v", id, err)
		}
	}
	if err := g.Migrate("10.0.0.9:8443"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if g.OpenStreams() != 0 {
		t.Fatalf("migration must clear old streams, got %d", g.OpenStreams())
	}
	for _, id := range []uint32{1, 3, 5} {
		if _, err := g.HandleData(id, 1); !errors.Is(err, task.ErrConnMigrated) {
			t.Fatalf("old stream %d after migration: %v", id, err)
		}
	}

	// New ids continue monotonically past the pre-migration tombstone.
	if _, err := g.HandleHeaders(7, 10, true); err != nil {
		t.Fatalf("new stream after migration: %v", err)
	}
	if _, err := g.HandleData(7, 20); err != nil {
		t.Fatalf("new stream must be writable: %v", err)
	}
	if g.OpenStreams() != 1 {
		t.Fatalf("OpenStreams = %d, want 1", g.OpenStreams())
	}
}
