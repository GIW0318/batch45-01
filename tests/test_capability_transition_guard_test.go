package task

import (
	"sync"
	"testing"

	. "gsb/batch45-safety-b-09"
)

func newCapGuard() *CapabilityGuard {
	return NewCapabilityGuard(GuardOptions{
		AllowedFileCapPaths: []string{"/usr/bin/ping", "/usr/bin/traceroute"},
		KernelCapVersion:    3,
		SupportedCapVersion: 3,
	})
}

func allSets(c Cap) CapSet {
	return CapSet{Bounding: c, Permitted: c, Effective: c, Inheritable: c, Ambient: c}
}

func TestDroppingBoundingClearsPermitted(t *testing.T) {
	g := newCapGuard()
	in := CapSet{
		Bounding:  CapSysAdmin | CapChown,
		Permitted: CapSysAdmin | CapChown,
		Effective: CapSysAdmin | CapChown,
	}
	out, err := g.DropBounding(in, CapSysAdmin)
	if err != nil {
		t.Fatalf("DropBounding returned error: %v", err)
	}
	if out.Bounding&CapSysAdmin != 0 {
		t.Fatalf("capability still in bounding set: %#x", out.Bounding)
	}
	if out.Permitted&CapSysAdmin != 0 {
		t.Fatalf("capability still in permitted set after dropping bounding: %#x", out.Permitted)
	}
	if out.Effective&CapSysAdmin != 0 {
		t.Fatalf("capability still in effective set after dropping bounding: %#x", out.Effective)
	}
	if out.Permitted&CapChown == 0 {
		t.Fatalf("unrelated capability was dropped: %#x", out.Permitted)
	}
}

func TestDropRemovesFromAllSets(t *testing.T) {
	g := newCapGuard()
	out, err := g.Drop(allSets(CapDACOverride|CapChown), CapDACOverride)
	if err != nil {
		t.Fatalf("Drop returned error: %v", err)
	}
	for name, got := range map[string]Cap{
		"bounding":    out.Bounding,
		"permitted":   out.Permitted,
		"effective":   out.Effective,
		"inheritable": out.Inheritable,
		"ambient":     out.Ambient,
	} {
		if got&CapDACOverride != 0 {
			t.Fatalf("%s set still contains CapDACOverride: %#x", name, got)
		}
		if got&CapChown == 0 {
			t.Fatalf("%s set lost an unrelated capability: %#x", name, got)
		}
	}
}

func TestSetuidExecClearsAmbient(t *testing.T) {
	g := newCapGuard()
	set := CapSet{Permitted: CapSetUID | CapChown, Effective: CapSetUID | CapChown, Ambient: CapChown}
	out, plan, err := g.PlanExec(set, ExecPlan{Binary: "/usr/bin/su", SetUID: true, SetGID: true})
	if err != nil {
		t.Fatalf("PlanExec returned error: %v", err)
	}
	if !plan.AmbientClear {
		t.Fatalf("exec plan does not clear the ambient set for a setuid binary")
	}
	if out.Ambient != 0 {
		t.Fatalf("ambient set survived a setuid exec: %#x", out.Ambient)
	}
}

func TestExecZeroesEffective(t *testing.T) {
	g := newCapGuard()
	set := CapSet{Permitted: CapChown, Effective: CapChown, Bounding: CapChown}
	out, plan, err := g.PlanExec(set, ExecPlan{Binary: "/bin/sh"})
	if err != nil {
		t.Fatalf("PlanExec returned error: %v", err)
	}
	if !plan.ZeroEffective {
		t.Fatalf("exec plan does not zero the effective set")
	}
	if out.Effective != 0 {
		t.Fatalf("effective set after exec = %#x, want 0", out.Effective)
	}
}

func TestNoNewPrivsDisablesFileCaps(t *testing.T) {
	g := newCapGuard()
	if err := g.ApplyFileCap(FileCap{Path: "/usr/bin/ping", Caps: CapNetBindService}); err != nil {
		t.Fatalf("allowed file capability rejected before no_new_privs: %v", err)
	}
	if err := g.EnableNoNewPrivs(); err != nil {
		t.Fatalf("EnableNoNewPrivs returned error: %v", err)
	}
	if err := g.ApplyFileCap(FileCap{Path: "/usr/bin/ping", Caps: CapNetBindService}); err == nil {
		t.Fatalf("file capability was applied although no_new_privs is active")
	}
}

func TestInheritableDoesNotLeak(t *testing.T) {
	g := newCapGuard()
	set := CapSet{Bounding: CapChown, Permitted: CapChown, Inheritable: CapChown}
	out, _, err := g.PlanExec(set, ExecPlan{Binary: "/bin/sh"})
	if err != nil {
		t.Fatalf("PlanExec returned error: %v", err)
	}
	if out.Inheritable != 0 {
		t.Fatalf("inheritable set leaked to the child: %#x", out.Inheritable)
	}
}

func TestCapVersionMismatchRejected(t *testing.T) {
	g := newCapGuard()
	if err := g.CheckVersion(3); err != nil {
		t.Fatalf("matching version rejected: %v", err)
	}
	if err := g.CheckVersion(2); err == nil {
		t.Fatalf("kernel capability version mismatch was silently accepted")
	}
}

