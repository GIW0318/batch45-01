package task

import (
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-04"
)

func newProxy() *taskpkg.QuicProxy {
	return taskpkg.NewQuicProxy(taskpkg.Config{
		MaxInflight:  16,
		QueryTimeout: 2 * time.Second,
		NegativeTTL:  30 * time.Second,
	})
}

func mustLoadPolicy(t *testing.T, g *taskpkg.QuicProxy) {
	t.Helper()
	if err := g.LoadPolicy(true); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
}

// Two queries in flight on one connection must keep separate state; completing
// them out of order must not cross the answers.
func TestConcurrentQueriesDoNotOverwrite(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h1, err := g.Submit("conn-a", taskpkg.Query{ID: 1, Name: "a.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit a: %v", err)
	}
	h2, err := g.Submit("conn-a", taskpkg.Query{ID: 2, Name: "b.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit b: %v", err)
	}
	if err := g.Complete(h2, taskpkg.UpstreamResponse{QueryID: 2, Name: "b.example.", Type: 1, RCode: 0, Answers: []string{"10.0.0.2"}}); err != nil {
		t.Fatalf("Complete b: %v", err)
	}
	if err := g.Complete(h1, taskpkg.UpstreamResponse{QueryID: 1, Name: "a.example.", Type: 1, RCode: 0, Answers: []string{"10.0.0.1"}}); err != nil {
		t.Fatalf("Complete a: %v", err)
	}
	r1, err := g.Result(h1)
	if err != nil {
		t.Fatalf("Result a: %v", err)
	}
	r2, err := g.Result(h2)
	if err != nil {
		t.Fatalf("Result b: %v", err)
	}
	if len(r1.Answers) != 1 || r1.Answers[0] != "10.0.0.1" {
		t.Fatalf("answer for a.example. was overwritten: %v", r1.Answers)
	}
	if len(r2.Answers) != 1 || r2.Answers[0] != "10.0.0.2" {
		t.Fatalf("answer for b.example. was overwritten: %v", r2.Answers)
	}
}

// The same name with different record types must keep separate results.
func TestSameNameDifferentTypesDistinct(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	hA, err := g.Submit("conn-a", taskpkg.Query{ID: 3, Name: "www.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit A: %v", err)
	}
	hAAAA, err := g.Submit("conn-a", taskpkg.Query{ID: 4, Name: "www.example.", Type: 28, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit AAAA: %v", err)
	}
	if err := g.Complete(hA, taskpkg.UpstreamResponse{QueryID: 3, Name: "www.example.", Type: 1, Answers: []string{"192.0.2.1"}}); err != nil {
		t.Fatalf("Complete A: %v", err)
	}
	if err := g.Complete(hAAAA, taskpkg.UpstreamResponse{QueryID: 4, Name: "www.example.", Type: 28, Answers: []string{"2001:db8::1"}}); err != nil {
		t.Fatalf("Complete AAAA: %v", err)
	}
	rA, err := g.Result(hA)
	if err != nil {
		t.Fatalf("Result A: %v", err)
	}
	rAAAA, err := g.Result(hAAAA)
	if err != nil {
		t.Fatalf("Result AAAA: %v", err)
	}
	if len(rA.Answers) != 1 || rA.Answers[0] != "192.0.2.1" {
		t.Fatalf("A answer mismatch: %v", rA.Answers)
	}
	if len(rAAAA.Answers) != 1 || rAAAA.Answers[0] != "2001:db8::1" {
		t.Fatalf("AAAA answer mismatch: %v", rAAAA.Answers)
	}
}

// 0-RTT queries must wait until the forwarding policy has loaded instead of
// being sent upstream immediately.
func TestZeroRTTDeferredUntilPolicy(t *testing.T) {
	g := newProxy()
	if _, err := g.Submit("conn-a", taskpkg.Query{ID: 5, Name: "early.example.", Type: 1, Path: "path-1", Early: true}); err != nil {
		t.Fatalf("Submit early: %v", err)
	}
	calls, err := g.UpstreamCalls("early.example.")
	if err != nil {
		t.Fatalf("UpstreamCalls: %v", err)
	}
	if calls != 0 {
		t.Fatalf("0-RTT query was forwarded before the policy loaded: %d calls", calls)
	}
	pending, err := g.Pending("conn-a")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending != 1 {
		t.Fatalf("deferred early query is not queued: pending=%d", pending)
	}
}

// Once the policy is loaded the deferred 0-RTT query must be dispatched exactly
// once.
func TestZeroRTTDispatchedOncePolicyReady(t *testing.T) {
	g := newProxy()
	if _, err := g.Submit("conn-a", taskpkg.Query{ID: 6, Name: "early.example.", Type: 1, Path: "path-1", Early: true}); err != nil {
		t.Fatalf("Submit early: %v", err)
	}
	mustLoadPolicy(t, g)
	calls, err := g.UpstreamCalls("early.example.")
	if err != nil {
		t.Fatalf("UpstreamCalls: %v", err)
	}
	if calls != 1 {
		t.Fatalf("deferred early query must be forwarded once after policy load, got %d", calls)
	}
}

// A reset stream must drop its query mapping.
func TestStreamResetRemovesMapping(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 7, Name: "reset.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := g.ResetStream("conn-a", h.StreamID); err != nil {
		t.Fatalf("ResetStream: %v", err)
	}
	pending, err := g.Pending("conn-a")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending != 0 {
		t.Fatalf("reset stream still has a pending mapping: %d", pending)
	}
	if err := g.Complete(h, taskpkg.UpstreamResponse{QueryID: 7, Name: "reset.example.", Type: 1}); err == nil {
		t.Fatalf("response was routed into a reset stream")
	}
}

