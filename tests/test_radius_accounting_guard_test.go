package task

import (
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-15"
)

const (
	nasOne    = "10.0.0.1"
	secretOne = "secret-one"
	nasTwo    = "10.0.0.2"
	secretTwo = "secret-two"
)

func radiusCfg() Config {
	return Config{
		SecretByNAS:    map[string]string{nasOne: secretOne, nasTwo: secretTwo},
		SessionTimeout: 30 * time.Minute,
	}
}

func newRadius(t *testing.T, cfg Config) *RadiusAccountingGuard {
	t.Helper()
	g, err := NewRadiusAccountingGuard(cfg)
	if err != nil {
		t.Fatalf("NewRadiusAccountingGuard: %v", err)
	}
	return g
}

func withAuth(secret string, req AcctRequest) AcctRequest {
	sum := sha256.Sum256([]byte(secret + "|" + req.NASAddr + "|" + req.SessionID + "|" + req.User + "|" + req.StatusType))
	req.Authenticator = sum[:16]
	return req
}

func acctReq(nas, requestID, sessionID, user, status string, at time.Time) AcctRequest {
	return AcctRequest{
		RequestID:   requestID,
		NASAddr:     nas,
		SessionID:   sessionID,
		User:        user,
		StatusType:  status,
		Timestamp:   at,
		SessionTime: 0,
		DelayTime:   0,
	}
}

func TestSessionKeyIncludesNAS(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	r1 := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(r1); err != nil {
		t.Fatalf("start from nas one: %v", err)
	}
	r2 := withAuth(secretTwo, acctReq(nasTwo, "rid-2", "sess-1", "bob", AcctStart, now))
	if _, err := g.Handle(r2); err != nil {
		t.Fatalf("start from nas two: %v", err)
	}
	if _, ok := g.Session(nasOne, "sess-1"); !ok {
		t.Fatalf("session from nas one missing")
	}
	if _, ok := g.Session(nasTwo, "sess-1"); !ok {
		t.Fatalf("session from nas two missing (same id collapsed across NAS)")
	}
	if got := len(g.Snapshot()); got != 2 {
		t.Fatalf("sessions = %d, want 2", got)
	}
	if got := g.ForwardedCount(); got != 2 {
		t.Fatalf("forwarded = %d, want 2", got)
	}
}

func TestPendingQueueRestoredAfterRestart(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := radiusCfg()
	g := newRadius(t, cfg)
	r := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	if _, err := g.HandleLossy(r); err != nil {
		t.Fatalf("lossy start: %v", err)
	}
	if got := len(g.Pending()); got != 1 {
		t.Fatalf("pending = %d before restart, want 1", got)
	}
	restarted := newRadius(t, cfg)
	if err := restarted.Restore(g.Snapshot(), g.Pending()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(restarted.Pending()); got != 1 {
		t.Fatalf("pending = %d after restore, want 1", got)
	}
	if _, ok := restarted.Session(nasOne, "sess-1"); !ok {
		t.Fatalf("session table lost across restart")
	}
}

func TestRetransmissionIsNotNewSession(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	r := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	first, err := g.Handle(r)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if !first.Forwarded {
		t.Fatalf("first request not forwarded")
	}
	second, err := g.Handle(r)
	if err != nil {
		t.Fatalf("retransmission: %v", err)
	}
	if second.Forwarded {
		t.Fatalf("retransmission was forwarded again")
	}
	if second.Code != ReplyDuplicate {
		t.Fatalf("retransmission code = %q, want %q", second.Code, ReplyDuplicate)
	}
	if got := len(g.Snapshot()); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}
	if got := g.ForwardedCount(); got != 1 {
		t.Fatalf("forwarded = %d, want 1", got)
	}
}

func TestStopBeforeStartRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	stop := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStop, now))
	reply, err := g.Handle(stop)
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("stop before start error = %v, want ErrOutOfOrder", err)
	}
	if !reply.Dropped || reply.Forwarded {
		t.Fatalf("stop before start reply = %+v, want dropped", reply)
	}
	if _, ok := g.Session(nasOne, "sess-1"); ok {
		t.Fatalf("session created by a Stop before Start")
	}
	start := withAuth(secretOne, acctReq(nasOne, "rid-2", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(start); err != nil {
		t.Fatalf("start after rejected stop: %v", err)
	}
	if _, err := g.Handle(stop); err != nil {
		t.Fatalf("stop after start: %v", err)
	}
}

func TestSessionLimitPerUser(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := radiusCfg()
	cfg.MaxSessionsPerUser = 1
	g := newRadius(t, cfg)
	first := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(first); err != nil {
		t.Fatalf("first login: %v", err)
	}
	second := withAuth(secretOne, acctReq(nasOne, "rid-2", "sess-2", "alice", AcctStart, now))
	if _, err := g.Handle(second); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("second login error = %v, want ErrSessionLimit", err)
	}
	stop := withAuth(secretOne, acctReq(nasOne, "rid-3", "sess-1", "alice", AcctStop, now))
	if _, err := g.Handle(stop); err != nil {
		t.Fatalf("stop first session: %v", err)
	}
	if _, err := g.Handle(second); err != nil {
		t.Fatalf("login after a slot freed: %v", err)
	}
}

func TestProxyUpdatesDelayKeepsTimestamp(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := radiusCfg()
	cfg.ForwardDelay = 3
	g := newRadius(t, cfg)
	req := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	req.DelayTime = 5
	if _, err := g.Handle(req); err != nil {
		t.Fatalf("handle: %v", err)
	}
	out, ok := g.LastForwarded()
	if !ok {
		t.Fatalf("no forwarded request recorded")
	}
	if out.DelayTime != 8 {
		t.Fatalf("forwarded delay = %d, want 8", out.DelayTime)
	}
	if !out.Timestamp.Equal(now) {
		t.Fatalf("forwarded timestamp = %v, want original %v", out.Timestamp, now)
	}
}

