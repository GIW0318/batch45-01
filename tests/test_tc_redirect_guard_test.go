package task

import (
	"errors"
	"testing"

	. "gsb/batch45-safety-b-04"
)

func newGuard() *TcRedirectGuard {
	return NewTcRedirectGuard(GuardOptions{
		AllowedIfIndexes: []int{5, 6},
		MapCapacity:      4,
		ConntrackSync:    true,
	})
}

func testPolicy() Policy {
	return Policy{
		ID:              "allow-web",
		SrcIP:           "10.0.0.1",
		DstIP:           "10.0.0.2",
		SrcPort:         1234,
		DstPort:         80,
		Proto:           6,
		Action:          VerdictRedirect,
		RedirectIfIndex: 5,
	}
}

func testPacket() Packet {
	return Packet{
		SrcIP:   "10.0.0.1",
		DstIP:   "10.0.0.2",
		SrcPort: 1234,
		DstPort: 80,
		Proto:   6,
		IfIndex: 3,
	}
}

func mustPolicy(t *testing.T, g *TcRedirectGuard, p Policy) {
	t.Helper()
	if err := g.AddPolicy(p); err != nil {
		t.Fatalf("AddPolicy returned error: %v", err)
	}
}

func TestRedirectTargetMustBeAllowedIfIndex(t *testing.T) {
	g := newGuard()
	mustPolicy(t, g, testPolicy())
	bad := testPolicy()
	bad.ID = "redirect-host"
	bad.RedirectIfIndex = 1
	if err := g.AddPolicy(bad); err == nil {
		t.Fatalf("redirect to ifindex 1 (host NIC) must be rejected")
	}
}

func TestMapKeyIsFiveTuple(t *testing.T) {
	g := newGuard()
	key := g.KeyFor(testPacket())
	if key.SrcIP != "10.0.0.1" || key.DstIP != "10.0.0.2" || key.SrcPort != 1234 || key.DstPort != 80 || key.Proto != 6 {
		t.Fatalf("KeyFor = %+v, want the full five tuple", key)
	}
	other := testPacket()
	other.SrcPort = 4321
	if g.KeyFor(other) == key {
		t.Fatalf("packets with different source ports must not share a map key")
	}
}

func TestMapKeyDistinguishesProtocol(t *testing.T) {
	g := newGuard()
	tcp := testPacket()
	udp := testPacket()
	udp.Proto = 17
	if g.KeyFor(tcp) == g.KeyFor(udp) {
		t.Fatalf("tcp and udp flows must not share a map key")
	}
}

func TestNoPolicyDropsPacket(t *testing.T) {
	g := newGuard()
	mustPolicy(t, g, testPolicy())
	v, ifidx, err := g.HookVerdict(testPacket())
	if err != nil {
		t.Fatalf("HookVerdict returned error: %v", err)
	}
	if v != VerdictRedirect || ifidx != 5 {
		t.Fatalf("matching packet verdict = %v/%d, want redirect to 5", v, ifidx)
	}
	unmatched := testPacket()
	unmatched.DstPort = 443
	v, _, err = g.HookVerdict(unmatched)
	if err != nil {
		t.Fatalf("HookVerdict returned error: %v", err)
	}
	if v != VerdictDrop {
		t.Fatalf("packet without policy verdict = %v, want drop", v)
	}
}

func TestRedirectAfterIfIndexRevokedDrops(t *testing.T) {
	g := newGuard()
	mustPolicy(t, g, testPolicy())
	v, _, err := g.HookVerdict(testPacket())
	if err != nil {
		t.Fatalf("HookVerdict returned error: %v", err)
	}
	if v != VerdictRedirect {
		t.Fatalf("verdict = %v, want redirect while ifindex 5 is allowed", v)
	}
	g.SetAllowedIfIndexes(nil)
	v, _, err = g.HookVerdict(testPacket())
	if err != nil {
		t.Fatalf("HookVerdict returned error: %v", err)
	}
	if v != VerdictDrop {
		t.Fatalf("verdict = %v after revoking the redirect ifindex, want drop instead of TC_ACT_OK", v)
	}
}

func TestConntrackKeepsReplyPath(t *testing.T) {
	g := newGuard()
	mustPolicy(t, g, testPolicy())
	v, ifidx, err := g.HookVerdict(testPacket())
	if err != nil {
		t.Fatalf("HookVerdict returned error: %v", err)
	}
	if v != VerdictRedirect || ifidx != 5 {
		t.Fatalf("forward verdict = %v/%d, want redirect to 5", v, ifidx)
	}
	reply := Packet{
		SrcIP:   "10.0.0.2",
		DstIP:   "10.0.0.1",
		SrcPort: 80,
		DstPort: 1234,
		Proto:   6,
		IfIndex: 3,
	}
	v, ifidx, err = g.HookVerdict(reply)
	if err != nil {
		t.Fatalf("HookVerdict returned error for reply: %v", err)
	}
	if v != VerdictRedirect || ifidx != 5 {
		t.Fatalf("reply verdict = %v/%d, want redirect to 5 on the tracked connection", v, ifidx)
	}
}

