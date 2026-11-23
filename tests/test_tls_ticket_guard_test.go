package task

import (
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-03"
)

func testConfig() taskpkg.Config {
	return taskpkg.Config{
		TicketLifetime: 10 * time.Second,
		RotationGrace:  30 * time.Second,
		MaxSessions:    8,
	}
}

func issueTicket(t *testing.T, g *taskpkg.TicketGuard, clientID string, now time.Time) taskpkg.Ticket {
	t.Helper()
	ticket, err := g.IssueTicket(taskpkg.IssueRequest{
		ClientID:        clientID,
		SNI:             "api.example.com",
		ALPN:            "h2",
		CertFingerprint: "fp-a",
		Now:             now,
	})
	if err != nil {
		t.Fatalf("IssueTicket(%s): %v", clientID, err)
	}
	return ticket
}

func resumeAt(g *taskpkg.TicketGuard, ticket taskpkg.Ticket, at time.Time) taskpkg.ResumeRequest {
	return taskpkg.ResumeRequest{
		Ticket:          ticket,
		SNI:             "api.example.com",
		ALPN:            "h2",
		CertFingerprint: "fp-a",
		Now:             at,
	}
}

// A ticket sealed under one key version must not open with another version's
// key.
func TestTicketKeyIsolatedByVersion(t *testing.T) {
	now := time.Unix(1700000000, 0)
	a := taskpkg.NewTicketGuard(testConfig())
	if err := a.AddTicketKey(1, []byte("v1-key-material")); err != nil {
		t.Fatalf("AddTicketKey v1: %v", err)
	}
	ticket := issueTicket(t, a, "client-1", now)

	b := taskpkg.NewTicketGuard(testConfig())
	if err := b.AddTicketKey(2, []byte("v2-completely-different")); err != nil {
		t.Fatalf("AddTicketKey v2: %v", err)
	}
	res, err := b.Resume(resumeAt(b, ticket, now))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.OK {
		t.Fatalf("v1 ticket was opened by a v2-only key ring")
	}
}

// The isolation is bidirectional: an old version must not open a new ticket.
func TestOldVersionCannotDecryptNewTicket(t *testing.T) {
	now := time.Unix(1700000000, 0)
	a := taskpkg.NewTicketGuard(testConfig())
	if err := a.AddTicketKey(2, []byte("v2-key-material")); err != nil {
		t.Fatalf("AddTicketKey v2: %v", err)
	}
	ticket := issueTicket(t, a, "client-1", now)

	b := taskpkg.NewTicketGuard(testConfig())
	if err := b.AddTicketKey(1, []byte("v1-completely-different")); err != nil {
		t.Fatalf("AddTicketKey v1: %v", err)
	}
	res, err := b.Resume(resumeAt(b, ticket, now))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.OK {
		t.Fatalf("v2 ticket was opened by a v1-only key ring")
	}
}

// Resumption must compare the ticket ALPN with the ALPN the client offers now.
func TestALPNMismatchRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	req := resumeAt(g, ticket, now)
	req.ALPN = "http/1.1"
	res, err := g.Resume(req)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.OK {
		t.Fatalf("ticket issued for h2 resumed with http/1.1")
	}
}

