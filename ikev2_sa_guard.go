package task

import (
	"errors"
	"net/netip"
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

// IkeSaGuard validates IKEv2 state transitions in the responder. It holds no
// state of its own.
type IkeSaGuard struct{}

// VerifyCookie checks that a returned cookie was issued for srcAddr and has
// not outlived ttl seconds.
func (g *IkeSaGuard) VerifyCookie(c Cookie, srcAddr string, now, ttl int64) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CreateSA refuses to create a second SA for an SPIi that is already active.
func (g *IkeSaGuard) CreateSA(active map[string]SA, sa SA) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// ClassifyAuth decides whether an IKE_AUTH with msgID is a new request. A
// message ID that was already processed is a retransmission and must not
// create a second request.
func (g *IkeSaGuard) ClassifyAuth(msgID uint32, seen map[uint32]bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckNonce rejects a NONCE that has already been used.
func (g *IkeSaGuard) CheckNonce(nonce string, used map[string]bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// NegotiateDH picks the strongest group both sides support. If there is no
// common group the negotiation fails and the responder must not fall back to
// a weaker group; the returned group is the selected DH group.
func (g *IkeSaGuard) NegotiateDH(offered, accepted []int) (int, error) {
	return 0, errors.New("not implemented")
}

// NegotiateLifetime returns the smaller of the two proposed lifetimes, which
// is the only value both sides may use.
func (g *IkeSaGuard) NegotiateLifetime(initiator, responder uint32) (uint32, error) {
	return 0, errors.New("not implemented")
}

// ParentStateAfterChildDelete reports what must happen to the parent IKE SA
// once children is the set of CHILD_SAs that remain: no children left means
// the parent has to be torn down.
func (g *IkeSaGuard) ParentStateAfterChildDelete(children []string) (ParentState, error) {
	return ParentUnknown, errors.New("not implemented")
}

// CheckIdentityBinding requires the EAP identity and the certificate identity
// to denote the same principal.
func (g *IkeSaGuard) CheckIdentityBinding(eapID, certID string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckNATPayload validates the NAT detection payload against the expected
// value derived from the source and destination addresses.
func (g *IkeSaGuard) CheckNATPayload(payload, expected string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckTrafficSelectors refuses traffic selectors covering 0.0.0.0/0 or ::/0
// unless allowAny is explicitly set.
func (g *IkeSaGuard) CheckTrafficSelectors(sels []netip.Prefix, allowAny bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// Rekey decides whether traffic may move to newSA. The old SA keeps carrying
// traffic until the new one is established, so a cutover before that point is
// denied.
func (g *IkeSaGuard) Rekey(oldSA, newSA SA) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// RestoreSAs rebuilds the SA table from persisted state after a restart.
func (g *IkeSaGuard) RestoreSAs(saved []SA) (map[string]SA, error) {
	return nil, errors.New("not implemented")
}
