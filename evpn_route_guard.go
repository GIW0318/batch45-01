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

// RouteType distinguishes EVPN route types.
type RouteType int

const (
	RouteTypeUnknown RouteType = iota
	RouteTypeMAC
	RouteTypePrefix
)

// EVPNRoute is one EVPN route as received from a peer.
type EVPNRoute struct {
	Tenant string
	RD     string
	RT     []string
	Type   RouteType
	MAC    string
	IP     string
	VNI    string
	VRF    string
	Prefix netip.Prefix
	VTEP   string
	ESI    string
	Seq    uint64
}

// MACEntry is one MAC learning event reported by a VTEP.
type MACEntry struct {
	MAC  string
	VTEP string
	Seq  uint64
}

// EvpnRouteGuard validates EVPN route installation. It holds no state of its
// own; tables are passed in and returned out.
type EvpnRouteGuard struct{}

// CheckTenantIsolation verifies that rd belongs to tenant according to
// rdOwners (RD -> tenant). A route carrying another tenant's RD is denied.
func (g *EvpnRouteGuard) CheckTenantIsolation(tenant, rd string, rdOwners map[string]string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckVNIVRF verifies that the MAC route's VNI maps to the VRF it claims,
// using mapping (VNI -> VRF).
func (g *EvpnRouteGuard) CheckVNIVRF(vni, vrf string, mapping map[string]string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// SelectMACOrigin picks the winning origin for one MAC when several VTEPs
// reported it: the earliest learning event (lowest Seq) wins, a later report
// must not overwrite it.
func (g *EvpnRouteGuard) SelectMACOrigin(entries []MACEntry) (MACEntry, error) {
	return MACEntry{}, errors.New("not implemented")
}

// CheckARPProxy verifies that the IP/MAC pair in an ARP proxy reply matches
// the binding table (IP -> MAC).
func (g *EvpnRouteGuard) CheckARPProxy(bindings map[string]string, ip, mac string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// LongestMatchPrefix returns the most specific prefix in table that covers
// target.
func (g *EvpnRouteGuard) LongestMatchPrefix(table []netip.Prefix, target netip.Addr) (netip.Prefix, error) {
	return netip.Prefix{}, errors.New("not implemented")
}

// RevokeEverywhere removes key from both the local and the peer table and
// reports how many entries were removed in total.
func (g *EvpnRouteGuard) RevokeEverywhere(local, peer map[string]EVPNRoute, key string) (int, error) {
	return 0, errors.New("not implemented")
}

// ElectDF elects the designated forwarder for a multi-homed Ethernet segment.
// The member with the lowest address wins, so the result is deterministic
// across controllers.
func (g *EvpnRouteGuard) ElectDF(members []string) (string, error) {
	return "", errors.New("not implemented")
}

// DedupAddresses removes repeated addresses while preserving first occurrence
// order.
func (g *EvpnRouteGuard) DedupAddresses(addrs []string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// RestoreTable rebuilds the route table from persisted state after a
// controller restart. MAC routes are keyed by MAC, prefix routes by the prefix
// string.
func (g *EvpnRouteGuard) RestoreTable(saved []EVPNRoute) (map[string]EVPNRoute, error) {
	return nil, errors.New("not implemented")
}

// WithdrawPeer removes every route learned from peer and returns the removed
// keys, as required when the BGP session goes down.
func (g *EvpnRouteGuard) WithdrawPeer(table map[string]EVPNRoute, peer string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// ApplyUpdate installs rt into table. MAC routes are keyed by MAC and prefix
// routes by the prefix string. An update whose Seq is lower than the stored
// route's Seq must not overwrite it.
func (g *EvpnRouteGuard) ApplyUpdate(table map[string]EVPNRoute, rt EVPNRoute) (map[string]EVPNRoute, error) {
	return nil, errors.New("not implemented")
}

// CheckRTImport verifies that the route's RT list is covered by policy.
// Wildcards are not accepted: policy entries must match exactly.
func (g *EvpnRouteGuard) CheckRTImport(rt []string, policy []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}