// When the early-data suite policy is updated, suites that were dropped must no
// longer be accepted for 0-RTT.
func TestEarlyDataSuitesFollowPolicy(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	if err := g.SetEarlyDataSuites([]uint16{0x1301}); err != nil {
		t.Fatalf("SetEarlyDataSuites initial: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	if err := g.SetEarlyDataSuites([]uint16{0x1302}); err != nil {
		t.Fatalf("SetEarlyDataSuites update: %v", err)
	}
	req := resumeAt(g, ticket, now)
	req.OfferedSuites = []uint16{0x1301}
	req.WantEarlyData = true
	res, err := g.Resume(req)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.EarlyDataAllowed {
		t.Fatalf("suite 0x1301 was accepted for early data after policy removed it")
	}
}

// The ticket lifetime starts at first use, not at issuance.
func TestTicketLifetimeFromFirstUse(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	first, err := g.Resume(resumeAt(g, ticket, now.Add(8*time.Second)))
	if err != nil {
		t.Fatalf("Resume first use: %v", err)
	}
	if !first.OK {
		t.Fatalf("first use was rejected, reason=%q", first.Reason)
	}
	second, err := g.Resume(resumeAt(g, ticket, now.Add(15*time.Second)))
	if err != nil {
		t.Fatalf("Resume second use: %v", err)
	}
	if !second.OK {
		t.Fatalf("ticket expired against its issue time instead of its first-use time")
	}
}

// After the first-use deadline the ticket must be rejected.
func TestTicketLifetimeExpiresAfterFirstUse(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	if _, err := g.Resume(resumeAt(g, ticket, now.Add(8*time.Second))); err != nil {
		t.Fatalf("Resume first use: %v", err)
	}
	late, err := g.Resume(resumeAt(g, ticket, now.Add(19*time.Second)))
	if err != nil {
		t.Fatalf("Resume late: %v", err)
	}
	if late.OK {
		t.Fatalf("ticket accepted after firstUse+lifetime")
	}
}

// Concurrent resumption with several tickets of one client must not let the
// later completion replace the session established first.
func TestConcurrentResumeKeepsFirstSession(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	t1 := issueTicket(t, g, "client-1", now)
	t2 := issueTicket(t, g, "client-1", now)
	first, err := g.Resume(resumeAt(g, t1, now))
	if err != nil {
		t.Fatalf("Resume ticket 1: %v", err)
	}
	if !first.OK || first.SessionID == "" {
		t.Fatalf("first resume failed: %+v", first)
	}
	second, err := g.Resume(resumeAt(g, t2, now))
	if err != nil {
		t.Fatalf("Resume ticket 2: %v", err)
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("second ticket replaced the established session: %q vs %q", second.SessionID, first.SessionID)
	}
	bound, err := g.SessionFor("client-1")
	if err != nil {
		t.Fatalf("SessionFor: %v", err)
	}
	if bound != first.SessionID {
		t.Fatalf("bound session changed: %q vs %q", bound, first.SessionID)
	}
}

// Every cluster node must share ticket keys so that the load balancer can send
// a resumption to any node.
func TestClusterSharedTicketKey(t *testing.T) {
	now := time.Unix(1700000000, 0)
	nodeA := taskpkg.NewTicketGuard(testConfig())
	if err := nodeA.AddTicketKey(1, []byte("shared-key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	ticket := issueTicket(t, nodeA, "client-1", now)
	ring, err := nodeA.ExportKeyRing()
	if err != nil {
		t.Fatalf("ExportKeyRing: %v", err)
	}
	nodeB := taskpkg.NewTicketGuardWithKeyRing(testConfig(), ring)
	res, err := nodeB.Resume(resumeAt(nodeB, ticket, now))
	if err != nil {
		t.Fatalf("Resume on node B: %v", err)
	}
	if !res.OK {
		t.Fatalf("node B could not resume a ticket issued by node A, reason=%q", res.Reason)
	}
}

// Replaying a ticket that was used for early data must revoke its PSK.
func TestReplayedTicketRevokesPSK(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	first := resumeAt(g, ticket, now)
	first.WantEarlyData = true
	first.OfferedSuites = []uint16{0x1301}
	res1, err := g.Resume(first)
	if err != nil {
		t.Fatalf("Resume first: %v", err)
	}
	if !res1.OK {
		t.Fatalf("first early-data resumption failed: %+v", res1)
	}
	second := resumeAt(g, ticket, now.Add(time.Second))
	second.WantEarlyData = true
	second.OfferedSuites = []uint16{0x1301}
	res2, err := g.Resume(second)
	if err != nil {
		t.Fatalf("Resume replay: %v", err)
	}
	if res2.OK {
		t.Fatalf("replayed ticket was accepted")
	}
	active, err := g.PSKActive(ticket.ID)
	if err != nil {
		t.Fatalf("PSKActive: %v", err)
	}
	if active {
		t.Fatalf("PSK of the replayed ticket was not revoked")
	}
}

// Resuming with a different SNI than the one bound to the ticket must fail.
func TestResumeSNIMismatchRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	req := resumeAt(g, ticket, now)
	req.SNI = "attacker.example.net"
	res, err := g.Resume(req)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.OK {
		t.Fatalf("ticket resumed under a different SNI")
	}
}

// The session cache key must include the client certificate fingerprint, so
// two certificates of the same client id never share a session.
func TestCacheKeyIncludesCertFingerprint(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	t1 := issueTicket(t, g, "client-1", now)
	t2, err := g.IssueTicket(taskpkg.IssueRequest{
		ClientID:        "client-1",
		SNI:             "api.example.com",
		ALPN:            "h2",
		CertFingerprint: "fp-b",
		Now:             now,
	})
	if err != nil {
		t.Fatalf("IssueTicket second cert: %v", err)
	}
	r1, err := g.Resume(resumeAt(g, t1, now))
	if err != nil {
		t.Fatalf("Resume first cert: %v", err)
	}
	req2 := resumeAt(g, t2, now)
	req2.CertFingerprint = "fp-b"
	r2, err := g.Resume(req2)
	if err != nil {
		t.Fatalf("Resume second cert: %v", err)
	}
	if r1.SessionID == r2.SessionID {
		t.Fatalf("two different client certificates shared one session cache entry")
	}
}

// During key rotation the previous key must remain usable for in-flight
// resumptions.
func TestRotationKeepsOldKeyForInflight(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("old-key-material")); err != nil {
		t.Fatalf("AddTicketKey v1: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	if err := g.RotateTicketKey(2, []byte("new-key-material")); err != nil {
		t.Fatalf("RotateTicketKey: %v", err)
	}
	res, err := g.Resume(resumeAt(g, ticket, now.Add(time.Second)))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !res.OK {
		t.Fatalf("in-flight resumption failed because the old key was deleted immediately")
	}
}

// A resumed session must trigger a key update instead of silently reusing the
// old traffic secrets.
func TestResumeRequiresKeyUpdate(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(testConfig())
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	ticket := issueTicket(t, g, "client-1", now)
	res, err := g.Resume(resumeAt(g, ticket, now))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !res.OK {
		t.Fatalf("resumption failed: %+v", res)
	}
	if !res.NeedsKeyUpdate {
		t.Fatalf("resumed session did not require a key update")
	}
}

// Cache eviction must skip pinned, active sessions.
func TestEvictionSkipsActiveSession(t *testing.T) {
	cfg := testConfig()
	cfg.MaxSessions = 1
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(cfg)
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	tA := issueTicket(t, g, "client-a", now)
	tB := issueTicket(t, g, "client-b", now)
	resA, err := g.Resume(resumeAt(g, tA, now))
	if err != nil {
		t.Fatalf("Resume A: %v", err)
	}
	if err := g.MarkSessionActive("client-a"); err != nil {
		t.Fatalf("MarkSessionActive: %v", err)
	}
	if _, err := g.Resume(resumeAt(g, tB, now)); err != nil {
		t.Fatalf("Resume B: %v", err)
	}
	stillThere, err := g.SessionFor("client-a")
	if err != nil {
		t.Fatalf("SessionFor A: %v", err)
	}
	if stillThere != resA.SessionID {
		t.Fatalf("active session was evicted to make room for a new one")
	}
}

// When the cache is full and all sessions are idle, the least recently used
// session is evicted.
func TestEvictionRemovesIdleSession(t *testing.T) {
	cfg := testConfig()
	cfg.MaxSessions = 1
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTicketGuard(cfg)
	if err := g.AddTicketKey(1, []byte("key-material")); err != nil {
		t.Fatalf("AddTicketKey: %v", err)
	}
	tA := issueTicket(t, g, "client-a", now)
	tB := issueTicket(t, g, "client-b", now)
	if _, err := g.Resume(resumeAt(g, tA, now)); err != nil {
		t.Fatalf("Resume A: %v", err)
	}
	if _, err := g.Resume(resumeAt(g, tB, now)); err != nil {
		t.Fatalf("Resume B: %v", err)
	}
	evicted, err := g.SessionFor("client-a")
	if err != nil {
		t.Fatalf("SessionFor A: %v", err)
	}
	if evicted != "" {
		t.Fatalf("idle session was not evicted from a full cache")
	}
}
