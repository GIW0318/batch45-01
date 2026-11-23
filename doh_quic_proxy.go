package task

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	rcodeSuccess  = 0
	rcodeServFail = 2
	rcodeNXDomain = 3
	rcodeRefused  = 5

	defaultNegativeTTL = 30 * time.Second
	cacheSnapshotV1    = 1
)

// DNS response codes used for locally generated failures.
const (
	RCodeSuccess  = rcodeSuccess
	RCodeServFail = rcodeServFail
	RCodeNXDomain = rcodeNXDomain
	RCodeRefused  = rcodeRefused
)

var (
	// ErrConnectionClosed is returned when a query is submitted on a closed
	// connection.
	ErrConnectionClosed = errors.New("connection is closed")
	// ErrEarlyDataRejected is returned when 0-RTT was not permitted by policy.
	ErrEarlyDataRejected = errors.New("early data is not permitted")
	// ErrInvalidQuery is returned for a query that cannot be routed.
	ErrInvalidQuery = errors.New("invalid query")
	// ErrUnknownHandle is returned when a handle was not issued by this proxy.
	ErrUnknownHandle = errors.New("unknown query handle")
	// ErrQueryFinished is returned when a completed query is completed again.
	ErrQueryFinished = errors.New("query is already finished")
	// ErrResponseMismatch is returned when the upstream question does not
	// match the query.
	ErrResponseMismatch = errors.New("upstream response question mismatch")
	// ErrQueueTimeout is returned when a query cannot obtain an upstream or
	// tenant slot before the queue timeout.
	ErrQueueTimeout = errors.New("upstream queue timeout")
)

// Config holds proxy limits.
type Config struct {
	MaxInflight  int
	QueryTimeout time.Duration
	NegativeTTL  time.Duration

	// MaxInflightPerUpstream caps concurrent dispatches to one upstream. When
	// zero, MaxInflight is used. A non-positive limit means unlimited.
	MaxInflightPerUpstream int
	// MaxInflightPerTenant caps concurrent upstream dispatches for one tenant.
	// A non-positive value means unlimited.
	MaxInflightPerTenant int
	// QueueTimeout bounds how long a query waits for a free slot. When zero,
	// QueryTimeout is used.
	QueueTimeout time.Duration
	// QueueWaitTimeout is a compatibility alias for QueueTimeout.
	QueueWaitTimeout time.Duration

	// MaxPerUpstream and MaxPerTenant are compatibility aliases.
	MaxPerUpstream int
	MaxPerTenant   int
}

// Query is one DNS query arriving on a DoQ stream.
type Query struct {
	// ID is the DNS transaction id inside the query.
	ID   uint16
	Name string
	Type uint16
	// Path identifies the network path the QUIC connection currently uses.
	Path string
	// Priority is the DNS priority that the DoQ stream must inherit.
	Priority uint8
	// EDNS carries the full EDNS(0) option bytes; they must not be truncated.
	EDNS []byte
	// Early marks a 0-RTT query submitted before the handshake completed.
	Early bool

	// Upstream selects the upstream resolver. Empty means the default
	// upstream.
	Upstream string
	// Tenant selects the tenant quota bucket. Empty uses the connection id.
	Tenant string
	// UpstreamID and TenantID are compatibility aliases.
	UpstreamID string
	TenantID   string
}

// Handle identifies one accepted query.
type Handle struct {
	QueryID  uint16
	StreamID uint64
}

// UpstreamResponse is what the upstream resolver returned.
type UpstreamResponse struct {
	QueryID   uint16
	Name      string
	Type      uint16
	RCode     int
	Answers   []string
	EDNS      []byte
	Truncated bool
}

// Result is the client-visible outcome of a query.
//
// OK is only true for a complete, matching, untruncated answer. Truncated marks
// responses that were cut off by the flow-control window.
type Result struct {
	QueryID   uint16
	Answers   []string
	RCode     int
	EDNS      []byte
	OK        bool
	Failed    bool
	Truncated bool
	Reason    string
}

type limits struct {
	upstream int
	tenant   int
	queue    time.Duration
	negative time.Duration
}

