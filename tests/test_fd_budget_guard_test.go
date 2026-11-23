package task

import (
	"context"
	"errors"
	"sync"
	"testing"

	task "gsb/batch45-safety-b-24"
)

func guardWith(t *testing.T, budget task.Budget) *task.FdBudgetGuard {
	t.Helper()
	g := task.NewFdBudgetGuard()
	if err := g.SetBudget("tenant-a", budget); err != nil {
		t.Fatalf("SetBudget returned unexpected error: %v", err)
	}
	if err := g.SetBudget("tenant-b", budget); err != nil {
		t.Fatalf("SetBudget returned unexpected error: %v", err)
	}
	return g
}

func TestQuotaIsPerTenantNotPerProcess(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 2, Hard: 10})
	for i := 0; i < 2; i++ {
		if _, err := g.Acquire("tenant-a", "file", "/data/a"); err != nil {
			t.Fatalf("Acquire %d returned unexpected error: %v", i, err)
		}
	}
	if _, err := g.Acquire("tenant-a", "file", "/data/a"); !errors.Is(err, task.ErrSoftLimitReached) {
		t.Fatalf("tenant-a must be capped at its own soft limit, got %v", err)
	}
	if _, err := g.Acquire("tenant-b", "file", "/data/b"); err != nil {
		t.Fatalf("tenant-b budget must be independent of tenant-a, got %v", err)
	}
}

func TestSoftLimitRejectsNewTasks(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 1, Hard: 10})
	if _, err := g.Acquire("tenant-a", "file", "/data/a"); err != nil {
		t.Fatalf("Acquire returned unexpected error: %v", err)
	}
	if _, err := g.Acquire("tenant-a", "file", "/data/b"); !errors.Is(err, task.ErrSoftLimitReached) {
		t.Fatalf("the soft limit must reject new work, got %v", err)
	}
}

func TestHardLimitNeverExceeded(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 10, Hard: 2})
	for i := 0; i < 2; i++ {
		if _, err := g.Acquire("tenant-a", "file", "/data/a"); err != nil {
			t.Fatalf("Acquire %d returned unexpected error: %v", i, err)
		}
	}
	if _, err := g.Acquire("tenant-a", "file", "/data/a"); !errors.Is(err, task.ErrHardLimitReached) {
		t.Fatalf("the hard limit must not be exceeded, got %v", err)
	}
}

func TestReleaseRestoresBudget(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 1, Hard: 2})
	tk, err := g.Acquire("tenant-a", "file", "/data/a")
	if err != nil {
		t.Fatalf("Acquire returned unexpected error: %v", err)
	}
	if err := g.Release(tk); err != nil {
		t.Fatalf("Release returned unexpected error: %v", err)
	}
	used, err := g.Usage("tenant-a")
	if err != nil {
		t.Fatalf("Usage returned unexpected error: %v", err)
	}
	if used != 0 {
		t.Fatalf("released fd must return to the budget, got %d", used)
	}
	if _, err := g.Acquire("tenant-a", "file", "/data/a"); err != nil {
		t.Fatalf("budget must be reusable after release, got %v", err)
	}
}

