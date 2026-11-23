package task

import (
	"net/netip"
	"testing"

	. "gsb/batch45-safety-c-08"
)

func TestCheckTenantIsolationRejectsForeignRD(t *testing.T) {
	g := &EvpnRouteGuard{}
	owners := map[string]string{"100:1": "tenant-a", "200:1": "tenant-b"}
	got, err := g.CheckTenantIsolation("tenant-a", "200:1", owners)
	if err != nil {
		t.Fatalf("CheckTenantIsolation: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("RD 200:1 belongs to tenant-b, want deny, got %v", got)
	}
}

func TestCheckTenantIsolationAcceptsOwnRD(t *testing.T) {
	g := &EvpnRouteGuard{}
	owners := map[string]string{"100:1": "tenant-a", "200:1": "tenant-b"}
	got, err := g.CheckTenantIsolation("tenant-a", "100:1", owners)
	if err != nil {
		t.Fatalf("CheckTenantIsolation: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("RD 100:1 belongs to tenant-a, want allow, got %v", got)
	}
}

func TestCheckVNIVRFRejectsMismatch(t *testing.T) {
	g := &EvpnRouteGuard{}
	mapping := map[string]string{"10010": "vrf-prod", "10020": "vrf-dev"}
	got, err := g.CheckVNIVRF("10010", "vrf-dev", mapping)
	if err != nil {
		t.Fatalf("CheckVNIVRF: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("VNI 10010 maps to vrf-prod, want deny, got %v", got)
	}
}

func TestCheckVNIVRFAllowsMatchingPair(t *testing.T) {
	g := &EvpnRouteGuard{}
	mapping := map[string]string{"10010": "vrf-prod", "10020": "vrf-dev"}
	got, err := g.CheckVNIVRF("10010", "vrf-prod", mapping)
	if err != nil {
		t.Fatalf("CheckVNIVRF: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("VNI 10010 maps to vrf-prod, want allow, got %v", got)
	}
}

func TestSelectMACOriginKeepsFirstLearned(t *testing.T) {
	g := &EvpnRouteGuard{}
	entries := []MACEntry{
		{MAC: "aa:bb:cc:dd:ee:ff", VTEP: "vtep-first", Seq: 1},
		{MAC: "aa:bb:cc:dd:ee:ff", VTEP: "vtep-later", Seq: 5},
	}
	got, err := g.SelectMACOrigin(entries)
	if err != nil {
		t.Fatalf("SelectMACOrigin: %v", err)
	}
	if got.VTEP != "vtep-first" {
		t.Fatalf("earliest learning event must win, got %q", got.VTEP)
	}
}

func TestCheckARPProxyRejectsMismatchedBinding(t *testing.T) {
	g := &EvpnRouteGuard{}
	bindings := map[string]string{"10.0.0.10": "aa:bb:cc:dd:ee:01"}
	got, err := g.CheckARPProxy(bindings, "10.0.0.10", "aa:bb:cc:dd:ee:99")
	if err != nil {
		t.Fatalf("CheckARPProxy: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("ARP reply MAC does not match binding, want deny, got %v", got)
	}
}

func TestLongestMatchPrefixPrefersMostSpecific(t *testing.T) {
	g := &EvpnRouteGuard{}
	table := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("10.1.0.0/16"),
	}
	got, err := g.LongestMatchPrefix(table, netip.MustParseAddr("10.1.2.3"))
	if err != nil {
		t.Fatalf("LongestMatchPrefix: %v", err)
	}
	if got != netip.MustParsePrefix("10.1.0.0/16") {
		t.Fatalf("want most specific cover 10.1.0.0/16, got %v", got)
	}
}

func TestRevokeRouteRemovesFromBothTables(t *testing.T) {
	g := &EvpnRouteGuard{}
	key := "aa:bb:cc:dd:ee:01"
	local := map[string]EVPNRoute{key: {MAC: key, VTEP: "vtep-a"}}
	peer := map[string]EVPNRoute{key: {MAC: key, VTEP: "vtep-a"}}
	removed, err := g.RevokeEverywhere(local, peer, key)
	if err != nil {
		t.Fatalf("RevokeEverywhere: %v", err)
	}
	if removed != 2 {
		t.Fatalf("revoke must clear both local and peer tables, removed %d", removed)
	}
	if len(local) != 0 || len(peer) != 0 {
		t.Fatalf("both tables must be empty, local=%d peer=%d", len(local), len(peer))
	}
}

func TestElectDFPicksLowestAddress(t *testing.T) {
	g := &EvpnRouteGuard{}
	got, err := g.ElectDF([]string{"10.0.0.3", "10.0.0.1", "10.0.0.2"})
	if err != nil {
		t.Fatalf("ElectDF: %v", err)
	}
	if got != "10.0.0.1" {
		t.Fatalf("DF election must pick the lowest address, got %q", got)
	}
}

func TestDedupAddressesRemovesDuplicates(t *testing.T) {
	g := &EvpnRouteGuard{}
	got, err := g.DedupAddresses([]string{"10.0.0.1", "10.0.0.2", "10.0.0.1"})
	if err != nil {
		t.Fatalf("DedupAddresses: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 distinct alias addresses, got %d (%v)", len(got), got)
	}
}

func TestRestoreTableRebuildsEntries(t *testing.T) {
	g := &EvpnRouteGuard{}
	saved := []EVPNRoute{
		{Type: RouteTypeMAC, MAC: "aa:bb:cc:dd:ee:01", VTEP: "vtep-a", Seq: 1},
		{Type: RouteTypeMAC, MAC: "aa:bb:cc:dd:ee:02", VTEP: "vtep-b", Seq: 2},
	}
	table, err := g.RestoreTable(saved)
	if err != nil {
		t.Fatalf("RestoreTable: %v", err)
	}
	if len(table) != 2 {
		t.Fatalf("restored table must contain 2 entries, got %d", len(table))
	}
}

func TestWithdrawPeerRemovesPeerRoutes(t *testing.T) {
	g := &EvpnRouteGuard{}
	table := map[string]EVPNRoute{
		"aa:bb:cc:dd:ee:01": {MAC: "aa:bb:cc:dd:ee:01", VTEP: "vtep-a", Seq: 1},
		"aa:bb:cc:dd:ee:02": {MAC: "aa:bb:cc:dd:ee:02", VTEP: "vtep-a", Seq: 2},
		"aa:bb:cc:dd:ee:03": {MAC: "aa:bb:cc:dd:ee:03", VTEP: "vtep-b", Seq: 3},
	}
	removed, err := g.WithdrawPeer(table, "vtep-a")
	if err != nil {
		t.Fatalf("WithdrawPeer: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("want 2 routes withdrawn for vtep-a, got %d %v", len(removed), removed)
	}
}

func TestApplyUpdateKeepsNewerMACRoute(t *testing.T) {
	g := &EvpnRouteGuard{}
	mac := "aa:bb:cc:dd:ee:01"
	table := map[string]EVPNRoute{
		mac: {Type: RouteTypeMAC, MAC: mac, VTEP: "vtep-new", Seq: 20},
	}
	updated, err := g.ApplyUpdate(table, EVPNRoute{Type: RouteTypeMAC, MAC: mac, VTEP: "vtep-old", Seq: 10})
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if got := updated[mac].VTEP; got != "vtep-new" {
		t.Fatalf("stale update must not resurrect the old route, got VTEP %q", got)
	}
}

func TestCheckRTImportRejectsWildcard(t *testing.T) {
	g := &EvpnRouteGuard{}
	got, err := g.CheckRTImport([]string{"100:1"}, []string{"*"})
	if err != nil {
		t.Fatalf("CheckRTImport: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("wildcard RT import policy, want deny, got %v", got)
	}
}

func TestCheckRTImportAllowsExactRT(t *testing.T) {
	g := &EvpnRouteGuard{}
	got, err := g.CheckRTImport([]string{"100:1"}, []string{"100:1", "100:2"})
	if err != nil {
		t.Fatalf("CheckRTImport: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("exact RT match, want allow, got %v", got)
	}
}
