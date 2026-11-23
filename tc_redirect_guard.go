package task

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Verdict is the TC action returned for a packet.
type Verdict int

const (
	VerdictDrop     Verdict = 0
	VerdictOK       Verdict = 1
	VerdictRedirect Verdict = 2
)

// Packet is the parsed packet metadata seen by the TC hook.
type Packet struct {
	SrcIP   string
	DstIP   string
	SrcPort uint16
	DstPort uint16
	Proto   uint8
	IfIndex int
}

// Policy is one redirect policy installed by the control plane.
type Policy struct {
	ID              string
	SrcIP           string
	DstIP           string
	SrcPort         uint16
	DstPort         uint16
	Proto           uint8
	Action          Verdict
	RedirectIfIndex int
	Revision        uint64
}

// MapKey is the lookup key of the redirect map.
type MapKey struct {
	SrcIP   string
	DstIP   string
	SrcPort uint16
	DstPort uint16
	Proto   uint8
}

// TcProgram is the compiled TC program description.
type TcProgram struct {
	Policies []Policy
	Ifaces   []int
	Helpers  []string
}

// GuardOptions configures the redirect guard.
type GuardOptions struct {
	AllowedIfIndexes []int
	MapCapacity      int
	ConntrackSync    bool
}

// Loader installs a compiled TC program.
type Loader func(*TcProgram) error

type flowRecord struct {
	target   int
	policyID string
}

// TcRedirectGuard owns the TC redirect policies and map state.
type TcRedirectGuard struct {
	mu sync.RWMutex

	allowed       map[int]struct{}
	mapCapacity   int
	conntrackSync bool

	policies  map[string]Policy
	revisions map[string]uint64
	entries   map[MapKey]int
	replies   map[MapKey]flowRecord

	attached    []int
	attachedSet map[int]struct{}

	loaded         bool
	blocking       bool
	activePrograms int

	policyVersion uint64
}

// NewTcRedirectGuard returns a guard configured by opts.
func NewTcRedirectGuard(opts GuardOptions) *TcRedirectGuard {
	g := &TcRedirectGuard{
		allowed:       make(map[int]struct{}, len(opts.AllowedIfIndexes)),
		mapCapacity:   opts.MapCapacity,
		conntrackSync: opts.ConntrackSync,
		policies:      make(map[string]Policy),
		revisions:     make(map[string]uint64),
		entries:       make(map[MapKey]int),
		replies:       make(map[MapKey]flowRecord),
		attachedSet:   make(map[int]struct{}),
	}
	for _, idx := range opts.AllowedIfIndexes {
		g.allowed[idx] = struct{}{}
	}
	return g
}

