package task

import (
	"testing"

	. "gsb/batch45-safety-c-13"
)

func TestEvaluateDeniesMmapOnProtectedPath(t *testing.T) {
	g := &LsmFileGuard{}
	rules := []Rule{{Pattern: "/etc/shadow", Verdict: VerdictDeny}}
	got, err := g.Evaluate(FileRef{Path: "/etc/shadow", Inode: 1}, AccessMmap, rules)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("mmap of a protected file must be denied, got %v", got)
	}
}

func TestEvaluateDeniesOpenOnProtectedPath(t *testing.T) {
	g := &LsmFileGuard{}
	rules := []Rule{{Pattern: "/etc/shadow", Verdict: VerdictDeny}}
	got, err := g.Evaluate(FileRef{Path: "/etc/shadow", Inode: 1}, AccessOpen, rules)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("open of a protected file must be denied, got %v", got)
	}
}

func TestEvaluateAllowsUnrelatedPath(t *testing.T) {
	g := &LsmFileGuard{}
	rules := []Rule{{Pattern: "/etc/shadow", Verdict: VerdictDeny}}
	got, err := g.Evaluate(FileRef{Path: "/srv/app/data", Inode: 2}, AccessOpen, rules)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("path outside the rules, want allow, got %v", got)
	}
}

func TestRebindMountNSRebindsAllRules(t *testing.T) {
	g := &LsmFileGuard{}
	rules := []Rule{
		{Pattern: "/etc/*", Verdict: VerdictDeny, MountNS: 1},
		{Pattern: "/srv/*", Verdict: VerdictAllow, MountNS: 1},
	}
	got, err := g.RebindMountNS(rules, 42)
	if err != nil {
		t.Fatalf("RebindMountNS: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rebound rules, got %d", len(got))
	}
	for i, r := range got {
		if r.MountNS != 42 {
			t.Fatalf("rule %d still bound to namespace %d", i, r.MountNS)
		}
	}
}

func TestRemoveInodeRulesDropsRenamedInode(t *testing.T) {
	g := &LsmFileGuard{}
	table := map[uint64]string{100: "/old/path", 200: "/other"}
	got, err := g.RemoveInodeRules(table, 100)
	if err != nil {
		t.Fatalf("RemoveInodeRules: %v", err)
	}
	if _, ok := got[100]; ok {
		t.Fatalf("renamed inode 100 must lose its rule, got %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("unrelated entries must stay, got %v", got)
	}
}

func TestCheckInodeGenerationDeniesRecycledInode(t *testing.T) {
	g := &LsmFileGuard{}
	got, err := g.CheckInodeGeneration(3, 4)
	if err != nil {
		t.Fatalf("CheckInodeGeneration: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("recycled inode must not inherit the old rule, got %v", got)
	}
}

func TestSwapRulesReturnsCompleteTable(t *testing.T) {
	g := &LsmFileGuard{}
	current := []Rule{{Pattern: "/etc/*", Verdict: VerdictDeny}}
	next := []Rule{
		{Pattern: "/etc/*", Verdict: VerdictDeny},
		{Pattern: "/srv/*", Verdict: VerdictAllow},
	}
	got, err := g.SwapRules(current, next)
	if err != nil {
		t.Fatalf("SwapRules: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("readers must see the whole new table, got %d rules", len(got))
	}
}

func TestExitCodeZeroForAllow(t *testing.T) {
	g := &LsmFileGuard{}
	code, err := g.ExitCode(VerdictAllow)
	if err != nil {
		t.Fatalf("ExitCode: %v", err)
	}
	if code != 0 {
		t.Fatalf("allow must return 0, got %d", code)
	}
}

func TestExitCodeNonZeroForDeny(t *testing.T) {
	g := &LsmFileGuard{}
	code, err := g.ExitCode(VerdictDeny)
	if err != nil {
		t.Fatalf("ExitCode: %v", err)
	}
	if code == 0 {
		t.Fatalf("deny must not return 0, got %d", code)
	}
}

func TestLoadPolicyDeniesAllOnLoadFailure(t *testing.T) {
	g := &LsmFileGuard{}
	got, err := g.LoadPolicy(false)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("failed policy load must fall back to deny, got %v", got)
	}
}

func TestBTFOffsetIsKernelSpecific(t *testing.T) {
	g := &LsmFileGuard{}
	offset, err := g.BTFOffset("6.1", "inode")
	if err != nil {
		t.Fatalf("BTFOffset: %v", err)
	}
	if offset != 8 {
		t.Fatalf("6.1 inode offset must be 8, got %d", offset)
	}
}

func TestCheckProcExclusionDeniesProcUnderWildcard(t *testing.T) {
	g := &LsmFileGuard{}
	got, err := g.CheckProcExclusion("/**", "/proc/self/mem")
	if err != nil {
		t.Fatalf("CheckProcExclusion: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("broad wildcard must not cover /proc, got %v", got)
	}
}

func TestAuditRecordsInode(t *testing.T) {
	g := &LsmFileGuard{}
	rec, err := g.Audit(AuditEvent{Path: "/etc/shadow", Inode: 12345, Access: AccessOpen, Verdict: VerdictDeny})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rec.Inode != 12345 {
		t.Fatalf("audit record must carry the inode, got %d", rec.Inode)
	}
}

func TestSwapProgramActivatesNewProgram(t *testing.T) {
	g := &LsmFileGuard{}
	res, err := g.SwapProgram("prog-old", "prog-new")
	if err != nil {
		t.Fatalf("SwapProgram: %v", err)
	}
	if res.ActiveID != "prog-new" {
		t.Fatalf("new program must serve new requests, active is %q", res.ActiveID)
	}
}

func TestCheckMountPropagationDeniesPropagatedHostPath(t *testing.T) {
	g := &LsmFileGuard{}
	got, err := g.CheckMountPropagation("/host/mnt/escaped", true)
	if err != nil {
		t.Fatalf("CheckMountPropagation: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("path propagated into the host namespace must be denied, got %v", got)
	}
}
