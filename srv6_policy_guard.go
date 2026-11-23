package task

import (
	"errors"
	"fmt"
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
	addr, err := netip.ParseAddr(sid.Value)
	if err != nil {
		return VerdictUnknown, fmt.Errorf("invalid SID %q: %w", sid.Value, err)
	}
	locator, err := netip.ParsePrefix(sid.Locator)
	if err != nil {
		return VerdictUnknown, fmt.Errorf("invalid locator %q: %w", sid.Locator, err)
	}
	if !locator.Contains(addr) {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckListLen rejects segment lists longer than the hardware limit.
func (g *Srv6PolicyGuard) CheckListLen(n, hardMax int) (Verdict, error) {
	if n < 0 || hardMax < 0 {
		return VerdictUnknown, fmt.Errorf("negative length: n=%d hardMax=%d", n, hardMax)
	}
	if n > hardMax {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckExpansionTable verifies that every compressed SID in the segment list
// has an entry in the decompression table, so the controller and the data
// plane never disagree about what a compressed SID expands to.
func (g *Srv6PolicyGuard) CheckExpansionTable(compressed []string, table map[string]string) (Verdict, error) {
	for _, c := range compressed {
		if _, ok := table[c]; !ok {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// ResolveBehavior maps an endpoint behavior to the action the data plane must
// take. End.DT4 decapsulates and performs an IPv4 table lookup; End.DX4
// decapsulates and cross-connects to a specific interface. End.DT4 applied to
// a non-IPv4 destination is refused.
func (g *Srv6PolicyGuard) ResolveBehavior(b Behavior, dstIsIPv4 bool) (Action, error) {
	switch b {
	case BehaviorEndDT4:
		if !dstIsIPv4 {
			return ActionUnknown, errors.New("End.DT4 requires an IPv4 destination")
		}
		return ActionDecapLookup, nil
	case BehaviorEndDX4:
		return ActionCrossConnect, nil
	default:
		return ActionUnknown, fmt.Errorf("unknown behavior %d", b)
	}
}

// CheckBSIDConflict rejects binding a BSID that is already bound to a
// different policy. Rebinding the same policy is idempotent and allowed.
func (g *Srv6PolicyGuard) CheckBSIDConflict(bindings map[string]string, bsid, policy string) (Verdict, error) {
	if owner, ok := bindings[bsid]; ok && owner != policy {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// DetectLoop rejects a candidate path that revisits a node already present in
// the visited set.
func (g *Srv6PolicyGuard) DetectLoop(candidate, visited []string) (Verdict, error) {
	seen := make(map[string]struct{}, len(visited)+len(candidate))
	for _, node := range visited {
		seen[node] = struct{}{}
	}
	for _, node := range candidate {
		if _, ok := seen[node]; ok {
			return VerdictDeny, nil
		}
		seen[node] = struct{}{}
	}
	return VerdictAllow, nil
}

// DedupSegments removes repeated segments while preserving the first
// occurrence order.
func (g *Srv6PolicyGuard) DedupSegments(sids []string) ([]string, error) {
	seen := make(map[string]struct{}, len(sids))
	out := make([]string, 0, len(sids))
	for _, sid := range sids {
		if _, ok := seen[sid]; ok {
			continue
		}
		seen[sid] = struct{}{}
		out = append(out, sid)
	}
	return out, nil
}

// EncapHeader builds the outer header for a segment list. The outer TTL must
// be an explicit value and never inherited from the inner packet: a configured
// TTL of zero falls back to 64.
func (g *Srv6PolicyGuard) EncapHeader(segments []string, configuredTTL int) (Header, error) {
	if len(segments) == 0 {
		return Header{}, errors.New("segment list is empty")
	}
	ttl := configuredTTL
	if ttl <= 0 {
		ttl = 64
	}
	if ttl > 255 {
		return Header{}, fmt.Errorf("TTL %d out of range", configuredTTL)
	}
	segs := make([]string, len(segments))
	copy(segs, segments)
	return Header{OuterTTL: ttl, Segments: segs}, nil
}

// VerifyHMAC checks the SRH HMAC: it must be present and valid.
func (g *Srv6PolicyGuard) VerifyHMAC(h HMACInfo) (Verdict, error) {
	if !h.Present || !h.Valid {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// ReleaseSIDs decrements the reference count of every SID used by a deleted
// policy. Counts never go below zero and unrelated SIDs are untouched.
func (g *Srv6PolicyGuard) ReleaseSIDs(refs map[string]int, sids []string) (map[string]int, error) {
	out := make(map[string]int, len(refs))
	for sid, n := range refs {
		out[sid] = n
	}
	for _, sid := range sids {
		if n, ok := out[sid]; ok && n > 0 {
			out[sid] = n - 1
		}
	}
	return out, nil
}

// RestorePolicies rebuilds the policy table from persisted state after a
// controller restart.
func (g *Srv6PolicyGuard) RestorePolicies(saved []Policy) (map[string]Policy, error) {
	policies := make(map[string]Policy, len(saved))
	for _, p := range saved {
		if p.Name == "" {
			return nil, errors.New("persisted policy with empty name")
		}
		if _, ok := policies[p.Name]; ok {
			return nil, fmt.Errorf("duplicate policy %q in persisted state", p.Name)
		}
		policies[p.Name] = p
	}
	return policies, nil
}

// CheckTenantSIDSpace rejects a SID that is already owned by a different
// tenant, so the per-tenant SID spaces stay disjoint.
func (g *Srv6PolicyGuard) CheckTenantSIDSpace(owners map[string]string, tenant, sid string) (Verdict, error) {
	if owner, ok := owners[sid]; ok && owner != tenant {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}
