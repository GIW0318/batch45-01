package task

import (
	"fmt"
	"sync"
	"testing"

	. "gsb/batch45-safety-b-12"
)

// baseConfig is the policy used by most tests: one ROA for 203.0.113.0/24
// and one for 198.51.100.0/24 (both origin 64500), a single whitelisted
// community, a six-hop path limit and a three-withdraw damping threshold.
func baseConfig() GuardConfig {
	return GuardConfig{
		ROAs: []ROA{
			{Prefix: "203.0.113.0/24", MaxLen: 24, OriginAS: 64500},
			{Prefix: "198.51.100.0/24", MaxLen: 24, OriginAS: 64500},
		},
		CommunityWhitelist: []uint32{65000},
		MaxASPathLen:       6,
		FlapThreshold:      3,
	}
}

func newGuard(t *testing.T) *HijackGuard {
	t.Helper()
	return NewGuard(baseConfig())
}

func hasReject(rejects []Reject, prefix string, code RejectReason) bool {
	for _, r := range rejects {
		if r.Prefix == prefix && r.Code == code {
			return true
		}
	}
	return false
}

func TestValidRPKIAnnouncementInstalled(t *testing.T) {
	g := newGuard(t)
	a := Announcement{
		Peer:        "peer-a",
		Prefix:      "203.0.113.0/24",
		NextHop:     "192.0.2.1",
		OriginAS:    64500,
		ASPath:      []uint32{64500},
		Communities: []uint32{65000},
		RPKI:        RPKIValid,
		Seq:         1,
	}
	if err := g.Announce(a); err != nil {
		t.Fatalf("announce valid route: %v", err)
	}
	rt, ok := g.Lookup("203.0.113.0/24")
	if !ok {
		t.Fatalf("valid announcement was not installed")
	}
	if rt.OriginAS != 64500 || rt.Peer != "peer-a" || rt.NextHop != "192.0.2.1" {
		t.Fatalf("installed route = %+v, want origin 64500 via peer-a", rt)
	}
	if got := g.Stats().Installed; got != 1 {
		t.Fatalf("installed counter = %d, want 1", got)
	}
}

func TestRPKIInvalidAnnouncementRejected(t *testing.T) {
	g := newGuard(t)
	a := Announcement{
		Peer:     "peer-b",
		Prefix:   "192.0.2.0/24",
		NextHop:  "192.0.2.9",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKIInvalid,
		Seq:      1,
	}
	if err := g.Announce(a); err == nil {
		t.Fatalf("announcement with an invalid RPKI verdict was accepted")
	}
	if _, ok := g.Lookup("192.0.2.0/24"); ok {
		t.Fatalf("invalid RPKI route reached the forwarding table")
	}
	if !hasReject(g.Rejects(), "192.0.2.0/24", ReasonRPKIInvalid) {
		t.Fatalf("rejects = %+v, want HIJACK_RPKI_INVALID", g.Rejects())
	}
	if got := g.Stats().Rejected; got != 1 {
		t.Fatalf("rejected counter = %d, want 1", got)
	}
}

func TestOriginASMustMatchROA(t *testing.T) {
	g := newGuard(t)
	// The prefix length is inside the accepted range and the validator says
	// "not found", but the local ROA only authorises origin 64500.
	a := Announcement{
		Peer:     "peer-c",
		Prefix:   "203.0.113.0/24",
		NextHop:  "192.0.2.30",
		OriginAS: 64600,
		ASPath:   []uint32{64600},
		RPKI:     RPKINotFound,
		Seq:      1,
	}
	if err := g.Announce(a); err == nil {
		t.Fatalf("prefix-length-only matching accepted origin 64600 for a 64500 ROA")
	}
	if _, ok := g.Lookup("203.0.113.0/24"); ok {
		t.Fatalf("unauthorised origin reached the forwarding table")
	}
	if !hasReject(g.Rejects(), "203.0.113.0/24", ReasonRPKIInvalid) {
		t.Fatalf("rejects = %+v, want HIJACK_RPKI_INVALID", g.Rejects())
	}
}

