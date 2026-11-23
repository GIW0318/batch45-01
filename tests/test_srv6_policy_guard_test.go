package task

import (
	"testing"

	. "gsb/batch45-safety-c-07"
)

func TestValidateSIDOwnerRejectsForeignLocator(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.ValidateSIDOwner(SID{Value: "2001:db8:2::1", Locator: "2001:db8:1::/48"})
	if err != nil {
		t.Fatalf("ValidateSIDOwner: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("SID outside its locator, want deny, got %v", got)
	}
}

func TestValidateSIDOwnerAcceptsOwnLocator(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.ValidateSIDOwner(SID{Value: "2001:db8:1:2::1", Locator: "2001:db8:1::/48"})
	if err != nil {
		t.Fatalf("ValidateSIDOwner: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("SID inside its locator, want allow, got %v", got)
	}
}

func TestCheckListLenRejectsOverHardwareLimit(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.CheckListLen(20, 16)
	if err != nil {
		t.Fatalf("CheckListLen: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("segment list of 20 exceeds hardware limit 16, want deny, got %v", got)
	}
}

func TestCheckExpansionTableRejectsUnknownSID(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.CheckExpansionTable([]string{"c1", "c2"}, map[string]string{"c1": "2001:db8:1::1"})
	if err != nil {
		t.Fatalf("CheckExpansionTable: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("compressed SID c2 missing from table, want deny, got %v", got)
	}
}

func TestCheckExpansionTableAcceptsConsistentTable(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.CheckExpansionTable([]string{"c1", "c2"}, map[string]string{
		"c1": "2001:db8:1::1",
		"c2": "2001:db8:1::2",
	})
	if err != nil {
		t.Fatalf("CheckExpansionTable: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("every compressed SID is known, want allow, got %v", got)
	}
}

func TestResolveBehaviorMapsDT4ToDecapLookup(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.ResolveBehavior(BehaviorEndDT4, true)
	if err != nil {
		t.Fatalf("ResolveBehavior: %v", err)
	}
	if got != ActionDecapLookup {
		t.Fatalf("End.DT4 with IPv4 destination, want decap+lookup, got %v", got)
	}
}

func TestResolveBehaviorMapsDX4ToCrossConnect(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.ResolveBehavior(BehaviorEndDX4, true)
	if err != nil {
		t.Fatalf("ResolveBehavior: %v", err)
	}
	if got != ActionCrossConnect {
		t.Fatalf("End.DX4, want cross-connect, got %v", got)
	}
}

func TestCheckBSIDConflictRejectsSecondPolicy(t *testing.T) {
	g := &Srv6PolicyGuard{}
	bindings := map[string]string{"2001:db8:ffff::1": "policy-a"}
	got, err := g.CheckBSIDConflict(bindings, "2001:db8:ffff::1", "policy-b")
	if err != nil {
		t.Fatalf("CheckBSIDConflict: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("BSID already bound to policy-a, want deny, got %v", got)
	}
}

func TestDetectLoopRejectsRepeatedNode(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.DetectLoop([]string{"n1", "n2", "n3"}, []string{"n3", "n4"})
	if err != nil {
		t.Fatalf("DetectLoop: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("candidate path revisits n3, want deny, got %v", got)
	}
}

func TestDedupSegmentsRemovesDuplicates(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.DedupSegments([]string{"s1", "s2", "s1", "s3", "s2"})
	if err != nil {
		t.Fatalf("DedupSegments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 distinct segments, got %d (%v)", len(got), got)
	}
	for i, want := range []string{"s1", "s2", "s3"} {
		if got[i] != want {
			t.Fatalf("first occurrence order broken at %d: want %q, got %q", i, want, got[i])
		}
	}
}

func TestEncapHeaderSetsOuterTTL(t *testing.T) {
	g := &Srv6PolicyGuard{}
	h, err := g.EncapHeader([]string{"s1", "s2"}, 0)
	if err != nil {
		t.Fatalf("EncapHeader: %v", err)
	}
	if h.OuterTTL != 64 {
		t.Fatalf("outer TTL must be set explicitly, default 64, got %d", h.OuterTTL)
	}
}

func TestVerifyHMACRejectsBadMAC(t *testing.T) {
	g := &Srv6PolicyGuard{}
	got, err := g.VerifyHMAC(HMACInfo{Present: true, Valid: false})
	if err != nil {
		t.Fatalf("VerifyHMAC: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("SRH HMAC invalid, want deny, got %v", got)
	}
}

func TestReleaseSIDsDropsRefcount(t *testing.T) {
	g := &Srv6PolicyGuard{}
	refs := map[string]int{"2001:db8:1::1": 1, "2001:db8:1::2": 1}
	got, err := g.ReleaseSIDs(refs, []string{"2001:db8:1::1"})
	if err != nil {
		t.Fatalf("ReleaseSIDs: %v", err)
	}
	if got["2001:db8:1::1"] != 0 {
		t.Fatalf("released SID must drop to 0 refs, got %d", got["2001:db8:1::1"])
	}
	if got["2001:db8:1::2"] != 1 {
		t.Fatalf("unrelated SID must keep its refcount, got %d", got["2001:db8:1::2"])
	}
}

func TestRestorePoliciesKeepsSegments(t *testing.T) {
	g := &Srv6PolicyGuard{}
	saved := []Policy{
		{Name: "p1", Tenant: "t1", BSID: "b1", Segments: []string{"s1", "s2"}},
		{Name: "p2", Tenant: "t2", BSID: "b2", Segments: []string{"s3"}},
	}
	policies, err := g.RestorePolicies(saved)
	if err != nil {
		t.Fatalf("RestorePolicies: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("want 2 restored policies, got %d", len(policies))
	}
	if got := len(policies["p1"].Segments); got != 2 {
		t.Fatalf("restored policy p1 must keep 2 segments, got %d", got)
	}
}

func TestCheckTenantSIDSpaceRejectsCrossTenant(t *testing.T) {
	g := &Srv6PolicyGuard{}
	owners := map[string]string{"2001:db8:5::1": "tenant-a"}
	got, err := g.CheckTenantSIDSpace(owners, "tenant-b", "2001:db8:5::1")
	if err != nil {
		t.Fatalf("CheckTenantSIDSpace: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("SID already owned by tenant-a, want deny, got %v", got)
	}
}
