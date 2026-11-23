package task

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
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

const defaultMACFlapWindow = 250 * time.Millisecond

// MACFlapSuppressionWindow is the interval during which repeated migrations
// of the same MAC are recorded but not installed.
var MACFlapSuppressionWindow = defaultMACFlapWindow

type macFlapState struct {
	origin   string
	pending  string
	lastMove time.Time
	moved    bool
}

// EvpnRouteGuard validates EVPN route installation. Route tables are passed
// in and returned out; only MAC migration damping is kept internally.
type EvpnRouteGuard struct {
	mu      sync.Mutex
	macFlap map[string]macFlapState
}

// CheckTenantIsolation verifies that rd belongs to tenant according to
// rdOwners (RD -> tenant). A route carrying another tenant's RD is denied.
func (g *EvpnRouteGuard) CheckTenantIsolation(tenant, rd string, rdOwners map[string]string) (Verdict, error) {
	if tenant == "" || rd == "" {
		return VerdictDeny, nil
	}
	owner, ok := rdOwners[rd]
	if !ok || owner != tenant {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckVNIVRF verifies that the MAC route's VNI maps to the VRF it claims,
// using mapping (VNI -> VRF).
func (g *EvpnRouteGuard) CheckVNIVRF(vni, vrf string, mapping map[string]string) (Verdict, error) {
	if vni == "" || vrf == "" {
		return VerdictDeny, nil
	}
	mapped, ok := mapping[vni]
	if !ok || mapped != vrf {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// SelectMACOrigin picks the winning origin for one MAC when several VTEPs
// reported it: the earliest learning event (lowest Seq) wins, a later report
// must not overwrite it.
func (g *EvpnRouteGuard) SelectMACOrigin(entries []MACEntry) (MACEntry, error) {
	if len(entries) == 0 {
		return MACEntry{}, errors.New("no MAC origin candidates")
	}
	winner := entries[0]
	for _, entry := range entries[1:] {
		if entry.Seq < winner.Seq {
			winner = entry
		}
	}
	return winner, nil
}

// CheckARPProxy verifies that the IP/MAC pair in an ARP proxy reply matches
// the binding table (IP -> MAC).
func (g *EvpnRouteGuard) CheckARPProxy(bindings map[string]string, ip, mac string) (Verdict, error) {
	if ip == "" || mac == "" {
		return VerdictDeny, nil
	}
	if boundMAC, ok := bindings[ip]; ok {
		if strings.EqualFold(strings.TrimSpace(boundMAC), strings.TrimSpace(mac)) {
			return VerdictAllow, nil
		}
		return VerdictDeny, nil
	}
	wantIP := canonicalAddr(ip)
	for boundIP, boundMAC := range bindings {
		if canonicalAddr(boundIP) == wantIP &&
			strings.EqualFold(strings.TrimSpace(boundMAC), strings.TrimSpace(mac)) {
			return VerdictAllow, nil
		}
	}
	return VerdictDeny, nil
}

// LongestMatchPrefix returns the most specific prefix in table that covers
// target.
func (g *EvpnRouteGuard) LongestMatchPrefix(table []netip.Prefix, target netip.Addr) (netip.Prefix, error) {
	if !target.IsValid() {
		return netip.Prefix{}, errors.New("invalid target address")
	}
	var best netip.Prefix
	found := false
	for _, prefix := range table {
		if !prefix.IsValid() || !prefix.Contains(target) {
			continue
		}
		if !found || prefix.Bits() > best.Bits() {
			best = prefix
			found = true
		}
	}
	if !found {
		return netip.Prefix{}, fmt.Errorf("no prefix covers %s", target)
	}
	return best, nil
}

// RevokeEverywhere removes key from both the local and the peer table and
// reports how many entries were removed in total.
func (g *EvpnRouteGuard) RevokeEverywhere(local, peer map[string]EVPNRoute, key string) (int, error) {
	removed := 0
	if _, ok := local[key]; ok {
		delete(local, key)
		removed++
	}
	if _, ok := peer[key]; ok {
		delete(peer, key)
		removed++
	}
	return removed, nil
}

// ElectDF elects the designated forwarder for a multi-homed Ethernet segment.
// The member with the lowest address wins, so the result is deterministic
// across controllers.
func (g *EvpnRouteGuard) ElectDF(members []string) (string, error) {
	if len(members) == 0 {
		return "", errors.New("no designated forwarder candidates")
	}
	var winner string
	var winnerAddr netip.Addr
	for i, member := range members {
		addr, err := netip.ParseAddr(strings.TrimSpace(member))
		if err != nil {
			return "", fmt.Errorf("invalid DF candidate %q: %w", member, err)
		}
		if i == 0 || addr.Compare(winnerAddr) < 0 {
			winner = member
			winnerAddr = addr
		}
	}
	return winner, nil
}

// DedupAddresses removes repeated addresses while preserving first occurrence
// order.
func (g *EvpnRouteGuard) DedupAddresses(addrs []string) ([]string, error) {
	if addrs == nil {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(addrs))
	unique := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		unique = append(unique, addr)
	}
	return unique, nil
}

// RestoreTable rebuilds the route table from persisted state after a
// controller restart. MAC routes are keyed by MAC, prefix routes by the prefix
// string.
func (g *EvpnRouteGuard) RestoreTable(saved []EVPNRoute) (map[string]EVPNRoute, error) {
	table := make(map[string]EVPNRoute, len(saved))
	for _, rt := range saved {
		if err := upsert(table, rt); err != nil {
			return nil, err
		}
	}
	return table, nil
}

// WithdrawPeer removes every route learned from peer and returns the removed
// keys, as required when the BGP session goes down.
func (g *EvpnRouteGuard) WithdrawPeer(table map[string]EVPNRoute, peer string) ([]string, error) {
	removed := make([]string, 0)
	for key, rt := range table {
		if rt.VTEP != peer {
			continue
		}
		delete(table, key)
		removed = append(removed, key)
	}
	sort.Strings(removed)
	return removed, nil
}

// ApplyUpdate installs rt into table. MAC routes are keyed by MAC and prefix
// routes by the prefix string. An update whose Seq is lower than the stored
// route's Seq must not overwrite it.
func (g *EvpnRouteGuard) ApplyUpdate(table map[string]EVPNRoute, rt EVPNRoute) (map[string]EVPNRoute, error) {
	updated := cloneTable(table)
	key, err := routeKey(rt)
	if err != nil {
		return updated, err
	}
	old, exists := updated[key]
	if exists && rt.Seq <= old.Seq {
		return updated, nil
	}
	if rt.Type == RouteTypeMAC {
		if exists && old.VTEP != rt.VTEP && !g.allowMACMove(key, old.VTEP, rt.VTEP) {
			return updated, nil
		}
		if !exists {
			g.noteMACOrigin(key, rt.VTEP)
		}
	}
	updated[key] = rt
	return updated, nil
}

// CheckRTImport verifies that the route's RT list is covered by policy.
// Wildcards are not accepted: policy entries must match exactly.
func (g *EvpnRouteGuard) CheckRTImport(rt []string, policy []string) (Verdict, error) {
	if len(rt) == 0 || len(policy) == 0 {
		return VerdictDeny, nil
	}
	allowed := make(map[string]struct{}, len(policy))
	for _, target := range policy {
		target = strings.TrimSpace(target)
		if target == "" || hasWildcard(target) {
			return VerdictDeny, nil
		}
		allowed[target] = struct{}{}
	}
	for _, target := range rt {
		target = strings.TrimSpace(target)
		if target == "" || hasWildcard(target) {
			return VerdictDeny, nil
		}
		if _, ok := allowed[target]; !ok {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

func canonicalAddr(value string) string {
	if addr, err := netip.ParseAddr(strings.TrimSpace(value)); err == nil {
		return addr.String()
	}
	return strings.TrimSpace(value)
}

func hasWildcard(value string) bool {
	return strings.ContainsAny(value, "*?")
}

func routeKey(rt EVPNRoute) (string, error) {
	routeType := rt.Type
	if routeType == RouteTypeUnknown {
		switch {
		case rt.MAC != "":
			routeType = RouteTypeMAC
		case rt.Prefix.IsValid():
			routeType = RouteTypePrefix
		}
	}
	switch routeType {
	case RouteTypeMAC:
		if rt.MAC == "" {
			return "", errors.New("MAC route has no MAC address")
		}
		return rt.MAC, nil
	case RouteTypePrefix:
		if !rt.Prefix.IsValid() {
			return "", errors.New("prefix route has no valid prefix")
		}
		return rt.Prefix.Masked().String(), nil
	default:
		return "", errors.New("unknown EVPN route type")
	}
}

func cloneTable(table map[string]EVPNRoute) map[string]EVPNRoute {
	clone := make(map[string]EVPNRoute, len(table)+1)
	for key, rt := range table {
		clone[key] = rt
	}
	return clone
}

func upsert(table map[string]EVPNRoute, rt EVPNRoute) error {
	key, err := routeKey(rt)
	if err != nil {
		return err
	}
	if old, ok := table[key]; ok && rt.Seq <= old.Seq {
		return nil
	}
	table[key] = rt
	return nil
}

func (g *EvpnRouteGuard) noteMACOrigin(key, vtep string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.macFlap == nil {
		g.macFlap = make(map[string]macFlapState)
	}
	state, ok := g.macFlap[key]
	if !ok || state.origin != vtep {
		g.macFlap[key] = macFlapState{origin: vtep}
	}
}

func (g *EvpnRouteGuard) allowMACMove(key, oldVTEP, newVTEP string) bool {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.macFlap == nil {
		g.macFlap = make(map[string]macFlapState)
	}
	state, ok := g.macFlap[key]
	if !ok || state.origin != oldVTEP {
		g.macFlap[key] = macFlapState{origin: newVTEP, lastMove: now, moved: true}
		return true
	}
	if !state.moved || now.Sub(state.lastMove) >= MACFlapSuppressionWindow {
		g.macFlap[key] = macFlapState{origin: newVTEP, lastMove: now, moved: true}
		return true
	}
	state.pending = newVTEP
	g.macFlap[key] = state
	return false
}