func TestPrivateASInPathRejected(t *testing.T) {
	g := newGuard(t)
	// A transit private ASN survives in the path after prepending.
	inPath := Announcement{
		Peer:     "peer-d",
		Prefix:   "203.0.113.0/24",
		NextHop:  "192.0.2.40",
		OriginAS: 64500,
		ASPath:   []uint32{65010, 64500},
		RPKI:     RPKIValid,
		Seq:      1,
	}
	if err := g.Announce(inPath); err == nil {
		t.Fatalf("path containing private ASN 65010 was accepted")
	}
	if !hasReject(g.Rejects(), "203.0.113.0/24", ReasonPrivateAS) {
		t.Fatalf("rejects = %+v, want HIJACK_PRIVATE_AS", g.Rejects())
	}
	// A private origin ASN is equally unacceptable.
	privateOrigin := Announcement{
		Peer:     "peer-d",
		Prefix:   "198.18.5.0/24",
		NextHop:  "192.0.2.41",
		OriginAS: 64512,
		ASPath:   []uint32{64512},
		RPKI:     RPKINotFound,
		Seq:      2,
	}
	if err := g.Announce(privateOrigin); err == nil {
		t.Fatalf("private origin ASN 64512 was accepted")
	}
	if !hasReject(g.Rejects(), "198.18.5.0/24", ReasonPrivateAS) {
		t.Fatalf("rejects = %+v, want HIJACK_PRIVATE_AS", g.Rejects())
	}
	if _, ok := g.Lookup("198.18.5.0/24"); ok {
		t.Fatalf("private-origin route reached the forwarding table")
	}
}

func TestASSetPathRejected(t *testing.T) {
	g := newGuard(t)
	a := Announcement{
		Peer:     "peer-e",
		Prefix:   "198.51.100.0/24",
		NextHop:  "192.0.2.50",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		ASSet:    true,
		RPKI:     RPKIValid,
		Seq:      1,
	}
	if err := g.Announce(a); err == nil {
		t.Fatalf("AS_SET path was accepted as a normal sequence")
	}
	if _, ok := g.Lookup("198.51.100.0/24"); ok {
		t.Fatalf("AS_SET route reached the forwarding table")
	}
	if !hasReject(g.Rejects(), "198.51.100.0/24", ReasonASSet) {
		t.Fatalf("rejects = %+v, want HIJACK_AS_SET", g.Rejects())
	}
}

func TestMOASConflictRejectsLaterOrigin(t *testing.T) {
	g := newGuard(t)
	// 198.18.1.0/24 has no ROA, so both origins pass RPKI validation and the
	// MOAS rule is the only gate that can stop the second one.
	first := Announcement{
		Peer:     "peer-a",
		Prefix:   "198.18.1.0/24",
		NextHop:  "192.0.2.61",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKINotFound,
		Priority: 10,
		Seq:      1,
	}
	if err := g.Announce(first); err != nil {
		t.Fatalf("announce first origin: %v", err)
	}
	second := Announcement{
		Peer:     "peer-b",
		Prefix:   "198.18.1.0/24",
		NextHop:  "192.0.2.62",
		OriginAS: 64600,
		ASPath:   []uint32{64600},
		RPKI:     RPKINotFound,
		Priority: 10,
		Seq:      2,
	}
	if err := g.Announce(second); err == nil {
		t.Fatalf("second origin 64600 overwrote origin 64500")
	}
	rt, ok := g.Lookup("198.18.1.0/24")
	if !ok {
		t.Fatalf("primary route disappeared after the MOAS conflict")
	}
	if rt.OriginAS != 64500 || rt.Peer != "peer-a" || rt.NextHop != "192.0.2.61" {
		t.Fatalf("installed route = %+v, want the first origin 64500 via peer-a", rt)
	}
	if !hasReject(g.Rejects(), "198.18.1.0/24", ReasonMOASConflict) {
		t.Fatalf("rejects = %+v, want HIJACK_MOAS_CONFLICT", g.Rejects())
	}
	if got := len(g.Routes()); got != 1 {
		t.Fatalf("RIB holds %d routes, want 1", got)
	}
}

