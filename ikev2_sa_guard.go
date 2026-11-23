package task

import (
	"crypto/subtle"
	"errors"
	"net/netip"
	"sync"
)

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// ParentState is the state the IKE SA must reach once its last CHILD_SA is
// deleted.
type ParentState int

const (
	ParentUnknown ParentState = iota
	ParentActive
	ParentIdle
)

// Cookie is the stateless IKE_SA_INIT cookie handed back to an initiator.
type Cookie struct {
	Value    string
	PeerAddr string
	IssuedAt int64
}

// SA is one IKE or CHILD security association.
type SA struct {
	SPI         string
	LocalAddr   string
	PeerAddr    string
	DHGroup     int
	Lifetime    uint32
	Established bool
	Children    []string
}

// maxHalfOpenPerPeer bounds the number of in-progress (not yet established)
// IKE SAs a single peer address may hold. When the cap is reached the new
// attempt is rejected and the least recently active half-open entry is evicted,
// so responder memory cannot grow with the number of handshakes.
const maxHalfOpenPerPeer = 8

// IkeSaGuard validates IKEv2 state transitions in the responder. The negotiated
// state lives in the caller-supplied maps; the guard itself only keeps the
// bookkeeping required to serialize concurrent creates and to bound the number
// of half-open SAs per peer.
type IkeSaGuard struct {
	mu sync.Mutex
	// peerSPIs records, per peer address, the SPIs the guard admitted in
	// insertion order; the head of the slice is the oldest, least recently
	// active entry and is evicted first.
	peerSPIs map[string][]string
}

