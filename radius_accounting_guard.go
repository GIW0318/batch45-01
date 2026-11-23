package task

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Accounting status types.
const (
	AcctStart   = "Start"
	AcctInterim = "Interim-Update"
	AcctStop    = "Stop"
)

// Session states kept by the proxy.
const (
	StateStarted = "started"
	StateInterim = "interim"
	StateStopped = "stopped"
)

// Reply codes.
const (
	ReplyResponse  = "Accounting-Response"
	ReplyDuplicate = "Accounting-Response-Duplicate"
)

var (
	ErrBadAuthenticator = errors.New("radius: request authenticator mismatch")
	ErrUnknownNAS       = errors.New("radius: unknown NAS")
	ErrOutOfOrder       = errors.New("radius: accounting status out of order")
	ErrSessionLimit     = errors.New("radius: per-user session limit reached")
	ErrUnknownSession   = errors.New("radius: unknown session")
	ErrRetryQueueFull   = errors.New("radius: retry queue full")
)

// Config configures the accounting proxy.
type Config struct {
	SecretByNAS        map[string]string
	MaxSessionsPerUser int
	SessionTimeout     time.Duration
	RetryQueueSize     int
	ForwardDelay       uint32
}

// AcctRequest is one Accounting-Request.
type AcctRequest struct {
	RequestID     string
	NASAddr       string
	SessionID     string
	User          string
	StatusType    string
	SessionTime   uint32
	DelayTime     uint32
	Timestamp     time.Time
	Authenticator []byte
	Domain        string
}

// Reply describes how one request was handled.
type Reply struct {
	Code      string
	Forwarded bool
	Dropped   bool
	Reason    string
	Out       AcctRequest
}

// Session is one accounting session keyed by NAS address plus session id.
type Session struct {
	NASAddr     string
	SessionID   string
	User        string
	State       string
	SessionTime uint32
	UpdatedAt   time.Time
}

// RadiusAccountingGuard proxies accounting requests.
type RadiusAccountingGuard struct {
	mu sync.Mutex

	secrets    map[string]string
	maxPerUser int
	timeout    time.Duration
	retryCap   int
	fwdDelay   uint32

	sessions map[sessionKey]*Session
	// dedup maps a forwarded RequestID to the session that produced it, so
	// retransmissions and proxy-chain duplicates are answered from the
	// table instead of being forwarded again. Entries are reclaimed when
	// their session is reaped, keeping residency proportional to the
	// number of live sessions.
	dedup map[string]sessionKey
	// bySession lists the RequestIDs recorded per session for reclamation.
	bySession map[sessionKey][]string

	pending []AcctRequest

	forwarded     int
	lastForwarded AcctRequest
	hasForwarded  bool
}

type sessionKey struct {
	nas       string
	sessionID string
}

// NewRadiusAccountingGuard validates the configuration and returns the guard.
func NewRadiusAccountingGuard(cfg Config) (*RadiusAccountingGuard, error) {
	if len(cfg.SecretByNAS) == 0 {
		return nil, errors.New("radius: at least one NAS secret is required")
	}
	if cfg.MaxSessionsPerUser < 0 {
		return nil, errors.New("radius: MaxSessionsPerUser must not be negative")
	}
	if cfg.RetryQueueSize < 0 {
		return nil, errors.New("radius: RetryQueueSize must not be negative")
	}
	secrets := make(map[string]string, len(cfg.SecretByNAS))
	for nas, secret := range cfg.SecretByNAS {
		secrets[nas] = secret
	}
	return &RadiusAccountingGuard{
		secrets:    secrets,
		maxPerUser: cfg.MaxSessionsPerUser,
		timeout:    cfg.SessionTimeout,
		retryCap:   cfg.RetryQueueSize,
		fwdDelay:   cfg.ForwardDelay,
		sessions:   make(map[sessionKey]*Session),
		dedup:      make(map[string]sessionKey),
		bySession:  make(map[sessionKey][]string),
	}, nil
}

