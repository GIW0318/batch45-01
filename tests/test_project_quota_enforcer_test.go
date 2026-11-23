package task

import (
	"sync"
	"testing"
	"time"

	task "gsb/batch45-safety-b-22"
)

func mustProject(t *testing.T, e *task.ProjectQuotaEnforcer, desc task.ProjectDesc) {
	t.Helper()
	if err := e.AddProject(desc); err != nil {
		t.Fatalf("AddProject(%d) returned unexpected error: %v", desc.ID, err)
	}
}

func TestChargeSharedAcrossUsersOfSameProject(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1000, Inodes: 100}})

	first, err := e.Charge(1, "alice", task.Usage{Bytes: 600, Inodes: 1})
	if err != nil {
		t.Fatalf("Charge(alice) returned unexpected error: %v", err)
	}
	if !first.Allowed {
		t.Fatalf("first charge must fit, got %+v", first)
	}
	second, err := e.Charge(1, "bob", task.Usage{Bytes: 600, Inodes: 1})
	if err != nil {
		t.Fatalf("Charge(bob) returned unexpected error: %v", err)
	}
	if second.Allowed {
		t.Fatalf("quota is per project, so bob must not get alice's accounting, got %+v", second)
	}
	used, err := e.UsageOf(1)
	if err != nil {
		t.Fatalf("UsageOf returned unexpected error: %v", err)
	}
	if used.Bytes != 600 {
		t.Fatalf("accounted bytes must stay at 600, got %d", used.Bytes)
	}
}

func TestChargeRejectsByteOverLimit(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 100, Inodes: 100}})

	res, err := e.Charge(1, "alice", task.Usage{Bytes: 101, Inodes: 1})
	if err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatalf("a charge above the byte hard limit must be rejected, got %+v", res)
	}
	if res.Reason != task.ReasonQuotaExceeded {
		t.Fatalf("expected reason %q, got %q", task.ReasonQuotaExceeded, res.Reason)
	}
}

func TestChargeRejectsInodeOverLimit(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1 << 40, Inodes: 10}})

	res, err := e.Charge(1, "alice", task.Usage{Bytes: 4096, Inodes: 11})
	if err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatalf("a charge above the inode hard limit must be rejected even when bytes fit, got %+v", res)
	}
}

func TestConcurrentChargesDoNotOversell(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1000, Inodes: 1000}})

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.Charge(1, "worker", task.Usage{Bytes: 100, Inodes: 1})
			if err != nil {
				t.Errorf("Charge returned unexpected error: %v", err)
				return
			}
			if res.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 10 {
		t.Fatalf("exactly 10 of 20 charges of 100 fit into 1000 bytes, got %d", allowed)
	}
	used, err := e.UsageOf(1)
	if err != nil {
		t.Fatalf("UsageOf returned unexpected error: %v", err)
	}
	if used.Bytes != 1000 {
		t.Fatalf("accounted bytes must equal the limit, got %d", used.Bytes)
	}
}

func TestGraceStartsAtFirstExceed(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	created := time.Unix(1000, 0)
	firstExceed := created.Add(10 * time.Minute)
	mustProject(t, e, task.ProjectDesc{
		ID:          1,
		Limits:      task.Limits{Bytes: 100, Inodes: 10},
		GracePeriod: time.Hour,
		CreatedAt:   created,
	})

	if _, err := e.ChargeAt(1, "alice", task.Usage{Bytes: 200, Inodes: 1}, firstExceed); err != nil {
		t.Fatalf("ChargeAt returned unexpected error: %v", err)
	}
	deadline, active, err := e.GraceDeadline(1)
	if err != nil {
		t.Fatalf("GraceDeadline returned unexpected error: %v", err)
	}
	if !active {
		t.Fatalf("grace must be active after the first exceed")
	}
	want := firstExceed.Add(time.Hour)
	if !deadline.Equal(want) {
		t.Fatalf("grace must start at the first exceed, want %v got %v", want, deadline)
	}
	if deadline.Equal(created.Add(time.Hour)) {
		t.Fatalf("grace must not be measured from the project creation time")
	}
}

func TestRejectedChargeLeavesUsageUnchanged(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 100, Inodes: 10}})

	if _, err := e.Charge(1, "alice", task.Usage{Bytes: 60, Inodes: 1}); err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	if _, err := e.Charge(1, "alice", task.Usage{Bytes: 60, Inodes: 1}); err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	used, err := e.UsageOf(1)
	if err != nil {
		t.Fatalf("UsageOf returned unexpected error: %v", err)
	}
	if used.Bytes != 60 {
		t.Fatalf("a rejected write must not leave partial usage behind, got %d bytes", used.Bytes)
	}
}

func TestParentLimitNarrowsChild(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 10, Limits: task.Limits{Bytes: 100, Inodes: 10}})
	mustProject(t, e, task.ProjectDesc{ID: 11, Parent: 10, Limits: task.Limits{Bytes: 1000, Inodes: 100}})

	lim, err := e.EffectiveLimit(11)
	if err != nil {
		t.Fatalf("EffectiveLimit returned unexpected error: %v", err)
	}
	if lim != (task.Limits{Bytes: 100, Inodes: 10}) {
		t.Fatalf("child limit must be the minimum with its parent, got %+v", lim)
	}
	res, err := e.Charge(11, "alice", task.Usage{Bytes: 150, Inodes: 1})
	if err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatalf("charge above the narrowed parent limit must be rejected, got %+v", res)
	}
}