// VerifyCookie checks that a returned cookie was issued for srcAddr and has
// not outlived ttl seconds. The cookie is bound to the source address: a
// cookie replayed from another address is denied.
func (g *IkeSaGuard) VerifyCookie(c Cookie, srcAddr string, now, ttl int64) (Verdict, error) {
	if c.Value == "" || c.PeerAddr == "" || srcAddr == "" {
		return VerdictDeny, errors.New("cookie and source address must both be present")
	}
	if ttl <= 0 {
		return VerdictDeny, errors.New("cookie ttl must be positive")
	}
	if c.PeerAddr != srcAddr {
		return VerdictDeny, nil
	}
	age := now - c.IssuedAt
	if age < 0 || age > ttl {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CreateSA is serialized per guard, so concurrent initiations carrying the
// same SPIi can succeed exactly once. Per peer the number of half-open SAs is
// capped: at the cap the new attempt is denied and the oldest half-open entry
// is evicted to bound responder memory.
func (g *IkeSaGuard) CreateSA(active map[string]SA, sa SA) (Verdict, error) {
	if active == nil {
		return VerdictUnknown, errors.New("active SA table is nil")
	}
	if sa.SPI == "" {
		return VerdictDeny, errors.New("SA is missing its SPI")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := active[sa.SPI]; exists {
		return VerdictDeny, nil
	}

	g.initBookkeepingLocked()
	g.pruneBookkeepingLocked(active)

	halfOpen := 0
	oldestSPI := ""
	for _, spi := range g.peerSPIs[sa.PeerAddr] {
		existing, ok := active[spi]
		if !ok || existing.Established || existing.PeerAddr != sa.PeerAddr {
			continue
		}
		halfOpen++
		if oldestSPI == "" {
			oldestSPI = spi
		}
	}

	if halfOpen >= maxHalfOpenPerPeer {
		if oldestSPI != "" {
			delete(active, oldestSPI)
			g.removeSPILocked(sa.PeerAddr, oldestSPI)
		}
		return VerdictDeny, nil
	}

	active[sa.SPI] = sa
	g.peerSPIs[sa.PeerAddr] = append(g.peerSPIs[sa.PeerAddr], sa.SPI)
	return VerdictAllow, nil
}

// ClassifyAuth decides whether an IKE_AUTH with msgID is a new request. A
// message ID that was already processed is a retransmission and must not
// create a second request. New message IDs are recorded as seen.
func (g *IkeSaGuard) ClassifyAuth(msgID uint32, seen map[uint32]bool) (Verdict, error) {
	if seen == nil {
		return VerdictUnknown, errors.New("seen message-ID map is nil")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if seen[msgID] {
		return VerdictDeny, nil
	}
	seen[msgID] = true
	return VerdictAllow, nil
}

// CheckNonce rejects a NONCE that has already been used and records a fresh
// NONCE so it cannot be presented again.
func (g *IkeSaGuard) CheckNonce(nonce string, used map[string]bool) (Verdict, error) {
	if nonce == "" {
		return VerdictDeny, errors.New("NONCE must not be empty")
	}
	if used == nil {
		return VerdictUnknown, errors.New("used NONCE map is nil")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if used[nonce] {
		return VerdictDeny, nil
	}
	used[nonce] = true
	return VerdictAllow, nil
}

// NegotiateDH picks the strongest group both sides support. If there is no
// common group the negotiation fails and the responder must not fall back to
// a weaker group; the returned group is the selected DH group.
func (g *IkeSaGuard) NegotiateDH(offered, accepted []int) (int, error) {
	supported := make(map[int]bool, len(accepted))
	for _, group := range accepted {
		if group > 0 {
			supported[group] = true
		}
	}

	strongest := 0
	matched := false
	for _, group := range offered {
		if group > 0 && supported[group] && group > strongest {
			strongest = group
			matched = true
		}
	}
	if !matched {
		return 0, errors.New("no common DH group: negotiation fails without downgrade")
	}
	return strongest, nil
}

// NegotiateLifetime returns the smaller of the two proposed lifetimes, which
// is the only value both sides may use.
func (g *IkeSaGuard) NegotiateLifetime(initiator, responder uint32) (uint32, error) {
	if initiator == 0 || responder == 0 {
		return 0, errors.New("lifetime must be non-zero")
	}
	if initiator < responder {
		return initiator, nil
	}
	return responder, nil
}

// ParentStateAfterChildDelete reports what must happen to the parent IKE SA
// once children is the set of CHILD_SAs that remain: no children left means
// the parent has to be torn down (idle), otherwise it stays active.
func (g *IkeSaGuard) ParentStateAfterChildDelete(children []string) (ParentState, error) {
	if len(children) == 0 {
		return ParentIdle, nil
	}
	return ParentActive, nil
}

// CheckIdentityBinding requires the EAP identity and the certificate identity
// to denote the same principal.
func (g *IkeSaGuard) CheckIdentityBinding(eapID, certID string) (Verdict, error) {
	if eapID == "" || certID == "" {
		return VerdictDeny, errors.New("identity binding requires both an EAP and a certificate identity")
	}
	if eapID != certID {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckNATPayload validates the NAT detection payload against the expected
// value derived from the source and destination addresses. The comparison is
// constant-time so the check cannot be probed through timing.
func (g *IkeSaGuard) CheckNATPayload(payload, expected string) (Verdict, error) {
	if payload == "" || expected == "" {
		return VerdictDeny, errors.New("NAT detection payload must not be empty")
	}
	if subtle.ConstantTimeCompare([]byte(payload), []byte(expected)) != 1 {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckTrafficSelectors refuses traffic selectors covering 0.0.0.0/0 or ::/0
// unless allowAny is explicitly set.
func (g *IkeSaGuard) CheckTrafficSelectors(sels []netip.Prefix, allowAny bool) (Verdict, error) {
	if allowAny {
		return VerdictAllow, nil
	}
	for _, prefix := range sels {
		if prefix.IsValid() && prefix.Bits() == 0 {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// Rekey decides whether traffic may move to newSA. The old SA keeps carrying
// traffic until the new one is established, so a cutover before that point is
// denied.
func (g *IkeSaGuard) Rekey(oldSA, newSA SA) (Verdict, error) {
	if oldSA.SPI == "" || newSA.SPI == "" {
		return VerdictUnknown, errors.New("rekey requires both an old and a new SA")
	}
	if oldSA.SPI == newSA.SPI {
		return VerdictDeny, errors.New("rekey target SA must differ from the old SA")
	}
	if !newSA.Established {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// RestoreSAs rebuilds the SA table from persisted state after a restart. The
// rebuilt map is keyed by SPI and contains exactly the persisted entries; a
// corrupt snapshot (missing or duplicate SPI) is rejected instead of silently
// dropping state.
func (g *IkeSaGuard) RestoreSAs(saved []SA) (map[string]SA, error) {
	table := make(map[string]SA, len(saved))
	for _, sa := range saved {
		if sa.SPI == "" {
			return nil, errors.New("persisted SA is missing its SPI")
		}
		if _, duplicate := table[sa.SPI]; duplicate {
			return nil, errors.New("persisted SA table contains a duplicate SPI")
		}
		table[sa.SPI] = sa
	}
	return table, nil
}

func (g *IkeSaGuard) initBookkeepingLocked() {
	if g.peerSPIs == nil {
		g.peerSPIs = make(map[string][]string)
	}
}

// pruneBookkeepingLocked drops tracking entries whose SPI disappeared from the
// caller's table, keeping guard memory proportional to the live SA table.
func (g *IkeSaGuard) pruneBookkeepingLocked(active map[string]SA) {
	for peer, spis := range g.peerSPIs {
		kept := spis[:0]
		for _, spi := range spis {
			if _, ok := active[spi]; ok {
				kept = append(kept, spi)
			}
		}
		if len(kept) == 0 {
			delete(g.peerSPIs, peer)
		} else {
			g.peerSPIs[peer] = kept
		}
	}
}

func (g *IkeSaGuard) removeSPILocked(peer, spi string) {
	spis := g.peerSPIs[peer]
	for index, candidate := range spis {
		if candidate == spi {
			g.peerSPIs[peer] = append(spis[:index], spis[index+1:]...)
			return
		}
	}
}
