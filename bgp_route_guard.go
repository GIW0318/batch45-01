package task

import (
	"errors"
	"net/netip"
)

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented or crashed check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// PathAttrs carries the BGP path attributes that the import policy inspects.
type PathAttrs struct {
	// ASPath is the AS_PATH as received, left to right, peer AS first.
	ASPath []uint32
	// Communities is the set of standard communities attached to the route.
	Communities []uint32
	// PrependCount is the number of leading AS prepends the sender declares.
	PrependCount int
	// RPKIState is one of "valid", "invalid" or "unknown".
	RPKIState string
	// HasASSet reports whether the AS_PATH contains an AS_SET segment.
	HasASSet bool
}

// Route is one candidate route for one prefix.
type Route struct {
	Prefix    netip.Prefix
	Peer      string
	LocalPref int
	Seq       uint64
	Attrs     PathAttrs
}

// RouteGuard is the egress/ingress route admission component. All policy
// decisions are made by the methods below; the type itself carries no state.
type RouteGuard struct{}

// ValidateASPath checks that the leftmost AS equals the configured peer AS,
// that no AS 0 appears, and that the path is non-empty. It does not decide
// about private AS numbers, AS_SET segments or prepend tampering; those have
// their own checks.
func (g *RouteGuard) ValidateASPath(asPath []uint32, peerAS uint32) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckPrivateAS rejects paths that carry a private AS number (64512-65534,
// 4200000000-4294967294) unless allowPrivate is set. A private AS that was
// stripped from the wire but is still present in the origin chain must not be
// accepted by accident.
func (g *RouteGuard) CheckPrivateAS(asPath []uint32, allowPrivate bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckCommunities requires every community on the route to appear in the
// allow list. A route with no communities is denied when the allow list is
// non-empty.
func (g *RouteGuard) CheckCommunities(communities []uint32, allowList []uint32) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckPrefixLimit denies the import once the peer would exceed the configured
// maximum prefix count. current is the number already accepted for that peer,
// incoming is the number in this update batch, limit is the configured maximum.
func (g *RouteGuard) CheckPrefixLimit(current, incoming, limit int) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// SelectBest picks the best route among candidates for the same prefix.
// Ordering: highest LocalPref first, then shortest AS_PATH, then lowest peer
// AS (the last AS in each path), then the lower peer name, then the lowest
// Seq, so the choice is deterministic.
func (g *RouteGuard) SelectBest(routes []Route) (Route, error) {
	return Route{}, errors.New("not implemented")
}

// CheckRPKI maps an RPKI origin validation state to a verdict: "valid" is
// allowed, "invalid" and "unknown" are denied.
func (g *RouteGuard) CheckRPKI(state string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckASSet denies any route whose AS_PATH contains an AS_SET segment.
func (g *RouteGuard) CheckASSet(hasASSet bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckPrepend compares the declared prepend count with the number of
// consecutive repeats of the peer AS at the head of the path. A declared count
// lower than the observed one means the attribute was tampered with and the
// route is denied; a declared count equal to the observed one is allowed.
func (g *RouteGuard) CheckPrepend(asPath []uint32, declared int) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckDefault applies the default-route policy: 0.0.0.0/0 and ::/0 are only
// accepted when allowDefault is true.
func (g *RouteGuard) CheckDefault(p netip.Prefix, allowDefault bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// FlapDamp applies route flap damping. Every timestamp in history is a past
// event; the penalty is the sum of 2^(-(now-t)/halfLife) over the events and
// the route is denied when the penalty exceeds 4.0.
func (g *RouteGuard) FlapDamp(history []int64, now, halfLife int64) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// ApplyUpdate installs rt into table keyed by rt.Prefix.String(). An update
// with a Seq lower than the stored route's Seq must not overwrite it. The
// returned map is a copy; the input map is left untouched.
func (g *RouteGuard) ApplyUpdate(table map[string]Route, rt Route) (map[string]Route, error) {
	return nil, errors.New("not implemented")
}

// Restore rebuilds the forwarding table from a persisted snapshot, as happens
// after a process restart.
func (g *RouteGuard) Restore(snapshot []Route) (map[string]Route, error) {
	return nil, errors.New("not implemented")
}