func TestMOASPrimarySelectedByPriority(t *testing.T) {
	g := newGuard(t)
	low := Announcement{
		Peer:     "peer-a",
		Prefix:   "198.19.0.0/24",
		NextHop:  "192.0.2.71",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKINotFound,
		Priority: 10,
		Seq:      1,
	}
	if err := g.Announce(low); err != nil {
		t.Fatalf("announce low priority origin: %v", err)
	}
	high := Announcement{
		Peer:     "peer-b",
		Prefix:   "198.19.0.0/24",
		NextHop:  "192.0.2.72",
		OriginAS: 64600,
		ASPath:   []uint32{64600},
		RPKI:     RPKINotFound,
		Priority: 30,
		Seq:      2,
	}
	if err := g.Announce(high); err != nil {
		t.Fatalf("announce higher priority origin: %v", err)
	}
	rt, ok := g.Lookup("198.19.0.0/24")
	if !ok {
		t.Fatalf("no route installed for the contended prefix")
	}
	if rt.OriginAS != 64600 || rt.Peer != "peer-b" || rt.Priority != 30 {
		t.Fatalf("installed route = %+v, want the higher priority origin 64600 via peer-b", rt)
	}
}

func TestDefaultRouteDeniedUnlessAllowed(t *testing.T) {
	g := newGuard(t)
	a := Announcement{
		Peer:     "peer-a",
		Prefix:   "0.0.0.0/0",
		NextHop:  "192.0.2.81",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKINotFound,
		Seq:      1,
	}
	if err := g.Announce(a); err == nil {
		t.Fatalf("default route was accepted without explicit permission")
	}
	if _, ok := g.Lookup("0.0.0.0/0"); ok {
		t.Fatalf("default route reached the forwarding table")
	}
	if !hasReject(g.Rejects(), "0.0.0.0/0", ReasonDefaultDenied) {
		t.Fatalf("rejects = %+v, want HIJACK_DEFAULT_DENIED", g.Rejects())
	}
}

func TestDefaultRouteAllowedWhenConfigured(t *testing.T) {
	cfg := baseConfig()
	cfg.AllowDefaultRoute = true
	g := NewGuard(cfg)
	a := Announcement{
		Peer:     "peer-a",
		Prefix:   "0.0.0.0/0",
		NextHop:  "192.0.2.82",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKINotFound,
		Seq:      1,
	}
	if err := g.Announce(a); err != nil {
		t.Fatalf("explicitly allowed default route rejected: %v", err)
	}
	if _, ok := g.Lookup("0.0.0.0/0"); !ok {
		t.Fatalf("allowed default route was not installed")
	}
	if got := len(g.Rejects()); got != 0 {
		t.Fatalf("rejects = %+v, want none", g.Rejects())
	}
}

func TestCommunityWhitelistEnforced(t *testing.T) {
	g := newGuard(t)
	good := Announcement{
		Peer:        "peer-a",
		Prefix:      "198.51.100.0/24",
		NextHop:     "192.0.2.91",
		OriginAS:    64500,
		ASPath:      []uint32{64500},
		Communities: []uint32{65000},
		RPKI:        RPKIValid,
		Seq:         1,
	}
	if err := g.Announce(good); err != nil {
		t.Fatalf("announce whitelisted community: %v", err)
	}
	bad := Announcement{
		Peer:        "peer-b",
		Prefix:      "203.0.113.0/24",
		NextHop:     "192.0.2.92",
		OriginAS:    64500,
		ASPath:      []uint32{64500},
		Communities: []uint32{65000, 64496},
		RPKI:        RPKIValid,
		Seq:         2,
	}
	if err := g.Announce(bad); err == nil {
		t.Fatalf("community 64496 outside the whitelist was accepted")
	}
	if _, ok := g.Lookup("203.0.113.0/24"); ok {
		t.Fatalf("route with a non-whitelisted community reached the table")
	}
	if !hasReject(g.Rejects(), "203.0.113.0/24", ReasonCommunity) {
		t.Fatalf("rejects = %+v, want HIJACK_COMMUNITY", g.Rejects())
	}
}