func TestMapUpdateReplacesAtomically(t *testing.T) {
	g := newGuard()
	k1 := g.KeyFor(testPacket())
	other := testPacket()
	other.SrcPort = 9999
	k2 := g.KeyFor(other)
	if err := g.UpdateMap(map[MapKey]int{k1: 5}); err != nil {
		t.Fatalf("UpdateMap returned error: %v", err)
	}
	if got := g.MapSnapshot(); len(got) != 1 || got[k1] != 5 {
		t.Fatalf("snapshot = %v, want exactly {k1: 5}", got)
	}
	if err := g.UpdateMap(map[MapKey]int{k2: 6}); err != nil {
		t.Fatalf("UpdateMap returned error: %v", err)
	}
	snap := g.MapSnapshot()
	if len(snap) != 1 || snap[k2] != 6 {
		t.Fatalf("snapshot = %v, want exactly {k2: 6}; the map must be replaced, not merged", snap)
	}
	if _, ok := snap[k1]; ok {
		t.Fatalf("stale key survived the map replacement")
	}
}

func TestMapCapacityExceededRejected(t *testing.T) {
	g := newGuard()
	entries := map[MapKey]int{}
	for i := 0; i < 2; i++ {
		pkt := testPacket()
		pkt.SrcPort = uint16(1000 + i)
		entries[g.KeyFor(pkt)] = 5
	}
	if err := g.UpdateMap(entries); err != nil {
		t.Fatalf("UpdateMap rejected %d entries within capacity: %v", len(entries), err)
	}
	for i := 2; i < 6; i++ {
		pkt := testPacket()
		pkt.SrcPort = uint16(1000 + i)
		entries[g.KeyFor(pkt)] = 5
	}
	if err := g.UpdateMap(entries); err == nil {
		t.Fatalf("UpdateMap must reject %d entries for a map of capacity 4 instead of evicting silently", len(entries))
	}
}

func TestDeletePolicyClearsMapEntries(t *testing.T) {
	g := newGuard()
	p := testPolicy()
	mustPolicy(t, g, p)
	key := g.KeyFor(testPacket())
	if _, ok := g.MapSnapshot()[key]; !ok {
		t.Fatalf("policy install did not publish a redirect map entry")
	}
	if err := g.DeletePolicy(p.ID); err != nil {
		t.Fatalf("DeletePolicy returned error: %v", err)
	}
	if _, ok := g.MapSnapshot()[key]; ok {
		t.Fatalf("deleting the policy left its map entry behind")
	}
}

func TestLoadFailureFailsClosed(t *testing.T) {
	g := newGuard()
	if err := g.LoadWith(func(*TcProgram) error { return nil }); err != nil {
		t.Fatalf("LoadWith returned error for a healthy loader: %v", err)
	}
	if !g.Loaded() {
		t.Fatalf("Loaded() must be true after a successful load")
	}
	if g.Blocking() {
		t.Fatalf("Blocking() must be false when a policy program is loaded")
	}
	if err := g.LoadWith(func(*TcProgram) error { return errors.New("verifier rejected program") }); err == nil {
		t.Fatalf("LoadWith must propagate loader failure")
	}
	if !g.Blocking() {
		t.Fatalf("after a failed load the guard must block traffic, not fall back to no policy")
	}
}

func TestAttachAccumulatesInterfaces(t *testing.T) {
	g := newGuard()
	if err := g.Attach([]int{2, 3}); err != nil {
		t.Fatalf("Attach returned error: %v", err)
	}
	if got := len(g.Attached()); got != 2 {
		t.Fatalf("Attached() has %d interfaces, want 2", got)
	}
	if err := g.Attach([]int{4}); err != nil {
		t.Fatalf("Attach returned error: %v", err)
	}
	if got := len(g.Attached()); got != 3 {
		t.Fatalf("Attached() has %d interfaces, want 3 (hooks must accumulate on multi NIC hosts)", got)
	}
}

func TestChecksumRecomputeAfterRedirect(t *testing.T) {
	g := newGuard()
	mustPolicy(t, g, testPolicy())
	if !g.ChecksumRequired(testPacket()) {
		t.Fatalf("redirected packets need checksum recomputation")
	}
	unmatched := testPacket()
	unmatched.DstPort = 443
	if g.ChecksumRequired(unmatched) {
		t.Fatalf("unmodified packets must not request checksum recomputation")
	}
}

func TestCompileUsesOnlySupportedHelpers(t *testing.T) {
	g := newGuard()
	mustPolicy(t, g, testPolicy())
	prog, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	if len(prog.Helpers) == 0 {
		t.Fatalf("compiled program uses no helpers")
	}
	supported := map[string]bool{
		"bpf_redirect":        true,
		"bpf_map_lookup_elem": true,
		"bpf_map_update_elem": true,
		"bpf_skb_store_bytes": true,
		"bpf_l3_csum_replace": true,
		"bpf_l4_csum_replace": true,
	}
	for _, h := range prog.Helpers {
		if !supported[h] {
			t.Fatalf("program uses unsupported helper %q", h)
		}
	}
}

func TestLoadWithKeepsSingleProgram(t *testing.T) {
	g := newGuard()
	for i := 0; i < 2; i++ {
		if err := g.LoadWith(func(*TcProgram) error { return nil }); err != nil {
			t.Fatalf("LoadWith returned error: %v", err)
		}
	}
	if n := g.ActivePrograms(); n != 1 {
		t.Fatalf("ActivePrograms() = %d after two loads, want 1 (atomic hot swap)", n)
	}
}

func TestDeletePolicyDropsTraffic(t *testing.T) {
	g := newGuard()
	p := testPolicy()
	mustPolicy(t, g, p)
	if err := g.DeletePolicy(p.ID); err != nil {
		t.Fatalf("DeletePolicy returned error: %v", err)
	}
	v, _, err := g.HookVerdict(testPacket())
	if err != nil {
		t.Fatalf("HookVerdict returned error: %v", err)
	}
	if v != VerdictDrop {
		t.Fatalf("verdict = %v after policy deletion, want drop", v)
	}
}