func TestSharedSecretPerNAS(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	wrong := withAuth(secretOne, acctReq(nasTwo, "rid-1", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(wrong); !errors.Is(err, ErrBadAuthenticator) {
		t.Fatalf("wrong secret error = %v, want ErrBadAuthenticator", err)
	}
	right := withAuth(secretTwo, acctReq(nasTwo, "rid-2", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(right); err != nil {
		t.Fatalf("correct secret: %v", err)
	}
	unknown := withAuth(secretOne, acctReq("10.9.9.9", "rid-3", "sess-2", "bob", AcctStart, now))
	if _, err := g.Handle(unknown); !errors.Is(err, ErrUnknownNAS) {
		t.Fatalf("unknown NAS error = %v, want ErrUnknownNAS", err)
	}
}

func TestBadAuthenticatorNotForwarded(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	req := acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now)
	req.Authenticator = []byte("short")
	reply, err := g.Handle(req)
	if !errors.Is(err, ErrBadAuthenticator) {
		t.Fatalf("bad authenticator error = %v, want ErrBadAuthenticator", err)
	}
	if reply.Forwarded || !reply.Dropped {
		t.Fatalf("bad authenticator reply = %+v, want dropped and not forwarded", reply)
	}
	if got := g.ForwardedCount(); got != 0 {
		t.Fatalf("forwarded = %d, want 0", got)
	}
	if got := len(g.Snapshot()); got != 0 {
		t.Fatalf("sessions = %d, want 0", got)
	}
}

func TestLostResponseQueuedForRetry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	req := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	reply, err := g.HandleLossy(req)
	if err != nil {
		t.Fatalf("lossy handle: %v", err)
	}
	if !reply.Forwarded {
		t.Fatalf("lossy request was not forwarded")
	}
	if got := len(g.Pending()); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
	if got := g.Retry(); got != 1 {
		t.Fatalf("retried = %d, want 1", got)
	}
	if got := len(g.Pending()); got != 0 {
		t.Fatalf("pending = %d after retry, want 0", got)
	}
	if got := g.ForwardedCount(); got != 1 {
		t.Fatalf("forwarded = %d after retry, want 1 (no double count)", got)
	}
}

func TestProxyChainDuplicateDeduped(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	first := withAuth(secretOne, acctReq(nasOne, "chain-rid-1", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(first); err != nil {
		t.Fatalf("first hop: %v", err)
	}
	second := withAuth(secretTwo, acctReq(nasTwo, "chain-rid-1", "sess-1", "alice", AcctStart, now))
	reply, err := g.Handle(second)
	if err != nil {
		t.Fatalf("duplicate hop: %v", err)
	}
	if reply.Forwarded {
		t.Fatalf("duplicate through the proxy chain was forwarded twice")
	}
	if reply.Code != ReplyDuplicate {
		t.Fatalf("duplicate hop code = %q, want %q", reply.Code, ReplyDuplicate)
	}
	if got := g.ForwardedCount(); got != 1 {
		t.Fatalf("forwarded = %d, want 1", got)
	}
}

func TestSessionTimeoutAutoStop(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	req := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stopped := g.ExpireIdle(now.Add(31 * time.Minute))
	if len(stopped) != 1 {
		t.Fatalf("expired sessions = %d, want 1", len(stopped))
	}
	if stopped[0].State != StateStopped {
		t.Fatalf("expired session state = %q, want stopped", stopped[0].State)
	}
	session, ok := g.Session(nasOne, "sess-1")
	if !ok || session.State != StateStopped {
		t.Fatalf("stored session = %+v, want stopped", session)
	}
}

func TestSessionTableRestoredAfterRestart(t *testing.T) {
	sessions := []Session{
		{NASAddr: nasOne, SessionID: "sess-1", User: "alice", State: StateStarted},
		{NASAddr: nasTwo, SessionID: "sess-2", User: "bob", State: StateStarted},
	}
	g := newRadius(t, radiusCfg())
	if err := g.Restore(sessions, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(g.Snapshot()); got != 2 {
		t.Fatalf("sessions = %d after restore, want 2", got)
	}
	if _, ok := g.Session(nasTwo, "sess-2"); !ok {
		t.Fatalf("restored session not found")
	}
	if got := len(g.UserSessions("alice")); got != 1 {
		t.Fatalf("alice sessions = %d, want 1", got)
	}
}

func TestInterimRequiresActiveSession(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	interim := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctInterim, now))
	if _, err := g.Handle(interim); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("interim without start error = %v, want ErrOutOfOrder", err)
	}
	start := withAuth(secretOne, acctReq(nasOne, "rid-2", "sess-1", "alice", AcctStart, now))
	if _, err := g.Handle(start); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := g.Handle(interim); err != nil {
		t.Fatalf("interim after start: %v", err)
	}
	session, ok := g.Session(nasOne, "sess-1")
	if !ok || session.State != StateInterim {
		t.Fatalf("session state = %+v, want interim", session)
	}
}

func TestStopAccumulatesSessionTime(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	if _, err := g.Handle(withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))); err != nil {
		t.Fatalf("start: %v", err)
	}
	interim := withAuth(secretOne, acctReq(nasOne, "rid-2", "sess-1", "alice", AcctInterim, now.Add(time.Minute)))
	interim.SessionTime = 30
	if _, err := g.Handle(interim); err != nil {
		t.Fatalf("interim: %v", err)
	}
	stop := withAuth(secretOne, acctReq(nasOne, "rid-3", "sess-1", "alice", AcctStop, now.Add(2*time.Minute)))
	stop.SessionTime = 60
	if _, err := g.Handle(stop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	session, ok := g.Session(nasOne, "sess-1")
	if !ok || session.State != StateStopped {
		t.Fatalf("session = %+v, want stopped", session)
	}
	if session.SessionTime != 60 {
		t.Fatalf("session time = %d, want 60", session.SessionTime)
	}
}

func TestConcurrentStartSameSessionSingle(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newRadius(t, radiusCfg())
	req := withAuth(secretOne, acctReq(nasOne, "rid-1", "sess-1", "alice", AcctStart, now))
	var wg sync.WaitGroup
	var mu sync.Mutex
	forwarded := 0
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reply, err := g.Handle(req)
			errs[i] = err
			if err == nil && reply.Forwarded {
				mu.Lock()
				forwarded++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent handle %d: %v", i, err)
		}
	}
	if forwarded != 1 {
		t.Fatalf("forwarded %d times for one session, want 1", forwarded)
	}
	if got := len(g.Snapshot()); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}
	if got := g.ForwardedCount(); got != 1 {
		t.Fatalf("forwarded count = %d, want 1", got)
	}
}