func normaliseLimits(cfg Config) limits {
	upstream := firstPositive(cfg.MaxInflightPerUpstream, cfg.MaxPerUpstream, cfg.MaxInflight)
	tenant := firstPositive(cfg.MaxInflightPerTenant, cfg.MaxPerTenant)
	queue := cfg.QueueTimeout
	if queue <= 0 {
		queue = cfg.QueueWaitTimeout
	}
	if queue <= 0 {
		queue = cfg.QueryTimeout
	}
	negative := cfg.NegativeTTL
	if negative <= 0 {
		negative = defaultNegativeTTL
	}
	return limits{upstream: upstream, tenant: tenant, queue: queue, negative: negative}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

type handleKey struct {
	connID   string
	streamID uint64
}

type flightKey struct {
	name     string
	qtype    uint16
	path     string
	upstream string
}

type cacheKey struct {
	name  string
	qtype uint16
	path  string
}

type queryState struct {
	handle Handle
	connID string
	query  Query

	group *flightGroup
	done  bool
	out   Result
}

type flightGroup struct {
	key      flightKey
	leader   *queryState
	members  map[*queryState]struct{}
	active   int
	hasEarly bool

	ready       chan struct{}
	readyClosed bool

	queued     bool
	queueTimer *time.Timer

	dispatched bool
	completed  bool
	cancelled  bool
	tenantHeld bool
}

type connState struct {
	pending map[*queryState]struct{}
	closed  bool
}

type cacheEntry struct {
	result   Result
	expires  time.Time
	negative bool
}

// QuicProxy multiplexes DNS queries over QUIC streams.
type QuicProxy struct {
	mu sync.Mutex

	limits limits

	policyLoaded  bool
	allowEarly    bool
	nextStreamID  uint64
	conns         map[string]*connState
	queries       map[handleKey]*queryState
	byHandle      map[Handle]*queryState
	groups        map[flightKey]*flightGroup
	queue         []*flightGroup
	cache         map[cacheKey]cacheEntry
	upstreamCalls map[string]int
	cancelled     map[string]bool
	upstreamUse   map[string]int
	tenantUse     map[string]int
}

// NewQuicProxy returns a proxy configured by cfg.
func NewQuicProxy(cfg Config) *QuicProxy {
	return &QuicProxy{
		limits:        normaliseLimits(cfg),
		conns:         make(map[string]*connState),
		queries:       make(map[handleKey]*queryState),
		byHandle:      make(map[Handle]*queryState),
		groups:        make(map[flightKey]*flightGroup),
		cache:         make(map[cacheKey]cacheEntry),
		upstreamCalls: make(map[string]int),
		cancelled:     make(map[string]bool),
		upstreamUse:   make(map[string]int),
		tenantUse:     make(map[string]int),
	}
}

// NewQuicProxyWithSharedCache returns a proxy that shares the negative cache
// exported by another cluster node.
func NewQuicProxyWithSharedCache(cfg Config, shared []byte) *QuicProxy {
	g := NewQuicProxy(cfg)
	if len(shared) != 0 {
		g.importNegativeCache(shared, time.Now())
	}
	return g
}

// LoadPolicy installs the forwarding policy. Queries arriving with Early set
// must be held until the policy is loaded.
func (g *QuicProxy) LoadPolicy(allowEarlyData bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.policyLoaded = true
	g.allowEarly = allowEarlyData

	for _, group := range g.groups {
		if !group.hasEarly || group.completed || group.cancelled || group.dispatched {
			continue
		}
		if !allowEarlyData {
			g.failGroupLocked(group, rcodeRefused, "early data is not permitted")
			continue
		}
		if !g.tryDispatchLocked(group) {
			g.enqueueGroupLocked(group)
		}
	}
	g.pumpLocked()
	return nil
}

// Submit accepts a query on connID and returns its handle. Queries with the
// same name, type and path that are already in flight must be coalesced into a
// single upstream call.
func (g *QuicProxy) Submit(connID string, q Query) (Handle, error) {
	if connID == "" || strings.TrimSpace(q.Name) == "" {
		return Handle{}, ErrInvalidQuery
	}

	q.EDNS = cloneBytes(q.EDNS)
	upstream := q.Upstream
	if upstream == "" {
		upstream = q.UpstreamID
	}
	tenant := q.Tenant
	if tenant == "" {
		tenant = q.TenantID
	}
	if tenant == "" {
		tenant = connID
	}
	name := canonicalName(q.Name)

	g.mu.Lock()
	conn := g.conns[connID]
	if conn == nil {
		conn = &connState{pending: make(map[*queryState]struct{})}
		g.conns[connID] = conn
	}
	if conn.closed {
		g.mu.Unlock()
		return Handle{}, ErrConnectionClosed
	}

	streamID := g.nextStreamID
	g.nextStreamID += 4
	h := Handle{QueryID: q.ID, StreamID: streamID}
	qs := &queryState{
		handle: h,
		connID: connID,
		query:  q,
		out: Result{
			QueryID: q.ID,
			Reason:  "pending",
		},
	}
	qs.query.Upstream = upstream
	qs.query.Tenant = tenant

	key := flightKey{name: name, qtype: q.Type, path: q.Path, upstream: upstream}
	group := g.groups[key]
	if group == nil {
		group = &flightGroup{
			key:     key,
			leader:  qs,
			members: make(map[*queryState]struct{}),
			ready:   make(chan struct{}),
		}
		g.groups[key] = group
	}

	qs.group = group
	group.members[qs] = struct{}{}
	group.active++
	if q.Early {
		group.hasEarly = true
	}

	g.queries[handleKey{connID: connID, streamID: streamID}] = qs
	g.byHandle[h] = qs
	conn.pending[qs] = struct{}{}

	if group.hasEarly && !g.policyLoaded {
		g.mu.Unlock()
		return h, nil
	}
	if group.hasEarly && !g.allowEarly {
		g.failGroupLocked(group, rcodeRefused, "early data is not permitted")
		g.mu.Unlock()
		return h, ErrEarlyDataRejected
	}
	if group.dispatched || group.completed {
		g.mu.Unlock()
		return h, nil
	}

	if g.tryDispatchLocked(group) {
		g.mu.Unlock()
		return h, nil
	}

	if g.limits.queue <= 0 {
		g.failGroupLocked(group, rcodeServFail, "upstream queue is full")
		g.mu.Unlock()
		return h, ErrQueueTimeout
	}

	g.enqueueGroupLocked(group)
	ready := group.ready
	g.mu.Unlock()

	<-ready

	g.mu.Lock()
	err := g.submitWaitErrorLocked(qs)
	g.mu.Unlock()
	return h, err
}

func (g *QuicProxy) submitWaitErrorLocked(qs *queryState) error {
	if !qs.done {
		return nil
	}
	switch qs.out.Reason {
	case "upstream queue timeout", "upstream queue is full":
		return ErrQueueTimeout
	case "early data is not permitted":
		return ErrEarlyDataRejected
	case "connection is closed":
		return ErrConnectionClosed
	default:
		return errors.New(qs.out.Reason)
	}
}

// UpstreamCalls returns how many upstream requests were dispatched for name.
func (g *QuicProxy) UpstreamCalls(name string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.upstreamCalls[canonicalName(name)], nil
}

// Pending returns the number of unfinished queries on connID.
func (g *QuicProxy) Pending(connID string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if conn := g.conns[connID]; conn != nil {
		return len(conn.pending), nil
	}
	return 0, nil
}

// Complete delivers an upstream response for h. A response whose question does
// not match the original query must not be cached.
func (g *QuicProxy) Complete(h Handle, resp UpstreamResponse) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	qs := g.byHandle[h]
	if qs == nil {
		return ErrUnknownHandle
	}
	if qs.done {
		if qs.out.OK && responseMatches(qs.query, resp) {
			return nil
		}
		return ErrQueryFinished
	}
	group := qs.group
	if group == nil || group.completed || group.cancelled {
		return ErrQueryFinished
	}
	if !group.dispatched {
		return errors.New("upstream response received before dispatch")
	}

	if !responseMatches(qs.query, resp) {
		g.failGroupLocked(group, rcodeServFail, "upstream response question mismatch")
		return ErrResponseMismatch
	}

	result := Result{
		RCode:     resp.RCode,
		Answers:   cloneStrings(resp.Answers),
		EDNS:      cloneBytes(resp.EDNS),
		OK:        !resp.Truncated,
		Truncated: resp.Truncated,
		Failed:    resp.Truncated,
	}
	if resp.Truncated {
		result.Reason = "response truncated by flow-control window"
	}

	g.completeGroupLocked(group, result)
	if !resp.Truncated && (resp.RCode == rcodeSuccess || resp.RCode == rcodeNXDomain) {
		negative := resp.RCode != rcodeSuccess || len(resp.Answers) == 0
		cached := cloneResult(result)
		cached.QueryID = 0
		cached.OK = true
		g.cache[cacheKey{
			name:  canonicalName(qs.query.Name),
			qtype: qs.query.Type,
			path:  qs.query.Path,
		}] = cacheEntry{
			result:   cached,
			expires:  time.Now().Add(g.limits.negative),
			negative: negative,
		}
	}
	g.pumpLocked()
	return nil
}

