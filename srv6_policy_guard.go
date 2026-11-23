package task

import "errors"

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// Behavior is the SRv6 endpoint behavior bound to a SID.
type Behavior int

const (
	BehaviorUnknown Behavior = iota
	BehaviorEndDT4
	BehaviorEndDX4
)

// Action is the forwarding action the data plane has to perform.
type Action int

const (
	ActionUnknown Action = iota
	ActionDecapLookup
	ActionCrossConnect
	ActionDrop
)

// SID is one segment identifier together with the locator it claims to belong
// to. Locator is written as a CIDR string, for example "2001:db8:1::/48".
type SID struct {
	Value   string
	Locator string
}

// Policy is one SRv6 traffic steering policy.
type Policy struct {
	Name     string
	Tenant   string
	BSID     string
	Segments []string
	Behavior Behavior
	HMAC     HMACInfo
	OuterTTL int
}

// HMACInfo describes the SRH HMAC of a policy.
type HMACInfo struct {
	Present bool
	Valid   bool
}

// Header is the outer SRv6 encapsulation header built for a segment list.
type Header struct {
	OuterTTL int
	Segments []string
}

// Srv6PolicyGuard validates SRv6 policies before they are programmed into the
// data plane. It holds no state of its own.
type Srv6PolicyGuard struct{}

// ValidateSIDOwner checks that Value falls inside the locator the SID claims,
// so a controller cannot program a SID owned by another node.
func (g *Srv6PolicyGuard) ValidateSIDOwner(sid SID) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckListLen rejects segment lists longer than the hardware limit.
func (g *Srv6PolicyGuard) CheckListLen(n, hardMax int) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckExpansionTable verifies that every compressed SID in the segment list
// has an entry in the decompression table, so the controller and the data
// plane never disagree about what a compressed SID expands to.
func (g *Srv6PolicyGuard) CheckExpansionTable(compressed []string, table map[string]string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// ResolveBehavior maps an endpoint behavior to the action the data plane must
// take. End.DT4 decapsulates and performs an IPv4 table lookup; End.DX4
// decapsulates and cross-connects to a specific interface. End.DT4 applied to
// a non-IPv4 destination is refused.
func (g *Srv6PolicyGuard) ResolveBehavior(b Behavior, dstIsIPv4 bool) (Action, error) {
	return ActionUnknown, errors.New("not implemented")
}

// CheckBSIDConflict rejects binding a BSID that is already bound to a
// different policy. Rebinding the same policy is idempotent and allowed.
func (g *Srv6PolicyGuard) CheckBSIDConflict(bindings map[string]string, bsid, policy string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// DetectLoop rejects a candidate path that revisits a node already present in
// the visited set.
func (g *Srv6PolicyGuard) DetectLoop(candidate, visited []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// DedupSegments removes repeated segments while preserving the first
// occurrence order.
func (g *Srv6PolicyGuard) DedupSegments(sids []string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// EncapHeader builds the outer header for a segment list. The outer TTL must
// be an explicit value and never inherited from the inner packet: a configured
// TTL of zero falls back to 64.
func (g *Srv6PolicyGuard) EncapHeader(segments []string, configuredTTL int) (Header, error) {
	return Header{}, errors.New("not implemented")
}

// VerifyHMAC checks the SRH HMAC: it must be present and valid.
func (g *Srv6PolicyGuard) VerifyHMAC(h HMACInfo) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// ReleaseSIDs decrements the reference count of every SID used by a deleted
// policy. Counts never go below zero and unrelated SIDs are untouched.
func (g *Srv6PolicyGuard) ReleaseSIDs(refs map[string]int, sids []string) (map[string]int, error) {
	return nil, errors.New("not implemented")
}

// RestorePolicies rebuilds the policy table from persisted state after a
// controller restart.
func (g *Srv6PolicyGuard) RestorePolicies(saved []Policy) (map[string]Policy, error) {
	return nil, errors.New("not implemented")
}

// CheckTenantSIDSpace rejects a SID that is already owned by a different
// tenant, so the per-tenant SID spaces stay disjoint.
func (g *Srv6PolicyGuard) CheckTenantSIDSpace(owners map[string]string, tenant, sid string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}
