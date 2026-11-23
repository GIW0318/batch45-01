package task

import (
	"errors"
	"net/netip"
	"sync"
)

// ErrPrefixLimitExceeded is returned when an update would push a peer over its
// hard prefix quota before anything is installed.
var ErrPrefixLimitExceeded = errors.New("peer prefix limit exceeded")

// ErrStaleSeq is returned when an update carries a Seq lower than the route
// already stored for the same prefix.
var ErrStaleSeq = errors.New("stale seq update")

// RouteTable is the concurrency-safe live forwarding table. All reads and
// writes go through one mutex, so readers either observe the table before a
// write or the fully committed table after it; a half-applied update is never
// visible.
type RouteTable struct {
	mu        sync.RWMutex
	routes    map[string]Route
	peerCount map[string]int
	limits    map[string]int
	audit     *AuditLog
}

// NewRouteTable builds an empty table. Per-peer hard prefix limits are passed
// as peer -> maximum number of prefixes.
func NewRouteTable(peerLimits map[string]int, audit *AuditLog) *RouteTable {
	limits := make(map[string]int, len(peerLimits))
	for peer, limit := range peerLimits {
		limits[peer] = limit
	}
	return &RouteTable{
		routes:    make(map[string]Route),
		peerCount: make(map[string]int),
		limits:    limits,
		audit:     audit,
	}
}

// Update applies rt under the table lock as an atomic read-modify-write. The
// hard quota is checked before any install, so an over-limit route never
// appears in the table even transiently. Refreshing a prefix the peer already
// owns does not consume extra quota. A lower Seq is rejected.
func (t *RouteTable) Update(rt Route) error {
	if !rt.Prefix.IsValid() {
		return errors.New("route has no valid prefix")
	}
	key := rt.Prefix.String()

	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.routes[key]; ok && existing.Seq > rt.Seq {
		t.record(rt, ReasonStaleSeqUpdate)
		return ErrStaleSeq
	}

	if limit, ok := t.limits[rt.Peer]; ok {
		_, ownsPrefix := t.routesOwnedByPeerLocked(rt.Peer, key)
		projected := t.peerCount[rt.Peer]
		if !ownsPrefix {
			projected++
		}
		if projected > limit {
			t.record(rt, ReasonPrefixLimitExceeded)
			return ErrPrefixLimitExceeded
		}
	}

	if existing, ok := t.routes[key]; ok {
		if existing.Peer != rt.Peer {
			t.peerCount[existing.Peer]--
			if t.peerCount[existing.Peer] <= 0 {
				delete(t.peerCount, existing.Peer)
			}
			t.peerCount[rt.Peer]++
		}
	} else {
		t.peerCount[rt.Peer]++
	}
	t.routes[key] = cloneRoute(rt)
	return nil
}

// routesOwnedByPeerLocked reports whether the current entry for key belongs to
// peer. It must be called with the write lock held.
func (t *RouteTable) routesOwnedByPeerLocked(peer, key string) (Route, bool) {
	existing, ok := t.routes[key]
	if !ok || existing.Peer != peer {
		return Route{}, false
	}
	return existing, true
}

func (t *RouteTable) record(rt Route, reason string) {
	if t.audit != nil {
		t.audit.Record(rt.Prefix.String(), rt.Peer, reason, rt.Seq)
	}
}

// Get returns a copy of the route stored for prefix and whether it exists.
func (t *RouteTable) Get(prefix string) (Route, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	rt, ok := t.routes[prefix]
	if !ok {
		return Route{}, false
	}
	return cloneRoute(rt), true
}

// Len returns the number of installed prefixes.
func (t *RouteTable) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.routes)
}

// PeerCount returns the number of prefixes currently installed for peer.
func (t *RouteTable) PeerCount(peer string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.peerCount[peer]
}

// Snapshot returns a deep, deterministically ordered copy of the table for
// persistence.
func (t *RouteTable) Snapshot() []Route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return sortedRoutes(t.routes)
}

// Persist is the on-disk shape of one table entry. It uses value types only so
// snapshots can be stored independently of the live table.
type Persist struct {
	Prefix    string
	Peer      string
	LocalPref int
	Seq       uint64
	Attrs     PathAttrs
}

// Persist renders the table as a plain snapshot slice that survives a restart.
func (t *RouteTable) Persist() []Persist {
	routes := t.Snapshot()
	out := make([]Persist, 0, len(routes))
	for _, rt := range routes {
		out = append(out, Persist{
			Prefix:    rt.Prefix.String(),
			Peer:      rt.Peer,
			LocalPref: rt.LocalPref,
			Seq:       rt.Seq,
			Attrs:     rt.Attrs,
		})
	}
	return out
}

// RestoreTable rebuilds a RouteTable from a persisted snapshot, mirroring the
// post-restart path: every persisted route is revalidated against the quota
// and duplicate prefixes keep the highest Seq.
func RestoreTable(snapshot []Persist, peerLimits map[string]int, audit *AuditLog) (*RouteTable, error) {
	routes := make([]Route, 0, len(snapshot))
	for _, entry := range snapshot {
		prefix, err := parsePersistedPrefix(entry.Prefix)
		if err != nil {
			return nil, err
		}
		routes = append(routes, Route{
			Prefix:    prefix,
			Peer:      entry.Peer,
			LocalPref: entry.LocalPref,
			Seq:       entry.Seq,
			Attrs:     entry.Attrs,
		})
	}
	guard := &RouteGuard{}
	base, err := guard.Restore(routes)
	if err != nil {
		return nil, err
	}

	table := NewRouteTable(peerLimits, audit)
	table.mu.Lock()
	defer table.mu.Unlock()
	for key, rt := range base {
		if limit, ok := table.limits[rt.Peer]; ok && table.peerCount[rt.Peer]+1 > limit {
			table.record(rt, ReasonPrefixLimitExceeded)
			return nil, ErrPrefixLimitExceeded
		}
		table.routes[key] = cloneRoute(rt)
		table.peerCount[rt.Peer]++
	}
	return table, nil
}

func parsePersistedPrefix(text string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return netip.Prefix{}, errors.New("invalid persisted prefix " + text + ": " + err.Error())
	}
	return prefix.Masked(), nil
}