// Result returns the outcome of h.
func (g *QuicProxy) Result(h Handle) (Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	qs := g.byHandle[h]
	if qs == nil {
		return Result{}, ErrUnknownHandle
	}
	return cloneResult(qs.out), nil
}

// ResetStream drops the mapping for a reset stream so later queries cannot be
// routed into it.
func (g *QuicProxy) ResetStream(connID string, streamID uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	qs := g.queries[handleKey{connID: connID, streamID: streamID}]
	if qs == nil || qs.done {
		return nil
	}
	g.failQueryLocked(qs, rcodeServFail, "stream reset")
	g.pumpLocked()
	return nil
}

// Expire applies the query timeout to h and cancels the upstream request.
func (g *QuicProxy) Expire(h Handle) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	qs := g.byHandle[h]
	if qs == nil {
		return ErrUnknownHandle
	}
	if qs.done {
		return nil
	}
	g.failQueryLocked(qs, rcodeServFail, "query timeout")
	g.pumpLocked()
	return nil
}

// UpstreamCancelled reports whether the upstream request for name was
// cancelled.
func (g *QuicProxy) UpstreamCancelled(name string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cancelled[canonicalName(name)], nil
}

// StreamPriority returns the DoQ stream priority assigned to h.
func (g *QuicProxy) StreamPriority(h Handle) (uint8, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	qs := g.byHandle[h]
	if qs == nil {
		return 0, ErrUnknownHandle
	}
	return qs.query.Priority, nil
}

