package task

import (
	"errors"
	"testing"

	. "gsb/batch45-safety-b-03"
)

func newWarden() *DelegationWarden {
	return NewDelegationWarden(WardenOptions{
		MaxDepth:     2,
		MaxChildren:  2,
		RootTenant:   "root",
		DefaultQuota: 5,
		RootQuota:    50,
	})
}

func mustDelegate(t *testing.T, w *DelegationWarden, tenant, path string) *Delegation {
	t.Helper()
	d, err := w.Delegate(tenant, path, []Controller{CtrlCPU, CtrlMemory})
	if err != nil {
		t.Fatalf("Delegate returned error: %v", err)
	}
	return d
}

func TestDelegateClearsSubtreeControl(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	if !d.SubtreeControlCleared {
		t.Fatalf("delegated directory must have cgroup.subtree_control cleared")
	}
	for _, f := range w.Files(d) {
		if f == "cgroup.subtree_control" {
			t.Fatalf("tenant can still write cgroup.subtree_control")
		}
	}
}

func TestDelegationHidesControllerFiles(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	files := w.Files(d)
	if len(files) == 0 {
		t.Fatalf("delegation exposes no files at all")
	}
	hasProcs := false
	for _, f := range files {
		if f == "cgroup.procs" {
			hasProcs = true
		}
		if f == "cgroup.controllers" || f == "cgroup.subtree_control" {
			t.Fatalf("tenant must not see %s, it can add controllers itself", f)
		}
	}
	if !hasProcs {
		t.Fatalf("delegation must expose cgroup.procs")
	}
}

func TestLimitFilesCoverAllControllers(t *testing.T) {
	w := newWarden()
	l := Limits{
		CPUMax:    "50000 100000",
		CPUSet:    "0-3",
		IOMax:     "8:16 rbps=1048576",
		MemoryMax: 1 << 30,
	}
	files := w.LimitFiles(l)
	want := map[string]string{
		"cpu.max":     "50000 100000",
		"cpuset.cpus": "0-3",
		"io.max":      "8:16 rbps=1048576",
		"memory.max":  "1073741824",
	}
	for name, val := range want {
		got, ok := files[name]
		if !ok {
			t.Fatalf("LimitFiles did not produce %s", name)
		}
		if got != val {
			t.Errorf("%s = %q, want %q", name, got, val)
		}
	}
}

func TestSetLimitsPersistsAllControllers(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	want := Limits{CPUMax: "25000 100000", CPUSet: "2-5", IOMax: "8:0 wbps=2097152", MemoryMax: 2 << 30}
	if err := w.SetLimits(d, want); err != nil {
		t.Fatalf("SetLimits returned error: %v", err)
	}
	got, err := w.LimitsFor(d)
	if err != nil {
		t.Fatalf("LimitsFor returned error: %v", err)
	}
	if got != want {
		t.Fatalf("LimitsFor = %+v, want %+v (cpuset and io must not be dropped)", got, want)
	}
}

func TestMoveProcessRejectsForeignProcess(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	if err := w.MoveProcess(d, 4242, "tenant-a"); err != nil {
		t.Fatalf("MoveProcess for the owning tenant returned error: %v", err)
	}
	if err := w.MoveProcess(d, 4243, "tenant-b"); err == nil {
		t.Fatalf("MoveProcess must reject a process owned by another tenant")
	}
}

func TestMoveProcessInheritsParentLimits(t *testing.T) {
	w := newWarden()
	parent := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	if err := w.SetLimits(parent, Limits{CPUMax: "50000 100000", MemoryMax: 2 << 30}); err != nil {
		t.Fatalf("SetLimits returned error: %v", err)
	}
	child, err := w.CreateChild(parent, "sandbox")
	if err != nil {
		t.Fatalf("CreateChild returned error: %v", err)
	}
	if err := w.MoveProcess(child, 5150, "tenant-a"); err != nil {
		t.Fatalf("MoveProcess returned error: %v", err)
	}
	limits, err := w.LimitsFor(child)
	if err != nil {
		t.Fatalf("LimitsFor returned error: %v", err)
	}
	if limits.MemoryMax != 2<<30 || limits.CPUMax != "50000 100000" {
		t.Fatalf("child limits = %+v, want parent limits inherited (memory 2GiB, cpu 50000 100000)", limits)
	}
}

func TestChildDepthLimitEnforced(t *testing.T) {
	w := newWarden()
	top := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	lvl2, err := w.CreateChild(top, "lvl2")
	if err != nil {
		t.Fatalf("first child must be allowed: %v", err)
	}
	if _, err := w.CreateChild(lvl2, "lvl3"); err == nil {
		t.Fatalf("nesting beyond MaxDepth must be rejected")
	}
}