func TestReleaseReclaimsUsage(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1000, Inodes: 10}})

	if _, err := e.Charge(1, "alice", task.Usage{Bytes: 800, Inodes: 2}); err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	left, err := e.Release(1, task.Usage{Bytes: 800, Inodes: 2})
	if err != nil {
		t.Fatalf("Release returned unexpected error: %v", err)
	}
	if left.Bytes != 0 || left.Inodes != 0 {
		t.Fatalf("usage must be reclaimed immediately after a delete, got %+v", left)
	}
}

func TestHardlinkCountedOnce(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1 << 20, Inodes: 100}})

	f := task.FileStat{Path: "/data/proj/a", Dev: 1, Ino: 5, Size: 1000, Blocks: 1, BlockSize: 4096, LinkCount: 2}
	if _, err := e.CountFile(1, f); err != nil {
		t.Fatalf("CountFile returned unexpected error: %v", err)
	}
	used, err := e.CountFile(1, f)
	if err != nil {
		t.Fatalf("CountFile returned unexpected error: %v", err)
	}
	if used.Inodes != 1 {
		t.Fatalf("the same hard link must be counted once, got %d inodes", used.Inodes)
	}
	if used.Bytes != 4096 {
		t.Fatalf("hard link bytes must be counted once, got %d", used.Bytes)
	}
}

func TestSymlinkNotCounted(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1 << 20, Inodes: 100}})

	used, err := e.CountFile(1, task.FileStat{
		Path:      "/data/proj/link",
		Dev:       1,
		Ino:       9,
		Size:      255,
		Blocks:    1,
		BlockSize: 4096,
		LinkCount: 1,
		Symlink:   true,
	})
	if err != nil {
		t.Fatalf("CountFile returned unexpected error: %v", err)
	}
	if used.Bytes != 0 || used.Inodes != 0 {
		t.Fatalf("symlinks must not be counted twice, got %+v", used)
	}
}

func TestRenameMovesCharge(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1000, Inodes: 10}})
	mustProject(t, e, task.ProjectDesc{ID: 2, Limits: task.Limits{Bytes: 1000, Inodes: 10}})

	if _, err := e.Charge(1, "alice", task.Usage{Bytes: 50, Inodes: 1}); err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	if err := e.Rename(1, 2, task.Usage{Bytes: 50, Inodes: 1}); err != nil {
		t.Fatalf("Rename returned unexpected error: %v", err)
	}
	from, err := e.UsageOf(1)
	if err != nil {
		t.Fatalf("UsageOf(1) returned unexpected error: %v", err)
	}
	to, err := e.UsageOf(2)
	if err != nil {
		t.Fatalf("UsageOf(2) returned unexpected error: %v", err)
	}
	if from.Bytes != 0 || to.Bytes != 50 {
		t.Fatalf("rename must move the charge, got from=%+v to=%+v", from, to)
	}
}

func TestTruncateReducesUsage(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1000, Inodes: 10}})

	if _, err := e.Charge(1, "alice", task.Usage{Bytes: 100, Inodes: 1}); err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	used, err := e.Truncate(1, 100, 40)
	if err != nil {
		t.Fatalf("Truncate returned unexpected error: %v", err)
	}
	if used.Bytes != 40 {
		t.Fatalf("truncate must re-account the file, got %d bytes", used.Bytes)
	}
}

func TestSparseFileUsesAllocatedBlocks(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1 << 40, Inodes: 10}})

	used, err := e.CountFile(1, task.FileStat{
		Path:      "/data/proj/sparse.img",
		Dev:       1,
		Ino:       21,
		Size:      1 << 30,
		Blocks:    8,
		BlockSize: 4096,
		LinkCount: 1,
	})
	if err != nil {
		t.Fatalf("CountFile returned unexpected error: %v", err)
	}
	if used.Bytes != 32768 {
		t.Fatalf("sparse files must be charged by allocated blocks, got %d", used.Bytes)
	}
}

func TestUpdateLimitsTakesEffectOnExistingProject(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 1, Limits: task.Limits{Bytes: 1000, Inodes: 10}})

	if _, err := e.Charge(1, "alice", task.Usage{Bytes: 500, Inodes: 1}); err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	if err := e.UpdateLimits(1, task.Limits{Bytes: 200, Inodes: 10}); err != nil {
		t.Fatalf("UpdateLimits returned unexpected error: %v", err)
	}
	res, err := e.Charge(1, "alice", task.Usage{Bytes: 1, Inodes: 1})
	if err != nil {
		t.Fatalf("Charge returned unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatalf("a lowered limit must apply to already open handles, got %+v", res)
	}
}

func TestSnapshotDirAttributedToProject(t *testing.T) {
	e := task.NewProjectQuotaEnforcer()
	mustProject(t, e, task.ProjectDesc{ID: 7, Root: "/data/projA", Limits: task.Limits{Bytes: 1000, Inodes: 10}})
	mustProject(t, e, task.ProjectDesc{ID: 8, Root: "/data/projB", Limits: task.Limits{Bytes: 1000, Inodes: 10}})

	got, err := e.AttributePath("/data/projA/.snapshots/2026-01-01/db.tar")
	if err != nil {
		t.Fatalf("AttributePath returned unexpected error: %v", err)
	}
	if got != 7 {
		t.Fatalf("snapshot files must be charged to their owning project 7, got %d", got)
	}
	backup, err := e.AttributePath("/data/projB/.backup/db.tar")
	if err != nil {
		t.Fatalf("AttributePath returned unexpected error: %v", err)
	}
	if backup != 8 {
		t.Fatalf("backup files must be charged to their owning project 8, got %d", backup)
	}
}