// Close closes connID and fails every query still in flight on it.
func (g *QuicProxy) Close(connID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	conn := g.conns[connID]
	if conn == nil {
		conn = &connState{pending: make(map[*queryState]struct{})}
		g.conns[connID] = conn
	}
	conn.closed = true
	for qs := range conn.pending {
		g.failQueryLocked(qs, rcodeServFail, "connection is closed")
	}
	g.pumpLocked()
	return nil
}

// CacheLookup looks a name up in the cache for the given path.
func (g *QuicProxy) CacheLookup(name string, qtype uint16, path string) (Result, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	key := cacheKey{name: canonicalName(name), qtype: qtype, path: path}
	entry, ok := g.cache[key]
	if !ok {
		return Result{}, false, nil
	}
	if !entry.expires.IsZero() && !time.Now().Before(entry.expires) {
		delete(g.cache, key)
		return Result{}, false, nil
	}
	return cloneResult(entry.result), true, nil
}

// CacheCount returns the number of cached entries.
func (g *QuicProxy) CacheCount() (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.purgeCacheLocked(time.Now())
	return len(g.cache), nil
}

type negativeCacheSnapshot struct {
	Version int                      `json:"version"`
	Entries []negativeCacheEntryJSON `json:"entries"`
}

type negativeCacheEntryJSON struct {
	Name      string   `json:"name"`
	Type      uint16   `json:"type"`
	Path      string   `json:"path"`
	RCode     int      `json:"rcode"`
	Answers   []string `json:"answers,omitempty"`
	EDNS      []byte   `json:"edns,omitempty"`
	ExpiresAt int64    `json:"expires_at"`
}

// ExportNegativeCache serialises the negative cache for cluster peers.
func (g *QuicProxy) ExportNegativeCache() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	g.purgeCacheLocked(now)

	keys := make([]cacheKey, 0)
	for key, entry := range g.cache {
		if entry.negative {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		if keys[i].qtype != keys[j].qtype {
			return keys[i].qtype < keys[j].qtype
		}
		return keys[i].path < keys[j].path
	})

	snapshot := negativeCacheSnapshot{
		Version: cacheSnapshotV1,
		Entries: make([]negativeCacheEntryJSON, 0, len(keys)),
	}
	for _, key := range keys {
		entry := g.cache[key]
		snapshot.Entries = append(snapshot.Entries, negativeCacheEntryJSON{
			Name:      key.name,
			Type:      key.qtype,
			Path:      key.path,
			RCode:     entry.result.RCode,
			Answers:   cloneStrings(entry.result.Answers),
			EDNS:      cloneBytes(entry.result.EDNS),
			ExpiresAt: entry.expires.UnixNano(),
		})
	}
	return json.Marshal(snapshot)
}