// A response whose question does not match the request must not be cached.
func TestResponseQuestionMismatchNotCached(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 8, Name: "asked.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := g.Complete(h, taskpkg.UpstreamResponse{QueryID: 8, Name: "other.example.", Type: 1, Answers: []string{"203.0.113.9"}}); err != nil {
		// A rejected mismatching response is acceptable; it must not be cached.
		_ = err
	}
	res, err := g.Result(h)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if res.OK {
		t.Fatalf("mismatching upstream response was reported as success")
	}
	if _, hit, err := g.CacheLookup("asked.example.", 1, "path-1"); err != nil {
		t.Fatalf("CacheLookup: %v", err)
	} else if hit {
		t.Fatalf("mismatching upstream response was written to the cache")
	}
}

// The cache key must include the network path, because a migrated connection
// may be served by a different path.
func TestCacheKeyIncludesPath(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 9, Name: "path.example.", Type: 1, Path: "path-new"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := g.Complete(h, taskpkg.UpstreamResponse{QueryID: 9, Name: "path.example.", Type: 1, Answers: []string{"198.51.100.7"}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, hit, err := g.CacheLookup("path.example.", 1, "path-old"); err != nil {
		t.Fatalf("CacheLookup old path: %v", err)
	} else if hit {
		t.Fatalf("cache entry was shared across two different paths")
	}
	if _, hit, err := g.CacheLookup("path.example.", 1, "path-new"); err != nil {
		t.Fatalf("CacheLookup new path: %v", err)
	} else if !hit {
		t.Fatalf("cache entry for the current path is missing")
	}
}

// EDNS options must be returned to the client unmodified and untruncated.
func TestEDNSOptionsPreserved(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	edns := []byte{0x00, 0x0a, 0x00, 0x08, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 10, Name: "edns.example.", Type: 1, Path: "path-1", EDNS: edns})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := g.Complete(h, taskpkg.UpstreamResponse{QueryID: 10, Name: "edns.example.", Type: 1, Answers: []string{"192.0.2.44"}, EDNS: edns}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	res, err := g.Result(h)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if len(res.EDNS) != len(edns) {
		t.Fatalf("EDNS options truncated: got %d bytes, want %d", len(res.EDNS), len(edns))
	}
	for i := range edns {
		if res.EDNS[i] != edns[i] {
			t.Fatalf("EDNS option byte %d changed", i)
		}
	}
}

// Identical in-flight queries must be coalesced into one upstream call.
func TestSameNameQueriesCoalescedUpstream(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	if _, err := g.Submit("conn-a", taskpkg.Query{ID: 11, Name: "dup.example.", Type: 1, Path: "path-1"}); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	if _, err := g.Submit("conn-b", taskpkg.Query{ID: 12, Name: "dup.example.", Type: 1, Path: "path-1"}); err != nil {
		t.Fatalf("Submit second: %v", err)
	}
	calls, err := g.UpstreamCalls("dup.example.")
	if err != nil {
		t.Fatalf("UpstreamCalls: %v", err)
	}
	if calls != 1 {
		t.Fatalf("identical concurrent queries produced %d upstream calls, want 1", calls)
	}
}

// A late duplicate completion must not replace the answer that completed first.
func TestLateDuplicateDoesNotOverwriteFirstAnswer(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h1, err := g.Submit("conn-a", taskpkg.Query{ID: 13, Name: "once.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	h2, err := g.Submit("conn-b", taskpkg.Query{ID: 14, Name: "once.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit second: %v", err)
	}
	if err := g.Complete(h1, taskpkg.UpstreamResponse{QueryID: 13, Name: "once.example.", Type: 1, Answers: []string{"203.0.113.1"}}); err != nil {
		t.Fatalf("Complete first: %v", err)
	}
	// The coalesced duplicate completes later with a different answer.
	if err := g.Complete(h2, taskpkg.UpstreamResponse{QueryID: 14, Name: "once.example.", Type: 1, Answers: []string{"203.0.113.99"}}); err != nil {
		_ = err
	}
	cached, hit, err := g.CacheLookup("once.example.", 1, "path-1")
	if err != nil {
		t.Fatalf("CacheLookup: %v", err)
	}
	if !hit {
		t.Fatalf("first answer was not cached")
	}
	if len(cached.Answers) != 1 || cached.Answers[0] != "203.0.113.1" {
		t.Fatalf("late duplicate overwrote the first answer: %v", cached.Answers)
	}
}

// A timed-out query must cancel its upstream request instead of leaving it
// running.
func TestTimeoutCancelsUpstream(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 15, Name: "slow.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := g.Expire(h); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	cancelled, err := g.UpstreamCancelled("slow.example.")
	if err != nil {
		t.Fatalf("UpstreamCancelled: %v", err)
	}
	if !cancelled {
		t.Fatalf("upstream request kept running after the query timed out")
	}
	pending, err := g.Pending("conn-a")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending != 0 {
		t.Fatalf("timed-out query still pending: %d", pending)
	}
}

// The DoQ stream must inherit the DNS priority of the query.
func TestStreamPriorityInherited(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 16, Name: "prio.example.", Type: 1, Path: "path-1", Priority: 7})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, err := g.StreamPriority(h)
	if err != nil {
		t.Fatalf("StreamPriority: %v", err)
	}
	if got != 7 {
		t.Fatalf("stream priority %d did not inherit DNS priority 7", got)
	}
}

// Closing a connection must fail the queries still in flight on it.
func TestCloseReturnsErrorsToPending(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h1, err := g.Submit("conn-a", taskpkg.Query{ID: 17, Name: "one.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit one: %v", err)
	}
	h2, err := g.Submit("conn-a", taskpkg.Query{ID: 18, Name: "two.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit two: %v", err)
	}
	if err := g.Close("conn-a"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, h := range []taskpkg.Handle{h1, h2} {
		res, err := g.Result(h)
		if err != nil {
			t.Fatalf("Result after close: %v", err)
		}
		if !res.Failed {
			t.Fatalf("in-flight query was not failed when the connection closed: %+v", res)
		}
	}
	pending, err := g.Pending("conn-a")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending != 0 {
		t.Fatalf("queries still pending after close: %d", pending)
	}
}

// A response cut off by the flow-control window must not be reported as a
// success.
func TestTruncationNotMarkedSuccess(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 19, Name: "big.example.", Type: 16, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := g.Complete(h, taskpkg.UpstreamResponse{QueryID: 19, Name: "big.example.", Type: 16, Answers: []string{"partial-record"}, Truncated: true}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	res, err := g.Result(h)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("truncation was not reported to the client")
	}
	if res.OK {
		t.Fatalf("truncated response was marked as success")
	}
}

// The negative cache must be shared between cluster nodes.
func TestNegativeCacheSharedAcrossNodes(t *testing.T) {
	g := newProxy()
	mustLoadPolicy(t, g)
	h, err := g.Submit("conn-a", taskpkg.Query{ID: 20, Name: "missing.example.", Type: 1, Path: "path-1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := g.Complete(h, taskpkg.UpstreamResponse{QueryID: 20, Name: "missing.example.", Type: 1, RCode: 3}); err != nil {
		t.Fatalf("Complete NXDOMAIN: %v", err)
	}
	shared, err := g.ExportNegativeCache()
	if err != nil {
		t.Fatalf("ExportNegativeCache: %v", err)
	}
	other := taskpkg.NewQuicProxyWithSharedCache(taskpkg.Config{NegativeTTL: 30 * time.Second}, shared)
	res, hit, err := other.CacheLookup("missing.example.", 1, "path-1")
	if err != nil {
		t.Fatalf("CacheLookup on peer node: %v", err)
	}
	if !hit {
		t.Fatalf("negative cache entry was not shared with the peer node")
	}
	if res.RCode != 3 {
		t.Fatalf("shared negative entry lost its RCODE: %d", res.RCode)
	}
}