func TestErrorPathReleasesFds(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 4, Hard: 8})
	boom := errors.New("boom")
	err := g.RunTask(context.Background(), "tenant-a", func(ctx context.Context) error {
		if _, err := g.Acquire("tenant-a", "file", "/data/x"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("RunTask must return the worker error, got %v", err)
	}
	used, err := g.Usage("tenant-a")
	if err != nil {
		t.Fatalf("Usage returned unexpected error: %v", err)
	}
	if used != 0 {
		t.Fatalf("an error path must still close every fd, got %d", used)
	}
}

func TestCancelReleasesFds(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 4, Hard: 8})
	ctx, cancel := context.WithCancel(context.Background())
	err := g.RunTask(ctx, "tenant-a", func(ctx context.Context) error {
		if _, err := g.Acquire("tenant-a", "file", "/data/x"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if err == nil {
		t.Fatalf("a cancelled task must report an error")
	}
	used, uerr := g.Usage("tenant-a")
	if uerr != nil {
		t.Fatalf("Usage returned unexpected error: %v", uerr)
	}
	if used != 0 {
		t.Fatalf("cancel must not leave background readers holding fds, got %d", used)
	}
}

func TestIdlePoolCapped(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 100, Hard: 200})
	if err := g.ConfigurePool(task.PoolConfig{MaxIdle: 2, MaxOpen: 10}); err != nil {
		t.Fatalf("ConfigurePool returned unexpected error: %v", err)
	}
	for i := 0; i < 5; i++ {
		tk, err := g.OpenConn("tenant-a")
		if err != nil {
			t.Fatalf("OpenConn %d returned unexpected error: %v", i, err)
		}
		if err := g.CloseConn(tk, true); err != nil {
			t.Fatalf("CloseConn returned unexpected error: %v", err)
		}
	}
	snap, err := g.PoolStats("tenant-a")
	if err != nil {
		t.Fatalf("PoolStats returned unexpected error: %v", err)
	}
	if snap.Open != 2 || snap.Idle != 2 {
		t.Fatalf("idle connections must be capped at MaxIdle, got %+v", snap)
	}
}

func TestLogHandleReused(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 10, Hard: 20})
	first, err := g.OpenLog("tenant-a", "/var/log/ingest.log")
	if err != nil {
		t.Fatalf("OpenLog returned unexpected error: %v", err)
	}
	if _, err := g.OpenLog("tenant-a", "/var/log/ingest.log"); err != nil {
		t.Fatalf("OpenLog returned unexpected error: %v", err)
	}
	open, err := g.OpenCount("tenant-a", "/var/log/ingest.log")
	if err != nil {
		t.Fatalf("OpenCount returned unexpected error: %v", err)
	}
	if open != 1 {
		t.Fatalf("the log file must be opened once and reused, got %d handles", open)
	}
	used, err := g.Usage("tenant-a")
	if err != nil {
		t.Fatalf("Usage returned unexpected error: %v", err)
	}
	if used != 2 {
		t.Fatalf("two logical log users must be refcounted, got %d", used)
	}
	if first.Path != "/var/log/ingest.log" {
		t.Fatalf("ticket must reference the log path, got %q", first.Path)
	}
}

func TestTempUnlinkReleasesHandle(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 10, Hard: 20})
	tk, err := g.OpenTemp("tenant-a", "/tmp/upload.part")
	if err != nil {
		t.Fatalf("OpenTemp returned unexpected error: %v", err)
	}
	if err := g.UnlinkTemp(tk); err != nil {
		t.Fatalf("UnlinkTemp returned unexpected error: %v", err)
	}
	used, err := g.Usage("tenant-a")
	if err != nil {
		t.Fatalf("Usage returned unexpected error: %v", err)
	}
	if used != 0 {
		t.Fatalf("unlinking a temp file must close its handle, got %d", used)
	}
}