func (g *QuicProxy) importNegativeCache(data []byte, now time.Time) {
	var snapshot negativeCacheSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return
	}
	if snapshot.Version != cacheSnapshotV1 {
		return
	}
	for _, entry := range snapshot.Entries {
		expires := time.Unix(0, entry.ExpiresAt)
		if !expires.After(now) {
			continue
		}
		result := Result{
			RCode:   entry.RCode,
			Answers: cloneStrings(entry.Answers),
			EDNS:    cloneBytes(entry.EDNS),
			OK:      true,
		}
		g.cache[cacheKey{
			name:  canonicalName(entry.Name),
			qtype: entry.Type,
			path:  entry.Path,
		}] = cacheEntry{
			result:   result,
			expires:  expires,
			negative: true,
		}
	}
}

func (g *QuicProxy) tryDispatchLocked(group *flightGroup) bool {
	if group.dispatched || group.completed || group.cancelled || group.active == 0 {
		return group.dispatched
	}
	if group.hasEarly && !g.policyLoaded {
		return false
	}
	if group.hasEarly && !g.allowEarly {
		g.failGroupLocked(group, rcodeRefused, "early data is not permitted")
		return false
	}

	upstream := group.key.upstream
	if g.limits.upstream > 0 && g.upstreamUse[upstream] >= g.limits.upstream {
		return false
	}
	tenant := group.leader.query.Tenant
	if g.limits.tenant > 0 && g.tenantUse[tenant] >= g.limits.tenant {
		return false
	}

	if g.limits.upstream > 0 {
		g.upstreamUse[upstream]++
	}
	if g.limits.tenant > 0 {
		g.tenantUse[tenant]++
		group.tenantHeld = true
	}
	group.dispatched = true
	group.queued = false
	g.removeQueuedGroupLocked(group)
	if group.queueTimer != nil {
		group.queueTimer.Stop()
		group.queueTimer = nil
	}
	g.upstreamCalls[group.key.name]++
	g.closeGroupReadyLocked(group)
	return true
}

func (g *QuicProxy) enqueueGroupLocked(group *flightGroup) {
	if group.queued || group.dispatched || group.completed || group.cancelled {
		return
	}
	if g.limits.queue <= 0 {
		g.failGroupLocked(group, rcodeServFail, "upstream queue is full")
		return
	}
	group.queued = true
	g.queue = append(g.queue, group)
	group.queueTimer = time.AfterFunc(g.limits.queue, func() {
		g.onQueueTimeout(group)
	})
}

func (g *QuicProxy) onQueueTimeout(group *flightGroup) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if group.completed || group.cancelled || group.dispatched {
		return
	}
	g.failGroupLocked(group, rcodeServFail, "upstream queue timeout")
	g.pumpLocked()
}

func (g *QuicProxy) pumpLocked() {
	for {
		progress := false
		for _, group := range append([]*flightGroup(nil), g.queue...) {
			if group.completed || group.cancelled || group.dispatched {
				g.removeQueuedGroupLocked(group)
				continue
			}
			if group.hasEarly && !g.policyLoaded {
				continue
			}
			if group.hasEarly && !g.allowEarly {
				g.failGroupLocked(group, rcodeRefused, "early data is not permitted")
				progress = true
				continue
			}
			if g.tryDispatchLocked(group) {
				g.removeQueuedGroupLocked(group)
				progress = true
			}
		}
		if !progress {
			return
		}
	}
}

func (g *QuicProxy) removeQueuedGroupLocked(group *flightGroup) {
	group.queued = false
	for i, queued := range g.queue {
		if queued == group {
			g.queue = append(g.queue[:i], g.queue[i+1:]...)
			break
		}
	}
}

