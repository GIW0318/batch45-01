package task

import (
	"errors"
	"math"
	"net/netip"
	"sort"
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
	if peerAS == 0 {
		return VerdictUnknown, errors.New("peer AS 0 is not a valid AS number")
	}
	if len(asPath) == 0 {
		return VerdictDeny, nil
	}
	if asPath[0] != peerAS {
		return VerdictDeny, nil
	}
	for _, as := range asPath {
		if as == 0 {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// CheckPrivateAS rejects paths that carry a private AS number (64512-65534,
// 4200000000-4294967294) unless allowPrivate is set. A private AS that was
// stripped from the wire but is still present in the origin chain must not be
// accepted by accident.
func (g *RouteGuard) CheckPrivateAS(asPath []uint32, allowPrivate bool) (Verdict, error) {
	if allowPrivate {
		return VerdictAllow, nil
	}
	for _, as := range asPath {
		if isPrivateAS(as) {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// CheckCommunities requires every community on the route to appear in the
// allow list. A route with no communities is denied when the allow list is
// non-empty.
func (g *RouteGuard) CheckCommunities(communities []uint32, allowList []uint32) (Verdict, error) {
	if len(allowList) == 0 {
		return VerdictAllow, nil
	}
	if len(communities) == 0 {
		return VerdictDeny, nil
	}
	allowed := make(map[uint32]struct{}, len(allowList))
	for _, community := range allowList {
		allowed[community] = struct{}{}
	}
	for _, community := range communities {
		if _, ok := allowed[community]; !ok {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// CheckPrefixLimit denies the import once the peer would exceed the configured
// maximum prefix count. current is the number already accepted for that peer,
// incoming is the number in this update batch, limit is the configured maximum.
func (g *RouteGuard) CheckPrefixLimit(current, incoming, limit int) (Verdict, error) {
	if limit < 0 || current < 0 || incoming < 0 {
		return VerdictUnknown, errors.New("prefix counts and limit must be non-negative")
	}
	if current+incoming > limit {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// SelectBest picks the best route among candidates for the same prefix.
// Ordering: highest LocalPref first, then shortest AS_PATH, then lowest peer
// AS (the last AS in each path), then the lower peer name, then the lowest
// Seq, so the choice is deterministic.
func (g *RouteGuard) SelectBest(routes []Route) (Route, error) {
	if len(routes) == 0 {
		return Route{}, errors.New("no candidate routes")
	}
	best := 0
	for i := 1; i < len(routes); i++ {
		if routeLess(routes[i], routes[best]) {
			best = i
		}
	}
	return cloneRoute(routes[best]), nil
}

// CheckRPKI maps an RPKI origin validation state to a verdict: "valid" is
// allowed, "invalid" and "unknown" are denied.
func (g *RouteGuard) CheckRPKI(state string) (Verdict, error) {
	switch state {
	case "valid":
		return VerdictAllow, nil
	case "invalid", "unknown":
		return VerdictDeny, nil
	default:
		return VerdictUnknown, errors.New("unknown RPKI state: " + state)
	}
}

// CheckASSet denies any route whose AS_PATH contains an AS_SET segment.
func (g *RouteGuard) CheckASSet(hasASSet bool) (Verdict, error) {
	if hasASSet {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckPrepend compares the declared prepend count with the number of
// consecutive repeats of the peer AS at the head of the path. A declared count
// lower than the observed one means the attribute was tampered with and the
// route is denied; a declared count equal to the observed one is allowed.
func (g *RouteGuard) CheckPrepend(asPath []uint32, declared int) (Verdict, error) {
	if declared < 0 {
		return VerdictUnknown, errors.New("declared prepend count must be non-negative")
	}
	observed := headRepeats(asPath)
	if declared != observed {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckDefault applies the default-route policy: 0.0.0.0/0 and ::/0 are only
// accepted when allowDefault is true.
func (g *RouteGuard) CheckDefault(p netip.Prefix, allowDefault bool) (Verdict, error) {
	if !p.IsValid() {
		return VerdictUnknown, errors.New("invalid prefix")
	}
	if p.Bits() == 0 && (p.Addr().Is4() || p.Addr().Is6()) && !allowDefault {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// FlapDamp applies route flap damping. Every timestamp in history is a past
// event; the penalty is the sum of 2^(-(now-t)/halfLife) over the events and
// the route is denied when the penalty exceeds 4.0.
func (g *RouteGuard) FlapDamp(history []int64, now, halfLife int64) (Verdict, error) {
	if halfLife <= 0 {
		return VerdictUnknown, errors.New("half life must be positive")
	}
	penalty := 0.0
	for _, eventAt := range history {
		age := now - eventAt
		if age < 0 {
			return VerdictUnknown, errors.New("flap event timestamp is in the future")
		}
		penalty += math.Pow(2, -float64(age)/float64(halfLife))
	}
	if penalty > flapSuppressPenalty {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// ApplyUpdate installs rt into table keyed by rt.Prefix.String(). An update
// with a Seq lower than the stored route's Seq must not overwrite it. The
// returned map is a copy; the input map is left untouched.
func (g *RouteGuard) ApplyUpdate(table map[string]Route, rt Route) (map[string]Route, error) {
	if !rt.Prefix.IsValid() {
		return nil, errors.New("route has no valid prefix")
	}
	out := make(map[string]Route, len(table)+1)
	for key, existing := range table {
		out[key] = cloneRoute(existing)
	}
	key := rt.Prefix.String()
	if existing, ok := out[key]; ok && existing.Seq > rt.Seq {
		return out, nil
	}
	out[key] = cloneRoute(rt)
	return out, nil
}

// Restore rebuilds the forwarding table from a persisted snapshot, as happens
// after a process restart.
func (g *RouteGuard) Restore(snapshot []Route) (map[string]Route, error) {
	out := make(map[string]Route, len(snapshot))
	for _, rt := range snapshot {
		if !rt.Prefix.IsValid() {
			return nil, errors.New("snapshot contains route without valid prefix")
		}
		key := rt.Prefix.String()
		if existing, ok := out[key]; ok && existing.Seq >= rt.Seq {
			continue
		}
		out[key] = cloneRoute(rt)
	}
	return out, nil
}

const flapSuppressPenalty = 4.0

// isPrivateAS reports whether as belongs to one of the private/reserved AS
// ranges that must not leak through the egress policy.
func isPrivateAS(as uint32) bool {
	return (as >= 64512 && as <= 65534) ||
		(as >= 4200000000 && as <= 4294967294)
}

// headRepeats counts how many consecutive entries at the head of asPath equal
// the first entry, i.e. the observed number of leading AS prepends.
func headRepeats(asPath []uint32) int {
	if len(asPath) == 0 {
		return 0
	}
	head := asPath[0]
	observed := 0
	for observed < len(asPath) && asPath[observed] == head {
		observed++
	}
	return observed
}

// lastAS returns the origin (rightmost) AS of a path, or MaxUint32 when the
// path is empty, so empty paths sort after real ones deterministically.
func lastAS(asPath []uint32) uint32 {
	if len(asPath) == 0 {
		return ^uint32(0)
	}
	return asPath[len(asPath)-1]
}

// routeLess implements the full best-path ordering documented on SelectBest.
func routeLess(a, b Route) bool {
	if a.LocalPref != b.LocalPref {
		return a.LocalPref > b.LocalPref
	}
	if len(a.Attrs.ASPath) != len(b.Attrs.ASPath) {
		return len(a.Attrs.ASPath) < len(b.Attrs.ASPath)
	}
	if lastAS(a.Attrs.ASPath) != lastAS(b.Attrs.ASPath) {
		return lastAS(a.Attrs.ASPath) < lastAS(b.Attrs.ASPath)
	}
	if a.Peer != b.Peer {
		return a.Peer < b.Peer
	}
	return a.Seq < b.Seq
}

// cloneRoute returns a deep copy of rt so callers can never mutate shared
// slice state through a table entry.
func cloneRoute(rt Route) Route {
	copyRoute := rt
	if rt.Attrs.ASPath != nil {
		copyRoute.Attrs.ASPath = append([]uint32(nil), rt.Attrs.ASPath...)
	}
	if rt.Attrs.Communities != nil {
		copyRoute.Attrs.Communities = append([]uint32(nil), rt.Attrs.Communities...)
	}
	return copyRoute
}

// sortedRoutes is a small helper for deterministic snapshot ordering.
func sortedRoutes(routes map[string]Route) []Route {
	out := make([]Route, 0, len(routes))
	for _, rt := range routes {
		out = append(out, cloneRoute(rt))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Prefix.String() < out[j].Prefix.String()
	})
	return out
}