func TestChildCountLimitEnforced(t *testing.T) {
	w := newWarden()
	top := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	for i := 0; i < 2; i++ {
		if _, err := w.CreateChild(top, "c"); err != nil {
			t.Fatalf("child %d must be allowed: %v", i, err)
		}
	}
	if _, err := w.CreateChild(top, "c"); err == nil {
		t.Fatalf("third child must be rejected, unlimited children exhaust inodes")
	}
}

func TestFreezeAndKillIsOrderedAndAtomic(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	steps, err := w.FreezeAndKill(d)
	if err != nil {
		t.Fatalf("FreezeAndKill returned error: %v", err)
	}
	if len(steps) != 2 || steps[0] != "freeze" || steps[1] != "kill" {
		t.Fatalf("steps = %v, want [freeze kill]", steps)
	}
	other := mustDelegate(t, w, "tenant-b", "/sys/fs/cgroup/tenant-b")
	steps, err = w.FreezeAndKillWith(other, func() error { return errors.New("freezer busy") })
	if err == nil {
		t.Fatalf("FreezeAndKillWith must report freezer failure")
	}
	for _, s := range steps {
		if s == "kill" {
			t.Fatalf("processes were killed although freezing failed: %v", steps)
		}
	}
}

func TestStatsTrackProcessMoves(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	for _, pid := range []int{11, 12, 13} {
		if err := w.MoveProcess(d, pid, "tenant-a"); err != nil {
			t.Fatalf("MoveProcess returned error: %v", err)
		}
	}
	s1, err := w.Stats(d)
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if s1.Pids != 3 {
		t.Fatalf("Stats.Pids = %d, want 3", s1.Pids)
	}
	s2, err := w.Stats(d)
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if s2.Version <= s1.Version {
		t.Fatalf("stats version did not advance (%d -> %d), snapshot may be torn", s1.Version, s2.Version)
	}
	if s2.Pids != 3 {
		t.Fatalf("second Stats.Pids = %d, want 3", s2.Pids)
	}
}

func TestRemoveCancelsTimers(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	if err := w.Remove(d); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if n := w.PendingTimers(); n != 0 {
		t.Fatalf("PendingTimers() = %d after Remove, timers still reference the dead cgroup", n)
	}
}

func TestRemovedDelegationRejectsOperations(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	if err := w.Remove(d); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if err := w.SetLimits(d, Limits{MemoryMax: 1 << 20}); err == nil {
		t.Fatalf("SetLimits on a removed delegation must fail")
	}
	if err := w.MoveProcess(d, 77, "tenant-a"); err == nil {
		t.Fatalf("MoveProcess into a removed delegation must fail")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	if err := w.SetLimits(d, Limits{MemoryMax: 3 << 30, CPUSet: "0-1"}); err != nil {
		t.Fatalf("SetLimits returned error: %v", err)
	}
	data, err := w.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	restored := newWarden()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	d2, ok := restored.Lookup(d.Path)
	if !ok {
		t.Fatalf("delegation %s missing after restore", d.Path)
	}
	limits, err := restored.LimitsFor(d2)
	if err != nil {
		t.Fatalf("LimitsFor returned error: %v", err)
	}
	if limits.MemoryMax != 3<<30 || limits.CPUSet != "0-1" {
		t.Fatalf("restored limits = %+v, want memory 3GiB and cpuset 0-1", limits)
	}
	if len(restored.Files(d2)) == 0 {
		t.Fatalf("restored delegation exposes no files")
	}
}

func TestRootTenantHasSeparateQuota(t *testing.T) {
	w := newWarden()
	if got := w.QuotaFor("root"); got != 50 {
		t.Fatalf("QuotaFor(root) = %d, want 50", got)
	}
	if got := w.QuotaFor("tenant-a"); got != 5 {
		t.Fatalf("QuotaFor(tenant-a) = %d, want 5", got)
	}
}

func TestOOMVictimInsideDelegation(t *testing.T) {
	w := newWarden()
	d := mustDelegate(t, w, "tenant-a", "/sys/fs/cgroup/tenant-a")
	procs := []ProcUsage{
		{PID: 101, TenantID: "tenant-a", MemoryBytes: 2 << 30},
		{PID: 202, TenantID: "tenant-b", MemoryBytes: 8 << 30},
	}
	victim, err := w.OOMVictim(d, procs, 1<<30)
	if err != nil {
		t.Fatalf("OOMVictim returned error: %v", err)
	}
	if victim != 101 {
		t.Fatalf("OOMVictim = %d, want 101 (largest process inside the delegation)", victim)
	}
	if _, err := w.OOMVictim(d, procs, 64<<30); err == nil {
		t.Fatalf("OOMVictim must fail when nothing exceeds the limit")
	}
}