// AddPolicy installs a policy.
func (g *TcRedirectGuard) AddPolicy(p Policy) error {
	if p.ID == "" {
		return errors.New("policy ID must not be empty")
	}
	switch p.Action {
	case VerdictDrop, VerdictOK, VerdictRedirect:
	default:
		return fmt.Errorf("policy %q has invalid action %d", p.ID, p.Action)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if p.Action == VerdictRedirect && !g.ifIndexAllowedLocked(p.RedirectIfIndex) {
		return fmt.Errorf("policy %q redirects to disallowed ifindex %d", p.ID, p.RedirectIfIndex)
	}

	lastRevision, seenRevision := g.revisions[p.ID]
	if seenRevision {
		if p.Revision < lastRevision {
			return fmt.Errorf("policy %q revision %d is stale, latest is %d", p.ID, p.Revision, lastRevision)
		}
		if p.Revision == lastRevision {
			current, exists := g.policies[p.ID]
			if !exists || !samePolicy(current, p) {
				return fmt.Errorf("policy %q revision %d has already been used", p.ID, p.Revision)
			}
			return nil
		}
	}

	nextEntries := cloneEntries(g.entries)
	old, replacing := g.policies[p.ID]
	if replacing && old.Action == VerdictRedirect {
		oldKey := keyForPolicy(old)
		if target, ok := g.redirectTargetForKeyLocked(oldKey, p.ID); ok {
			nextEntries[oldKey] = target
		} else {
			delete(nextEntries, oldKey)
		}
	}
	if p.Action == VerdictRedirect {
		nextEntries[keyForPolicy(p)] = p.RedirectIfIndex
	}
	if g.mapCapacity > 0 && len(nextEntries) > g.mapCapacity {
		return fmt.Errorf("policy %q would exceed redirect map capacity %d", p.ID, g.mapCapacity)
	}

	g.policies[p.ID] = p
	g.revisions[p.ID] = p.Revision
	g.entries = nextEntries
	if replacing {
		g.deleteConntrackForPolicyLocked(p.ID)
	}
	g.policyVersion++
	return nil
}

// DeletePolicy removes a policy and its map entries.
func (g *TcRedirectGuard) DeletePolicy(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.policies[id]
	if !ok {
		return fmt.Errorf("policy %q does not exist", id)
	}
	delete(g.policies, id)

	if p.Action == VerdictRedirect {
		key := keyForPolicy(p)
		nextEntries := cloneEntries(g.entries)
		if target, exists := g.redirectTargetForKeyLocked(key, ""); exists {
			nextEntries[key] = target
		} else {
			delete(nextEntries, key)
		}
		g.entries = nextEntries
	}
	g.deleteConntrackForPolicyLocked(id)
	g.policyVersion++
	return nil
}

// SetAllowedIfIndexes replaces the set of interfaces redirects may target.
func (g *TcRedirectGuard) SetAllowedIfIndexes(idxs []int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	allowed := make(map[int]struct{}, len(idxs))
	for _, idx := range idxs {
		allowed[idx] = struct{}{}
	}
	g.allowed = allowed
	g.policyVersion++
}

// HookVerdict evaluates a packet at the TC hook.
func (g *TcRedirectGuard) HookVerdict(pkt Packet) (Verdict, int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.verdictLocked(pkt, true)
}

// KeyFor returns the map key of a packet.
func (g *TcRedirectGuard) KeyFor(pkt Packet) MapKey {
	return MapKey{
		SrcIP:   pkt.SrcIP,
		DstIP:   pkt.DstIP,
		SrcPort: pkt.SrcPort,
		DstPort: pkt.DstPort,
		Proto:   pkt.Proto,
	}
}

// UpdateMap atomically replaces the redirect map content.
func (g *TcRedirectGuard) UpdateMap(entries map[MapKey]int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.mapCapacity > 0 && len(entries) > g.mapCapacity {
		return fmt.Errorf("redirect map has %d entries, capacity is %d", len(entries), g.mapCapacity)
	}
	for _, ifindex := range entries {
		if !g.ifIndexAllowedLocked(ifindex) {
			return fmt.Errorf("redirect map contains disallowed ifindex %d", ifindex)
		}
	}

	nextEntries := make(map[MapKey]int, len(entries))
	for key, ifindex := range entries {
		nextEntries[key] = ifindex
	}

	g.entries = nextEntries
	return nil
}

// MapSnapshot returns a copy of the redirect map.
func (g *TcRedirectGuard) MapSnapshot() map[MapKey]int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return cloneEntries(g.entries)
}

// Attach attaches the program to the given interfaces.
func (g *TcRedirectGuard) Attach(ifaces []int) error {
	for _, ifindex := range ifaces {
		if ifindex <= 0 {
			return fmt.Errorf("invalid interface index %d", ifindex)
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, ifindex := range ifaces {
		if _, exists := g.attachedSet[ifindex]; exists {
			continue
		}
		g.attachedSet[ifindex] = struct{}{}
		g.attached = append(g.attached, ifindex)
	}
	g.policyVersion++
	return nil
}

// Attached lists the interfaces the program is attached to.
func (g *TcRedirectGuard) Attached() []int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return append([]int(nil), g.attached...)
}

// Compile builds the TC program description.
func (g *TcRedirectGuard) Compile() (*TcProgram, error) {
	program, _, err := g.compileSnapshot()
	return program, err
}

// LoadWith installs the program with the supplied loader.
func (g *TcRedirectGuard) LoadWith(l Loader) error {
	if l == nil {
		g.markLoadFailed()
		return errors.New("loader must not be nil")
	}

	program, version, err := g.compileSnapshot()
	if err != nil {
		g.markLoadFailed()
		return err
	}
	if err := l(program); err != nil {
		g.markLoadFailed()
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if version != g.policyVersion {
		g.loaded = false
		g.blocking = true
		g.activePrograms = 0
		return errors.New("policy state changed while loading TC program")
	}
	g.loaded = true
	g.blocking = false
	g.activePrograms = 1
	return nil
}

// Loaded reports whether a program is installed.
func (g *TcRedirectGuard) Loaded() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.loaded
}

// Blocking reports whether all traffic is dropped because no policy is loaded.
func (g *TcRedirectGuard) Blocking() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.blocking
}

// ActivePrograms reports how many programs are attached.
func (g *TcRedirectGuard) ActivePrograms() int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.activePrograms
}