func TestDropOfMissingCapReturnsError(t *testing.T) {
	g := newCapGuard()
	if _, err := g.Drop(CapSet{Bounding: CapChown, Permitted: CapChown, Effective: CapChown}, CapKill); err == nil {
		t.Fatalf("dropping a capability that is not held must report an error")
	}
	if _, err := g.Drop(CapSet{Bounding: CapKill, Permitted: CapKill, Effective: CapKill}, CapKill); err != nil {
		t.Fatalf("dropping a held capability rejected: %v", err)
	}
}

func TestUIDChangeRecomputesPermitted(t *testing.T) {
	g := newCapGuard()
	base := CapSet{
		Bounding:  CapChown,
		Permitted: CapChown | CapSysAdmin,
		Effective: CapChown | CapSysAdmin,
	}
	if err := g.SetCurrent(base); err != nil {
		t.Fatalf("SetCurrent returned error: %v", err)
	}
	out, err := g.SetUIDTo(1000)
	if err != nil {
		t.Fatalf("SetUIDTo returned error: %v", err)
	}
	if out.Permitted != CapChown {
		t.Fatalf("permitted after uid change = %#x, want %#x recomputed from the bounding set", out.Permitted, CapChown)
	}
	if out.Permitted & ^base.Bounding != 0 {
		t.Fatalf("permitted set grew beyond the bounding set: %#x", out.Permitted)
	}
}

func TestConcurrentPlanExecKeepsState(t *testing.T) {
	g := newCapGuard()
	base := CapSet{Bounding: CapChown | CapKill, Permitted: CapChown, Effective: CapChown}
	if err := g.SetCurrent(base); err != nil {
		t.Fatalf("SetCurrent returned error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, _, err := g.PlanExec(base, ExecPlan{Binary: "/bin/sh", SetUID: true})
			if err != nil {
				t.Errorf("PlanExec returned error: %v", err)
				return
			}
			if out.Permitted & ^base.Bounding != 0 {
				t.Errorf("plan produced capabilities outside the bounding set: %#x", out.Permitted)
			}
		}()
	}
	wg.Wait()
	if got := g.Current(); got != base {
		t.Fatalf("Current() = %+v after concurrent plans, want the recorded baseline %+v", got, base)
	}
}

func TestFileCapPathAllowlist(t *testing.T) {
	g := newCapGuard()
	if err := g.ApplyFileCap(FileCap{Path: "/usr/bin/ping", Caps: CapNetBindService}); err != nil {
		t.Fatalf("allow-listed path rejected: %v", err)
	}
	if err := g.ApplyFileCap(FileCap{Path: "/tmp/evil", Caps: CapSysAdmin}); err == nil {
		t.Fatalf("file capability outside the allow list was applied")
	}
}

func TestUpstreamCannotAddCaps(t *testing.T) {
	g := newCapGuard()
	base := CapSet{Bounding: CapChown, Permitted: CapChown, Effective: CapChown}
	if err := g.SetCurrent(base); err != nil {
		t.Fatalf("SetCurrent returned error: %v", err)
	}
	upstream := CapSet{
		Bounding:  CapSysAdmin | CapChown,
		Permitted: CapSysAdmin | CapChown,
		Effective: CapSysAdmin | CapChown,
	}
	out, err := g.ApplyUpstreamCaps(upstream)
	if err != nil {
		t.Fatalf("ApplyUpstreamCaps returned error: %v", err)
	}
	if out.Permitted&CapSysAdmin != 0 {
		t.Fatalf("upstream added a capability the baseline never had: %#x", out.Permitted)
	}
	if out.Permitted != CapChown {
		t.Fatalf("permitted = %#x, want %#x", out.Permitted, CapChown)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	g := newCapGuard()
	base := CapSet{Bounding: CapChown, Permitted: CapChown, Effective: CapChown, Ambient: CapKill}
	if err := g.SetCurrent(base); err != nil {
		t.Fatalf("SetCurrent returned error: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	restored := newCapGuard()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if got := restored.Current(); got != base {
		t.Fatalf("restored state = %+v, want %+v", got, base)
	}
}

func TestDropAllExceptKeepsOnlyListed(t *testing.T) {
	g := newCapGuard()
	if err := g.SetCurrent(allSets(CapChown | CapKill | CapSysAdmin)); err != nil {
		t.Fatalf("SetCurrent returned error: %v", err)
	}
	out, err := g.DropAllExcept(CapNetBindService)
	if err != nil {
		t.Fatalf("DropAllExcept returned error: %v", err)
	}
	if out.Bounding != CapNetBindService || out.Permitted != CapNetBindService || out.Effective != CapNetBindService {
		t.Fatalf("sets not narrowed to the kept capability: %+v", out)
	}
	if out.Inheritable != 0 || out.Ambient != 0 {
		t.Fatalf("inheritable or ambient leaked: %+v", out)
	}
}

func TestCurrentTracksLastSet(t *testing.T) {
	g := newCapGuard()
	if got := g.Current(); got != (CapSet{}) {
		t.Fatalf("initial Current() = %+v, want the zero value", got)
	}
	base := CapSet{Bounding: CapKill, Permitted: CapKill, Effective: CapKill}
	if err := g.SetCurrent(base); err != nil {
		t.Fatalf("SetCurrent returned error: %v", err)
	}
	if got := g.Current(); got != base {
		t.Fatalf("Current() = %+v, want %+v", got, base)
	}
	invalid := CapSet{Bounding: CapChown, Permitted: CapSysAdmin}
	if err := g.SetCurrent(invalid); err == nil {
		t.Fatalf("state whose permitted set exceeds the bounding set was accepted")
	}
	if got := g.Current(); got != base {
		t.Fatalf("Current() changed after a rejected update: %+v", got)
	}
}