// Handle validates and applies one accounting request. The authenticator is
// the first 16 bytes of SHA-256(secret|nas|sessionID|user|status). A
// retransmission is answered with ReplyDuplicate and is not forwarded again;
// duplicates are not errors.
func (g *RadiusAccountingGuard) Handle(req AcctRequest) (Reply, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.handleLocked(req)
}

// HandleLossy behaves like Handle but simulates a lost accounting response:
// the request is forwarded and then queued for retry.
func (g *RadiusAccountingGuard) HandleLossy(req AcctRequest) (Reply, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retryCap > 0 && len(g.pending) >= g.retryCap {
		return Reply{Dropped: true, Reason: ErrRetryQueueFull.Error()}, ErrRetryQueueFull
	}
	reply, err := g.handleLocked(req)
	if err != nil {
		return reply, err
	}
	if reply.Forwarded {
		g.pending = append(g.pending, reply.Out)
	}
	return reply, nil
}

func (g *RadiusAccountingGuard) handleLocked(req AcctRequest) (Reply, error) {
	secret, ok := g.secrets[req.NASAddr]
	if !ok {
		return Reply{Dropped: true, Reason: ErrUnknownNAS.Error()}, ErrUnknownNAS
	}
	if !g.validAuthenticator(secret, req) {
		return Reply{Dropped: true, Reason: ErrBadAuthenticator.Error()}, ErrBadAuthenticator
	}
	if _, seen := g.dedup[req.RequestID]; seen {
		return Reply{Code: ReplyDuplicate}, nil
	}

	key := sessionKey{nas: req.NASAddr, sessionID: req.SessionID}
	sess, exists := g.sessions[key]
	active := exists && sess.State != StateStopped

	switch req.StatusType {
	case AcctStart:
		if active {
			return Reply{Dropped: true, Reason: ErrOutOfOrder.Error()}, ErrOutOfOrder
		}
		if err := g.checkSessionLimit(req.User); err != nil {
			return Reply{Dropped: true, Reason: err.Error()}, err
		}
		sess = &Session{NASAddr: req.NASAddr, SessionID: req.SessionID, User: req.User}
		g.sessions[key] = sess
		sess.State = StateStarted
		sess.SessionTime = req.SessionTime
		sess.UpdatedAt = req.Timestamp
	case AcctInterim:
		if !active {
			return Reply{Dropped: true, Reason: ErrOutOfOrder.Error()}, ErrOutOfOrder
		}
		sess.State = StateInterim
		if req.SessionTime > sess.SessionTime {
			sess.SessionTime = req.SessionTime
		}
		sess.UpdatedAt = req.Timestamp
	case AcctStop:
		if !active {
			return Reply{Dropped: true, Reason: ErrOutOfOrder.Error()}, ErrOutOfOrder
		}
		sess.State = StateStopped
		if req.SessionTime > sess.SessionTime {
			sess.SessionTime = req.SessionTime
		}
		sess.UpdatedAt = req.Timestamp
	default:
		err := fmt.Errorf("radius: unknown status type %q", req.StatusType)
		return Reply{Dropped: true, Reason: err.Error()}, err
	}

	out := req
	out.DelayTime += g.fwdDelay
	g.forwarded++
	g.lastForwarded = out
	g.hasForwarded = true
	g.dedup[req.RequestID] = key
	g.bySession[key] = append(g.bySession[key], req.RequestID)
	return Reply{Code: ReplyResponse, Forwarded: true, Out: out}, nil
}

func (g *RadiusAccountingGuard) validAuthenticator(secret string, req AcctRequest) bool {
	sum := sha256.Sum256([]byte(secret + "|" + req.NASAddr + "|" + req.SessionID + "|" + req.User + "|" + req.StatusType))
	want := sum[:16]
	if len(req.Authenticator) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(req.Authenticator, want) == 1
}