func TestPrependCountedInPathLength(t *testing.T) {
	g := newGuard(t)
	// len(ASPath)=1 plus five prepends is exactly the six-hop limit.
	within := Announcement{
		Peer:     "peer-a",
		Prefix:   "198.51.100.0/24",
		NextHop:  "192.0.2.101",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		Prepend:  5,
		RPKI:     RPKIValid,
		Seq:      1,
	}
	if err := g.Announce(within); err != nil {
		t.Fatalf("path of length 6 rejected: %v", err)
	}
	// One more prepend exceeds the limit; zeroing the counter must not help.
	over := Announcement{
		Peer:     "peer-b",
		Prefix:   "203.0.113.0/24",
		NextHop:  "192.0.2.102",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		Prepend:  6,
		RPKI:     RPKIValid,
		Seq:      2,
	}
	if err := g.Announce(over); err == nil {
		t.Fatalf("prepended path of length 7 bypassed the length check")
	}
	if _, ok := g.Lookup("203.0.113.0/24"); ok {
		t.Fatalf("over-long path reached the forwarding table")
	}
	if !hasReject(g.Rejects(), "203.0.113.0/24", ReasonPathTooLong) {
		t.Fatalf("rejects = %+v, want HIJACK_AS_PATH_TOO_LONG", g.Rejects())
	}
}

func TestFlapDampingSuppressesPrefix(t *testing.T) {
	g := newGuard(t)
	const prefix = "198.18.2.0/24"
	for i := 0; i < 3; i++ {
		ann := Announcement{
			Peer:     "peer-a",
			Prefix:   prefix,
			NextHop:  "192.0.2.110",
			OriginAS: 64500,
			ASPath:   []uint32{64500},
			RPKI:     RPKINotFound,
			Seq:      uint64(2*i + 1),
		}
		if err := g.Announce(ann); err != nil {
			t.Fatalf("announce %d: %v", i, err)
		}
		if err := g.Withdraw("peer-a", prefix); err != nil {
			t.Fatalf("withdraw %d: %v", i, err)
		}
	}
	dampered := Announcement{
		Peer:     "peer-a",
		Prefix:   prefix,
		NextHop:  "192.0.2.110",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKINotFound,
		Seq:      7,
	}
	if err := g.Announce(dampered); err == nil {
		t.Fatalf("prefix flapped three times was reinstalled")
	}
	if _, ok := g.Lookup(prefix); ok {
		t.Fatalf("damped prefix reached the forwarding table")
	}
	if !hasReject(g.Rejects(), prefix, ReasonDamped) {
		t.Fatalf("rejects = %+v, want HIJACK_DAMPED", g.Rejects())
	}
	if n := g.FlapCount(prefix); n < 3 {
		t.Fatalf("flap count = %d, want at least 3", n)
	}
}

func TestSessionDownWithdrawsPeerRoutes(t *testing.T) {
	g := newGuard(t)
	peerA := []Announcement{
		{Peer: "peer-a", Prefix: "198.51.100.0/24", NextHop: "192.0.2.121", OriginAS: 64500, ASPath: []uint32{64500}, RPKI: RPKIValid, Seq: 1},
		{Peer: "peer-a", Prefix: "203.0.113.0/24", NextHop: "192.0.2.122", OriginAS: 64500, ASPath: []uint32{64500}, RPKI: RPKIValid, Seq: 2},
	}
	for _, a := range peerA {
		if err := g.Announce(a); err != nil {
			t.Fatalf("announce %s: %v", a.Prefix, err)
		}
	}
	peerB := Announcement{
		Peer:     "peer-b",
		Prefix:   "198.18.3.0/24",
		NextHop:  "192.0.2.123",
		OriginAS: 64600,
		ASPath:   []uint32{64600},
		RPKI:     RPKINotFound,
		Seq:      3,
	}
	if err := g.Announce(peerB); err != nil {
		t.Fatalf("announce peer-b route: %v", err)
	}
	if err := g.SessionDown("peer-a"); err != nil {
		t.Fatalf("session down: %v", err)
	}
	for _, a := range peerA {
		if _, ok := g.Lookup(a.Prefix); ok {
			t.Fatalf("route %s survived the session teardown", a.Prefix)
		}
	}
	if _, ok := g.Lookup(peerB.Prefix); !ok {
		t.Fatalf("peer-b route withdrawn by peer-a session down")
	}
	if got := g.Stats().Withdrawn; got != 2 {
		t.Fatalf("withdrawn counter = %d, want 2", got)
	}
	if got := len(g.Routes()); got != 1 {
		t.Fatalf("RIB holds %d routes after teardown, want 1", got)
	}
}

