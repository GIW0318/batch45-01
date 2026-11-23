package task

import (
	"net/netip"
	"testing"

	. "gsb/batch45-safety-c-09"
)

func TestVerifyCookieRejectsOtherSourceAddress(t *testing.T) {
	g := &IkeSaGuard{}
	c := Cookie{Value: "abc", PeerAddr: "10.0.0.1", IssuedAt: 100}
	got, err := g.VerifyCookie(c, "10.0.0.2", 110, 60)
	if err != nil {
		t.Fatalf("VerifyCookie: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("cookie issued for another source address, want deny, got %v", got)
	}
}

func TestVerifyCookieRejectsExpiredCookie(t *testing.T) {
	g := &IkeSaGuard{}
	c := Cookie{Value: "abc", PeerAddr: "10.0.0.1", IssuedAt: 100}
	got, err := g.VerifyCookie(c, "10.0.0.1", 200, 60)
	if err != nil {
		t.Fatalf("VerifyCookie: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("cookie older than ttl, want deny, got %v", got)
	}
}

func TestVerifyCookieAcceptsFreshMatchingCookie(t *testing.T) {
	g := &IkeSaGuard{}
	c := Cookie{Value: "abc", PeerAddr: "10.0.0.1", IssuedAt: 100}
	got, err := g.VerifyCookie(c, "10.0.0.1", 130, 60)
	if err != nil {
		t.Fatalf("VerifyCookie: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("fresh cookie for the same address, want allow, got %v", got)
	}
}

func TestCreateSADeniesDuplicateSPI(t *testing.T) {
	g := &IkeSaGuard{}
	active := map[string]SA{"0xdeadbeef": {SPI: "0xdeadbeef", Established: true}}
	got, err := g.CreateSA(active, SA{SPI: "0xdeadbeef", PeerAddr: "10.0.0.1"})
	if err != nil {
		t.Fatalf("CreateSA: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("SPIi already has an active SA, want deny, got %v", got)
	}
}

func TestClassifyAuthTreatsRetransmitAsDuplicate(t *testing.T) {
	g := &IkeSaGuard{}
	seen := map[uint32]bool{2: true}
	got, err := g.ClassifyAuth(2, seen)
	if err != nil {
		t.Fatalf("ClassifyAuth: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("retransmitted IKE_AUTH msgID must not be a new request, want deny, got %v", got)
	}
}

func TestCheckNonceDeniesReuse(t *testing.T) {
	g := &IkeSaGuard{}
	used := map[string]bool{"nonce-1": true}
	got, err := g.CheckNonce("nonce-1", used)
	if err != nil {
		t.Fatalf("CheckNonce: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("NONCE replayed, want deny, got %v", got)
	}
}

func TestNegotiateDHPicksStrongestCommonGroup(t *testing.T) {
	g := &IkeSaGuard{}
	got, err := g.NegotiateDH([]int{14, 19, 20}, []int{2, 19, 20})
	if err != nil {
		t.Fatalf("NegotiateDH: %v", err)
	}
	if got != 20 {
		t.Fatalf("want strongest common DH group 20, got %d", got)
	}
}

func TestNegotiateLifetimeTakesSmaller(t *testing.T) {
	g := &IkeSaGuard{}
	got, err := g.NegotiateLifetime(3600, 1800)
	if err != nil {
		t.Fatalf("NegotiateLifetime: %v", err)
	}
	if got != 1800 {
		t.Fatalf("want smaller lifetime 1800, got %d", got)
	}
}

func TestParentStateGoesIdleWhenLastChildDeleted(t *testing.T) {
	g := &IkeSaGuard{}
	got, err := g.ParentStateAfterChildDelete(nil)
	if err != nil {
		t.Fatalf("ParentStateAfterChildDelete: %v", err)
	}
	if got != ParentIdle {
		t.Fatalf("last CHILD_SA deleted, parent must be torn down, got %v", got)
	}
}

func TestCheckIdentityBindingDeniesMismatch(t *testing.T) {
	g := &IkeSaGuard{}
	got, err := g.CheckIdentityBinding("alice@corp", "bob@corp")
	if err != nil {
		t.Fatalf("CheckIdentityBinding: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("EAP identity differs from certificate identity, want deny, got %v", got)
	}
}

func TestCheckNATPayloadDeniesTamperedPayload(t *testing.T) {
	g := &IkeSaGuard{}
	got, err := g.CheckNATPayload("hash-of-peer-a", "hash-of-peer-b")
	if err != nil {
		t.Fatalf("CheckNATPayload: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("NAT payload does not match the endpoints, want deny, got %v", got)
	}
}

func TestCheckTrafficSelectorsDeniesAnyPrefix(t *testing.T) {
	g := &IkeSaGuard{}
	got, err := g.CheckTrafficSelectors([]netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, false)
	if err != nil {
		t.Fatalf("CheckTrafficSelectors: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("0.0.0.0/0 traffic selector, want deny, got %v", got)
	}
}

func TestCheckTrafficSelectorsAllowsNarrowPrefix(t *testing.T) {
	g := &IkeSaGuard{}
	got, err := g.CheckTrafficSelectors([]netip.Prefix{netip.MustParsePrefix("10.10.0.0/24")}, false)
	if err != nil {
		t.Fatalf("CheckTrafficSelectors: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("narrow traffic selector, want allow, got %v", got)
	}
}

func TestRekeyDeniesCutoverBeforeNewSAEstablished(t *testing.T) {
	g := &IkeSaGuard{}
	oldSA := SA{SPI: "0x01", Established: true, Lifetime: 3600}
	newSA := SA{SPI: "0x02", Established: false, Lifetime: 3600}
	got, err := g.Rekey(oldSA, newSA)
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("new SA not established, old SA must keep traffic, want deny, got %v", got)
	}
}

func TestRestoreSAsRebuildsTable(t *testing.T) {
	g := &IkeSaGuard{}
	saved := []SA{
		{SPI: "0x01", Established: true},
		{SPI: "0x02", Established: true},
	}
	table, err := g.RestoreSAs(saved)
	if err != nil {
		t.Fatalf("RestoreSAs: %v", err)
	}
	if len(table) != 2 {
		t.Fatalf("restored SA table must contain 2 SAs, got %d", len(table))
	}
}
