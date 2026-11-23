package task

import (
	"errors"
	"time"
)

// Config holds proxy limits.
type Config struct {
	MaxInflight  int
	QueryTimeout time.Duration
	NegativeTTL  time.Duration
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

// QuicProxy multiplexes DNS queries over QUIC streams.
type QuicProxy struct {
	cfg Config
}

// NewQuicProxy returns a proxy configured by cfg.
func NewQuicProxy(cfg Config) *QuicProxy {
	return &QuicProxy{cfg: cfg}
}

// NewQuicProxyWithSharedCache returns a proxy that shares the negative cache
// exported by another cluster node.
func NewQuicProxyWithSharedCache(cfg Config, shared []byte) *QuicProxy {
	return &QuicProxy{cfg: cfg}
}

// LoadPolicy installs the forwarding policy. Queries arriving with Early set
// must be held until the policy is loaded.
func (g *QuicProxy) LoadPolicy(allowEarlyData bool) error {
	return errors.New("not implemented")
}

// Submit accepts a query on connID and returns its handle. Queries with the
// same name, type and path that are already in flight must be coalesced into a
// single upstream call.
func (g *QuicProxy) Submit(connID string, q Query) (Handle, error) {
	return Handle{}, errors.New("not implemented")
}

// UpstreamCalls returns how many upstream requests were dispatched for name.
func (g *QuicProxy) UpstreamCalls(name string) (int, error) {
	return 0, errors.New("not implemented")
}

// Pending returns the number of unfinished queries on connID.
func (g *QuicProxy) Pending(connID string) (int, error) {
	return 0, errors.New("not implemented")
}

// Complete delivers an upstream response for h. A response whose question does
// not match the original query must not be cached.
func (g *QuicProxy) Complete(h Handle, resp UpstreamResponse) error {
	return errors.New("not implemented")
}

// Result returns the outcome of h.
func (g *QuicProxy) Result(h Handle) (Result, error) {
	return Result{}, errors.New("not implemented")
}

// ResetStream drops the mapping for a reset stream so later queries cannot be
// routed into it.
func (g *QuicProxy) ResetStream(connID string, streamID uint64) error {
	return errors.New("not implemented")
}

// Expire applies the query timeout to h and cancels the upstream request.
func (g *QuicProxy) Expire(h Handle) error {
	return errors.New("not implemented")
}

// UpstreamCancelled reports whether the upstream request for name was
// cancelled.
func (g *QuicProxy) UpstreamCancelled(name string) (bool, error) {
	return false, errors.New("not implemented")
}

// StreamPriority returns the DoQ stream priority assigned to h.
func (g *QuicProxy) StreamPriority(h Handle) (uint8, error) {
	return 0, errors.New("not implemented")
}

// Close closes connID and fails every query still in flight on it.
func (g *QuicProxy) Close(connID string) error {
	return errors.New("not implemented")
}

// CacheLookup looks a name up in the cache for the given path.
func (g *QuicProxy) CacheLookup(name string, qtype uint16, path string) (Result, bool, error) {
	return Result{}, false, errors.New("not implemented")
}

// CacheCount returns the number of cached entries.
func (g *QuicProxy) CacheCount() (int, error) {
	return 0, errors.New("not implemented")
}

// ExportNegativeCache serialises the negative cache for cluster peers.
func (g *QuicProxy) ExportNegativeCache() ([]byte, error) {
	return nil, errors.New("not implemented")
}