// ChecksumRequired reports whether the packet needs checksum recomputation.
func (g *TcRedirectGuard) ChecksumRequired(pkt Packet) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	verdict, _, _ := g.verdictLocked(pkt, false)
	return verdict == VerdictRedirect
}

func (g *TcRedirectGuard) compileSnapshot() (*TcProgram, uint64, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	policies := make([]Policy, 0, len(g.policies))
	for _, p := range g.policies {
		policies = append(policies, p)
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].ID == policies[j].ID {
			return policies[i].Revision < policies[j].Revision
		}
		return policies[i].ID < policies[j].ID
	})

	return &TcProgram{
		Policies: policies,
		Ifaces:   append([]int(nil), g.attached...),
		Helpers: []string{
			"bpf_map_lookup_elem",
			"bpf_map_update_elem",
			"bpf_redirect",
			"bpf_skb_store_bytes",
			"bpf_l3_csum_replace",
			"bpf_l4_csum_replace",
		},
	}, g.policyVersion, nil
}

func (g *TcRedirectGuard) markLoadFailed() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.loaded = false
	g.blocking = true
	g.activePrograms = 0
}

func (g *TcRedirectGuard) verdictLocked(pkt Packet, track bool) (Verdict, int, error) {
	if g.blocking {
		return VerdictDrop, 0, nil
	}

	key := g.KeyFor(pkt)
	if ifindex, ok := g.entries[key]; ok {
		if !g.ifIndexAllowedLocked(ifindex) {
			return VerdictDrop, 0, nil
		}
		if track && g.conntrackSync {
			policyID := ""
			if p, exists := g.bestPolicyForKeyLocked(key); exists && p.Action == VerdictRedirect {
				policyID = p.ID
			}
			g.replies[reverseKey(key)] = flowRecord{
				target:   ifindex,
				policyID: policyID,
			}
		}
		return VerdictRedirect, ifindex, nil
	}

	if flow, ok := g.replies[key]; ok {
		if !g.ifIndexAllowedLocked(flow.target) {
			return VerdictDrop, 0, nil
		}
		return VerdictRedirect, flow.target, nil
	}

	if p, ok := g.bestPolicyForKeyLocked(key); ok {
		switch p.Action {
		case VerdictOK:
			return VerdictOK, 0, nil
		case VerdictDrop:
			return VerdictDrop, 0, nil
		}
	}
	return VerdictDrop, 0, nil
}

func (g *TcRedirectGuard) ifIndexAllowedLocked(ifindex int) bool {
	_, ok := g.allowed[ifindex]
	return ok
}

func (g *TcRedirectGuard) bestPolicyForKeyLocked(key MapKey) (Policy, bool) {
	var best Policy
	found := false
	for _, p := range g.policies {
		if keyForPolicy(p) != key {
			continue
		}
		if !found || p.Revision > best.Revision || (p.Revision == best.Revision && p.ID < best.ID) {
			best = p
			found = true
		}
	}
	return best, found
}

func (g *TcRedirectGuard) redirectTargetForKeyLocked(key MapKey, excludeID string) (int, bool) {
	var best Policy
	found := false
	for id, p := range g.policies {
		if id == excludeID || p.Action != VerdictRedirect || keyForPolicy(p) != key {
			continue
		}
		if !found || p.Revision > best.Revision || (p.Revision == best.Revision && p.ID < best.ID) {
			best = p
			found = true
		}
	}
	if !found {
		return 0, false
	}
	return best.RedirectIfIndex, true
}

func (g *TcRedirectGuard) deleteConntrackForPolicyLocked(id string) {
	for key, flow := range g.replies {
		if flow.policyID == id {
			delete(g.replies, key)
		}
	}
}

func keyForPolicy(p Policy) MapKey {
	return MapKey{
		SrcIP:   p.SrcIP,
		DstIP:   p.DstIP,
		SrcPort: p.SrcPort,
		DstPort: p.DstPort,
		Proto:   p.Proto,
	}
}

func reverseKey(key MapKey) MapKey {
	return MapKey{
		SrcIP:   key.DstIP,
		DstIP:   key.SrcIP,
		SrcPort: key.DstPort,
		DstPort: key.SrcPort,
		Proto:   key.Proto,
	}
}

func cloneEntries(entries map[MapKey]int) map[MapKey]int {
	cloned := make(map[MapKey]int, len(entries))
	for key, ifindex := range entries {
		cloned[key] = ifindex
	}
	return cloned
}

func samePolicy(a, b Policy) bool {
	return a == b
}