func TestConcurrentSharedOpenDeduped(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 100, Hard: 200})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.OpenShared("tenant-a", "/data/shared.bin"); err != nil {
				t.Errorf("OpenShared returned unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	open, err := g.OpenCount("tenant-a", "/data/shared.bin")
	if err != nil {
		t.Fatalf("OpenCount returned unexpected error: %v", err)
	}
	if open != 1 {
		t.Fatalf("concurrent readers must share one handle, got %d", open)
	}
}

func TestLeakReportIsPerTenant(t *testing.T) {
	g := task.NewFdBudgetGuard()
	for _, tID := range []task.TenantID{"tenant-a", "tenant-b"} {
		if err := g.SetBudget(tID, task.Budget{Soft: 10, Hard: 20}); err != nil {
			t.Fatalf("SetBudget returned unexpected error: %v", err)
		}
	}
	quiet, err := g.LeakReport([]task.ProcFdInfo{
		{Tenant: "tenant-a", PID: 1, Open: 6},
		{Tenant: "tenant-b", PID: 2, Open: 6},
	})
	if err != nil {
		t.Fatalf("LeakReport returned unexpected error: %v", err)
	}
	if len(quiet) != 0 {
		t.Fatalf("the total exceeds the per tenant soft limit only when summed, got %+v", quiet)
	}
	leaks, err := g.LeakReport([]task.ProcFdInfo{
		{Tenant: "tenant-a", PID: 1, Open: 11},
		{Tenant: "tenant-b", PID: 2, Open: 6},
	})
	if err != nil {
		t.Fatalf("LeakReport returned unexpected error: %v", err)
	}
	if len(leaks) != 1 || leaks[0].Tenant != "tenant-a" {
		t.Fatalf("only tenant-a is over its own budget, got %+v", leaks)
	}
}

func TestChildInheritsNoFds(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 10, Hard: 20})
	for i := 0; i < 3; i++ {
		if _, err := g.Acquire("tenant-a", "file", "/data/payload"); err != nil {
			t.Fatalf("Acquire returned unexpected error: %v", err)
		}
	}
	plan, err := g.Spawn("tenant-a", []string{"/usr/bin/ffmpeg", "-i", "in.mp4"}, nil)
	if err != nil {
		t.Fatalf("Spawn returned unexpected error: %v", err)
	}
	if len(plan.FDs) != 0 {
		t.Fatalf("a child must not inherit the parent fds, got %+v", plan.FDs)
	}
	if len(plan.Argv) != 3 {
		t.Fatalf("spawn plan must carry argv, got %+v", plan.Argv)
	}
}

func TestWalkClosesDirectories(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 10, Hard: 20})
	tree := map[string][]string{
		"/root":   {"a", "b"},
		"/root/a": {"c"},
	}
	files, err := g.Walk("tenant-a", "/root", tree)
	if err != nil {
		t.Fatalf("Walk returned unexpected error: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("walk must report all files, got %+v", files)
	}
	used, err := g.Usage("tenant-a")
	if err != nil {
		t.Fatalf("Usage returned unexpected error: %v", err)
	}
	if used != 0 {
		t.Fatalf("directory handles must be closed after the walk, got %d", used)
	}
}

func TestRestartRecoveryClosesStaleHandles(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 10, Hard: 20})
	closed, err := g.RecoverAfterRestart([]task.Ticket{
		{ID: 1, Tenant: "tenant-a", Path: "/data/a"},
		{ID: 2, Tenant: "tenant-a", Path: "/data/b"},
		{ID: 3, Tenant: "tenant-b", Path: "/data/c"},
	})
	if err != nil {
		t.Fatalf("RecoverAfterRestart returned unexpected error: %v", err)
	}
	if closed != 3 {
		t.Fatalf("all stale handles must be cleaned after a restart, got %d", closed)
	}
	used, err := g.Usage("tenant-a")
	if err != nil {
		t.Fatalf("Usage returned unexpected error: %v", err)
	}
	if used != 0 {
		t.Fatalf("recovered tenant must be back at zero, got %d", used)
	}
}

func TestUsageTrackedPerTenant(t *testing.T) {
	g := guardWith(t, task.Budget{Soft: 10, Hard: 20})
	for i := 0; i < 2; i++ {
		if _, err := g.Acquire("tenant-a", "file", "/data/a"); err != nil {
			t.Fatalf("Acquire returned unexpected error: %v", err)
		}
	}
	if _, err := g.Acquire("tenant-b", "file", "/data/b"); err != nil {
		t.Fatalf("Acquire returned unexpected error: %v", err)
	}
	a, err := g.Usage("tenant-a")
	if err != nil {
		t.Fatalf("Usage(tenant-a) returned unexpected error: %v", err)
	}
	b, err := g.Usage("tenant-b")
	if err != nil {
		t.Fatalf("Usage(tenant-b) returned unexpected error: %v", err)
	}
	if a != 2 || b != 1 {
		t.Fatalf("usage must be accounted per tenant, got a=%d b=%d", a, b)
	}
}
