package task

import (
	"fmt"
	"sync"
)

// Reject reasons emitted by the admission chain. They are stable machine
// readable codes so each denial can be replayed per prefix, peer and reason.
const (
	ReasonASPathMalformed        = "as_path_malformed_or_peer_mismatch"
	ReasonPrivateASInPath        = "private_as_in_path"
	ReasonCommunityNotAllowed    = "community_not_allowlisted"
	ReasonRPKINotValid           = "rpki_state_not_valid"
	ReasonASSetInPath            = "as_set_in_path"
	ReasonPrependCountMismatch   = "prepend_count_mismatch"
	ReasonDefaultRouteNotAllowed = "default_route_not_allowed"
	ReasonFlapDampeningSuppress  = "route_flap_dampening_suppressed"
	ReasonPrefixLimitExceeded    = "prefix_limit_exceeded"
	ReasonStaleSeqUpdate         = "stale_seq_update"
)

// AuditEntry is one per-reason denial record. A single rejected route can
// produce several entries, one for every failed check.
type AuditEntry struct {
	Prefix string
	Peer   string
	Reason string
	Seq    uint64
}

// AuditLog keeps every denial individually; aggregate counts are derived from
// the entries instead of replacing them.
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// Record stores a denial entry and returns a copy of it.
func (l *AuditLog) Record(prefix, peer, reason string, seq uint64) AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := AuditEntry{Prefix: prefix, Peer: peer, Reason: reason, Seq: seq}
	l.entries = append(l.entries, entry)
	return entry
}

// Entries returns a copy of every recorded denial.
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AuditEntry(nil), l.entries...)
}

// ForPrefix returns the recorded denials for one prefix, optionally filtered
// by peer. Pass an empty peer to keep every peer.
func (l *AuditLog) ForPrefix(prefix, peer string) []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []AuditEntry
	for _, entry := range l.entries {
		if entry.Prefix != prefix {
			continue
		}
		if peer != "" && entry.Peer != peer {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// CountByReason returns per-reason denial counts for reporting.
func (l *AuditLog) CountByReason() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	counts := make(map[string]int)
	for _, entry := range l.entries {
		counts[entry.Reason]++
	}
	return counts
}

// Policy is the per-guard import configuration consumed by Admit.
type Policy struct {
	PeerAS             uint32
	AllowPrivate       bool
	CommunityAllowList []uint32
	AllowDefault       bool
}

// Admit runs the full stateless import policy chain against rt and returns the
// overall verdict plus the reason code of every individual failed check. Any
// check that errors fails closed (deny) and the error is returned alongside.
func (g *RouteGuard) Admit(rt Route, policy Policy, now, halfLife int64,
	flaps *FlapHistory, audit *AuditLog) (Verdict, []string, error) {

	reasons := make([]string, 0)
	record := func(reason string) {
		reasons = append(reasons, reason)
		if audit != nil {
			audit.Record(rt.Prefix.String(), rt.Peer, reason, rt.Seq)
		}
	}

	verdict, err := g.ValidateASPath(rt.Attrs.ASPath, policy.PeerAS)
	if err != nil {
		return VerdictUnknown, nil, fmt.Errorf("validate AS path: %w", err)
	}
	if verdict != VerdictAllow {
		record(ReasonASPathMalformed)
	}

	verdict, err = g.CheckPrivateAS(rt.Attrs.ASPath, policy.AllowPrivate)
	if err != nil {
		return VerdictUnknown, nil, fmt.Errorf("check private AS: %w", err)
	}
	if verdict != VerdictAllow {
		record(ReasonPrivateASInPath)
	}

	verdict, err = g.CheckCommunities(rt.Attrs.Communities, policy.CommunityAllowList)
	if err != nil {
		return VerdictUnknown, nil, fmt.Errorf("check communities: %w", err)
	}
	if verdict != VerdictAllow {
		record(ReasonCommunityNotAllowed)
	}

	verdict, err = g.CheckRPKI(rt.Attrs.RPKIState)
	if err != nil {
		return VerdictUnknown, nil, fmt.Errorf("check RPKI: %w", err)
	}
	if verdict != VerdictAllow {
		record(ReasonRPKINotValid)
	}

	verdict, err = g.CheckASSet(rt.Attrs.HasASSet)
	if err != nil {
		return VerdictUnknown, nil, fmt.Errorf("check AS_SET: %w", err)
	}
	if verdict != VerdictAllow {
		record(ReasonASSetInPath)
	}

	verdict, err = g.CheckPrepend(rt.Attrs.ASPath, rt.Attrs.PrependCount)
	if err != nil {
		return VerdictUnknown, nil, fmt.Errorf("check prepend: %w", err)
	}
	if verdict != VerdictAllow {
		record(ReasonPrependCountMismatch)
	}

	verdict, err = g.CheckDefault(rt.Prefix, policy.AllowDefault)
	if err != nil {
		return VerdictUnknown, nil, fmt.Errorf("check default route: %w", err)
	}
	if verdict != VerdictAllow {
		record(ReasonDefaultRouteNotAllowed)
	}

	if flaps != nil && halfLife > 0 {
		verdict, err = g.FlapDamp(flaps.History(rt.Prefix.String()), now, halfLife)
		if err != nil {
			return VerdictUnknown, nil, fmt.Errorf("flap dampening: %w", err)
		}
		if verdict != VerdictAllow {
			record(ReasonFlapDampeningSuppress)
		}
	}

	if len(reasons) > 0 {
		return VerdictDeny, reasons, nil
	}
	return VerdictAllow, nil, nil
}

// FlapHistory stores per-prefix flap event timestamps across updates so the
// damping penalty survives between individual Admit calls.
type FlapHistory struct {
	mu     sync.Mutex
	events map[string][]int64
}

// Append records a flap event for prefix.
func (h *FlapHistory) Append(prefix string, at int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.events == nil {
		h.events = make(map[string][]int64)
	}
	h.events[prefix] = append(h.events[prefix], at)
}

// History returns a copy of the flap timestamps recorded for prefix.
func (h *FlapHistory) History(prefix string) []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]int64(nil), h.events[prefix]...)
}