func (g *QuicProxy) failQueryLocked(qs *queryState, rcode int, reason string) {
	if qs == nil || qs.done {
		return
	}
	g.finishQueryLocked(qs, Result{
		RCode:  rcode,
		OK:     false,
		Failed: true,
		Reason: reason,
	})
}

func (g *QuicProxy) failGroupLocked(group *flightGroup, rcode int, reason string) {
	if group == nil || group.completed || group.cancelled {
		return
	}
	members := make([]*queryState, 0, len(group.members))
	for qs := range group.members {
		members = append(members, qs)
	}
	for _, qs := range members {
		if qs.done {
			continue
		}
		g.finishQueryLocked(qs, Result{
			RCode:  rcode,
			OK:     false,
			Failed: true,
			Reason: reason,
		})
	}
	if !group.completed {
		g.cancelGroupLocked(group, false)
	}
}

func (g *QuicProxy) completeGroupLocked(group *flightGroup, result Result) {
	if group.completed || group.cancelled {
		return
	}
	group.completed = true
	members := make([]*queryState, 0, len(group.members))
	for qs := range group.members {
		members = append(members, qs)
	}
	for _, qs := range members {
		if qs.done {
			continue
		}
		g.finishQueryLocked(qs, result)
	}
	g.releaseGroupResourcesLocked(group, false)
	if g.groups[group.key] == group {
		delete(g.groups, group.key)
	}
	g.closeGroupReadyLocked(group)
}

func (g *QuicProxy) finishQueryLocked(qs *queryState, result Result) {
	if qs.done {
		return
	}
	qs.done = true
	qs.out = cloneResult(result)
	qs.out.QueryID = qs.handle.QueryID
	delete(g.queries, handleKey{connID: qs.connID, streamID: qs.handle.StreamID})
	if conn := g.conns[qs.connID]; conn != nil {
		delete(conn.pending, qs)
	}
	if group := qs.group; group != nil {
		group.active--
		if group.active <= 0 && !group.completed {
			g.cancelGroupLocked(group, true)
		}
	}
}

func (g *QuicProxy) cancelGroupLocked(group *flightGroup, upstreamCancelled bool) {
	if group == nil || group.completed || group.cancelled {
		return
	}
	group.cancelled = true
	g.releaseGroupResourcesLocked(group, upstreamCancelled)
	if g.groups[group.key] == group {
		delete(g.groups, group.key)
	}
	g.closeGroupReadyLocked(group)
}

func (g *QuicProxy) releaseGroupResourcesLocked(group *flightGroup, upstreamCancelled bool) {
	if group.dispatched {
		upstream := group.key.upstream
		if g.limits.upstream > 0 {
			g.upstreamUse[upstream]--
			if g.upstreamUse[upstream] <= 0 {
				delete(g.upstreamUse, upstream)
			}
		}
		if group.tenantHeld {
			tenant := group.leader.query.Tenant
			g.tenantUse[tenant]--
			if g.tenantUse[tenant] <= 0 {
				delete(g.tenantUse, tenant)
			}
			group.tenantHeld = false
		}
		if upstreamCancelled {
			g.cancelled[group.key.name] = true
		}
		group.dispatched = false
	}
	if group.queued {
		g.removeQueuedGroupLocked(group)
	}
	if group.queueTimer != nil {
		group.queueTimer.Stop()
		group.queueTimer = nil
	}
}

func (g *QuicProxy) closeGroupReadyLocked(group *flightGroup) {
	if group.readyClosed {
		return
	}
	group.readyClosed = true
	close(group.ready)
}

func (g *QuicProxy) purgeCacheLocked(now time.Time) {
	for key, entry := range g.cache {
		if !entry.expires.IsZero() && !now.Before(entry.expires) {
			delete(g.cache, key)
		}
	}
}

func canonicalName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for len(name) > 1 && strings.HasSuffix(name, ".") {
		name = strings.TrimSuffix(name, ".")
	}
	return name
}

func responseMatches(query Query, response UpstreamResponse) bool {
	return response.QueryID == query.ID &&
		canonicalName(response.Name) == canonicalName(query.Name) &&
		response.Type == query.Type
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	return append([]byte(nil), in...)
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneResult(in Result) Result {
	in.Answers = cloneStrings(in.Answers)
	in.EDNS = cloneBytes(in.EDNS)
	return in
}
