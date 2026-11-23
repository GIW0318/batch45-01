package task

import (
	"net/netip"
	"testing"

	. "gsb/batch45-safety-c-06"
)

func TestValidateASPathRejectsPeerASMismatch(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.ValidateASPath([]uint32{64501, 65000, 174}, 64500)
	if err != nil {
		t.Fatalf("ValidateASPath: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("first AS does not match peer AS, want deny, got %v", got)
	}
}

func TestValidateASPathAcceptsWellFormedPath(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.ValidateASPath([]uint32{64500, 64501, 174}, 64500)
	if err != nil {
		t.Fatalf("ValidateASPath: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("well formed path, want allow, got %v", got)
	}
}

func TestCheckPrivateASDeniesPrivateNumber(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckPrivateAS([]uint32{64500, 65001, 174}, false)
	if err != nil {
		t.Fatalf("CheckPrivateAS: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("path carries private AS 65001, want deny, got %v", got)
	}
}

func TestCheckCommunitiesRejectsUnknownCommunity(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckCommunities([]uint32{0x000b0009}, []uint32{0x000a0001, 0x000a0002})
	if err != nil {
		t.Fatalf("CheckCommunities: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("community outside allowlist, want deny, got %v", got)
	}
}

func TestCheckCommunitiesAcceptsAllowlisted(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckCommunities([]uint32{0x000a0001, 0x000a0002}, []uint32{0x000a0001, 0x000a0002})
	if err != nil {
		t.Fatalf("CheckCommunities: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("all communities allowlisted, want allow, got %v", got)
	}
}

func TestCheckPrefixLimitDeniesOverLimit(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckPrefixLimit(1000, 1, 1000)
	if err != nil {
		t.Fatalf("CheckPrefixLimit: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("peer would exceed max prefixes, want deny, got %v", got)
	}
}

func TestSelectBestPrefersHigherLocalPref(t *testing.T) {
	g := &RouteGuard{}
	p := netip.MustParsePrefix("203.0.113.0/24")
	routes := []Route{
		{Prefix: p, Peer: "peer-low", LocalPref: 100, Seq: 1, Attrs: PathAttrs{ASPath: []uint32{64500, 174}}},
		{Prefix: p, Peer: "peer-high", LocalPref: 200, Seq: 2, Attrs: PathAttrs{ASPath: []uint32{64500, 64501, 174}}},
	}
	best, err := g.SelectBest(routes)
	if err != nil {
		t.Fatalf("SelectBest: %v", err)
	}
	if best.Peer != "peer-high" {
		t.Fatalf("want peer-high by local preference, got %q", best.Peer)
	}
}

func TestSelectBestBreaksTieByPathLength(t *testing.T) {
	g := &RouteGuard{}
	p := netip.MustParsePrefix("198.51.100.0/24")
	routes := []Route{
		{Prefix: p, Peer: "peer-long", LocalPref: 100, Seq: 1, Attrs: PathAttrs{ASPath: []uint32{64500, 64501, 64502, 174}}},
		{Prefix: p, Peer: "peer-short", LocalPref: 100, Seq: 2, Attrs: PathAttrs{ASPath: []uint32{64500, 174}}},
	}
	best, err := g.SelectBest(routes)
	if err != nil {
		t.Fatalf("SelectBest: %v", err)
	}
	if best.Peer != "peer-short" {
		t.Fatalf("equal local pref, want shorter AS_PATH peer-short, got %q", best.Peer)
	}
}

func TestCheckRPKIDeniesInvalidState(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckRPKI("invalid")
	if err != nil {
		t.Fatalf("CheckRPKI: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("RPKI invalid route, want deny, got %v", got)
	}
}

func TestCheckASSetDeniesSetSegment(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckASSet(true)
	if err != nil {
		t.Fatalf("CheckASSet: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("AS_PATH contains AS_SET, want deny, got %v", got)
	}
}

func TestCheckPrependDetectsResetCounter(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckPrepend([]uint32{64500, 64500, 64500, 64501, 174}, 0)
	if err != nil {
		t.Fatalf("CheckPrepend: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("declared prepend count %d but path shows 2 prepends, want deny, got %v", 0, got)
	}
}

func TestCheckDefaultDeniesUnrequestedDefault(t *testing.T) {
	g := &RouteGuard{}
	got, err := g.CheckDefault(netip.MustParsePrefix("0.0.0.0/0"), false)
	if err != nil {
		t.Fatalf("CheckDefault: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("default route not allowed by policy, want deny, got %v", got)
	}
}

func TestFlapDampSuppressesFlapping(t *testing.T) {
	g := &RouteGuard{}
	now := int64(1_000_000)
	history := []int64{now - 1, now - 2, now - 3, now - 4, now - 5, now - 6, now - 7, now - 8}
	got, err := g.FlapDamp(history, now, 60)
	if err != nil {
		t.Fatalf("FlapDamp: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("route flaps 8 times within seconds, want deny, got %v", got)
	}
}

func TestApplyUpdateKeepsNewerRoute(t *testing.T) {
	g := &RouteGuard{}
	p := netip.MustParsePrefix("10.0.0.0/24")
	table := map[string]Route{
		p.String(): {Prefix: p, Peer: "peer-b", LocalPref: 100, Seq: 20},
	}
	updated, err := g.ApplyUpdate(table, Route{Prefix: p, Peer: "peer-a", LocalPref: 100, Seq: 10})
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if got := updated[p.String()].Peer; got != "peer-b" {
		t.Fatalf("stale update seq 10 must not replace seq 20, got peer %q", got)
	}
}

func TestRestoreRebuildsTable(t *testing.T) {
	g := &RouteGuard{}
	snapshot := []Route{
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Peer: "peer-a", Seq: 1},
		{Prefix: netip.MustParsePrefix("192.0.3.0/24"), Peer: "peer-b", Seq: 2},
	}
	table, err := g.Restore(snapshot)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(table) != 2 {
		t.Fatalf("restored table must contain 2 routes, got %d", len(table))
	}
}