func (g *RadiusAccountingGuard) checkSessionLimit(user string) error {
	if g.maxPerUser <= 0 {
		return nil
	}
	count := 0
	for _, sess := range g.sessions {
		if sess.User == user && sess.State != StateStopped {
			count++
		}
	}
	if count >= g.maxPerUser {
		return ErrSessionLimit
	}
	return nil
}

// reapLocked removes a stopped session and its dedup entries.
func (g *RadiusAccountingGuard) reapLocked(key sessionKey) {
	for _, rid := range g.bySession[key] {
		delete(g.dedup, rid)
	}
	delete(g.bySession, key)
	delete(g.sessions, key)
}

// Session returns one session by NAS address and session id.
func (g *RadiusAccountingGuard) Session(nas, sessionID string) (Session, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	sess, ok := g.sessions[sessionKey{nas: nas, sessionID: sessionID}]
	if !ok {
		return Session{}, false
	}
	return *sess, true
}

// UserSessions returns all sessions of one user.
func (g *RadiusAccountingGuard) UserSessions(user string) []Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Session
	for _, sess := range g.sessions {
		if sess.User == user {
			out = append(out, *sess)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NASAddr != out[j].NASAddr {
			return out[i].NASAddr < out[j].NASAddr
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// Pending returns the retry queue.
func (g *RadiusAccountingGuard) Pending() []AcctRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]AcctRequest, len(g.pending))
	copy(out, g.pending)
	return out
}

// Retry flushes the retry queue and returns the number of retried requests.
func (g *RadiusAccountingGuard) Retry() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := len(g.pending)
	if n == 0 {
		return 0
	}
	for _, req := range g.pending {
		g.lastForwarded = req
		g.hasForwarded = true
	}
	g.pending = nil
	return n
}

// Restore loads the session table and retry queue after a restart.
func (g *RadiusAccountingGuard) Restore(sessions []Session, pending []AcctRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, sess := range sessions {
		key := sessionKey{nas: sess.NASAddr, sessionID: sess.SessionID}
		copied := sess
		g.sessions[key] = &copied
	}
	if g.retryCap > 0 && len(g.pending)+len(pending) > g.retryCap {
		return ErrRetryQueueFull
	}
	g.pending = append(g.pending, pending...)
	return nil
}

// ExpireIdle stops the sessions that have been idle longer than the configured
// timeout and returns them.
func (g *RadiusAccountingGuard) ExpireIdle(now time.Time) []Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timeout <= 0 {
		return nil
	}
	var stopped []Session
	for key, sess := range g.sessions {
		idle := now.Sub(sess.UpdatedAt)
		if sess.State == StateStopped {
			// Reap sessions that have been stopped for longer than the
			// timeout together with their dedup entries, so long-running
			// proxies keep memory proportional to active sessions.
			if idle > g.timeout {
				g.reapLocked(key)
			}
			continue
		}
		if idle > g.timeout {
			sess.State = StateStopped
			sess.UpdatedAt = now
			stopped = append(stopped, *sess)
		}
	}
	sort.Slice(stopped, func(i, j int) bool {
		if stopped[i].NASAddr != stopped[j].NASAddr {
			return stopped[i].NASAddr < stopped[j].NASAddr
		}
		return stopped[i].SessionID < stopped[j].SessionID
	})
	return stopped
}

// Snapshot returns a copy of the session table.
func (g *RadiusAccountingGuard) Snapshot() []Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Session, 0, len(g.sessions))
	for _, sess := range g.sessions {
		out = append(out, *sess)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NASAddr != out[j].NASAddr {
			return out[i].NASAddr < out[j].NASAddr
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// ForwardedCount reports how many requests were actually forwarded upstream.
func (g *RadiusAccountingGuard) ForwardedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.forwarded
}

// LastForwarded returns the last request sent upstream.
func (g *RadiusAccountingGuard) LastForwarded() (AcctRequest, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastForwarded, g.hasForwarded
}