func TestConcurrentUpdatesSerializedAndFresh(t *testing.T) {
	g := newGuard(t)
	const workers = 40
	var wg sync.WaitGroup
	var errs [workers]error
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prefix := fmt.Sprintf("198.18.%d.0/24", i)
			errs[i] = g.Announce(Announcement{
				Peer:     "peer-a",
				Prefix:   prefix,
				NextHop:  "192.0.2.1",
				OriginAS: 64500,
				ASPath:   []uint32{64500},
				RPKI:     RPKINotFound,
				Seq:      uint64(i + 1),
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent announce %d: %v", i, err)
		}
	}
	if got := len(g.Routes()); got != workers {
		t.Fatalf("RIB holds %d routes after concurrent adds, want %d", got, workers)
	}
	fresh := Announcement{
		Peer:     "peer-a",
		Prefix:   "198.18.0.0/24",
		NextHop:  "192.0.2.100",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKINotFound,
		Seq:      100,
	}
	if err := g.Announce(fresh); err != nil {
		t.Fatalf("announce fresh update: %v", err)
	}
	stale := Announcement{
		Peer:     "peer-a",
		Prefix:   "198.18.0.0/24",
		NextHop:  "192.0.2.200",
		OriginAS: 64500,
		ASPath:   []uint32{64500},
		RPKI:     RPKINotFound,
		Seq:      50,
	}
	if err := g.Announce(stale); err == nil {
		t.Fatalf("stale sequence 50 overwrote sequence 100")
	}
	rt, ok := g.Lookup("198.18.0.0/24")
	if !ok {
		t.Fatalf("route disappeared after the stale update")
	}
	if rt.NextHop != "192.0.2.100" || rt.Seq != 100 {
		t.Fatalf("installed route = %+v, want the seq 100 nexthop 192.0.2.100", rt)
	}
	if !hasReject(g.Rejects(), "198.18.0.0/24", ReasonStaleUpdate) {
		t.Fatalf("rejects = %+v, want HIJACK_STALE_UPDATE", g.Rejects())
	}
}

func TestRestartRestoresSnapshot(t *testing.T) {
	g := newGuard(t)
	anns := []Announcement{
		{Peer: "peer-a", Prefix: "198.51.100.0/24", NextHop: "192.0.2.131", OriginAS: 64500, ASPath: []uint32{64500}, RPKI: RPKIValid, Seq: 1},
		{Peer: "peer-b", Prefix: "198.18.4.0/24", NextHop: "192.0.2.132", OriginAS: 64600, ASPath: []uint32{64600}, RPKI: RPKINotFound, Seq: 2},
	}
	for _, a := range anns {
		if err := g.Announce(a); err != nil {
			t.Fatalf("announce %s: %v", a.Prefix, err)
		}
	}
	snap := g.Snapshot()
	if got := len(snap.Routes); got != 2 {
		t.Fatalf("snapshot holds %d routes, want 2", got)
	}
	restored, err := NewGuardFromSnapshot(baseConfig(), snap)
	if err != nil {
		t.Fatalf("restore guard from snapshot: %v", err)
	}
	for _, a := range anns {
		rt, ok := restored.Lookup(a.Prefix)
		if !ok {
			t.Fatalf("route %s missing after restart", a.Prefix)
		}
		if rt.OriginAS != a.OriginAS || rt.NextHop != a.NextHop {
			t.Fatalf("restored route = %+v, want origin %d via %s", rt, a.OriginAS, a.NextHop)
		}
	}
	if got := len(restored.Routes()); got != 2 {
		t.Fatalf("restored RIB holds %d routes, want 2", got)
	}
}
