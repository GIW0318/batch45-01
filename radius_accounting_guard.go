package task

import (
	"errors"
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
type RadiusAccountingGuard struct{}

// NewRadiusAccountingGuard validates the configuration and returns the guard.
func NewRadiusAccountingGuard(cfg Config) (*RadiusAccountingGuard, error) {
	return nil, errors.New("not implemented")
}

// Handle validates and applies one accounting request. The authenticator is
// the first 16 bytes of SHA-256(secret|nas|sessionID|user|status). A
// retransmission is answered with ReplyDuplicate and is not forwarded again;
// duplicates are not errors.
func (g *RadiusAccountingGuard) Handle(req AcctRequest) (Reply, error) {
	return Reply{}, errors.New("not implemented")
}

// HandleLossy behaves like Handle but simulates a lost accounting response:
// the request is forwarded and then queued for retry.
func (g *RadiusAccountingGuard) HandleLossy(req AcctRequest) (Reply, error) {
	return Reply{}, errors.New("not implemented")
}

// Session returns one session by NAS address and session id.
func (g *RadiusAccountingGuard) Session(nas, sessionID string) (Session, bool) {
	return Session{}, false
}

// UserSessions returns all sessions of one user.
func (g *RadiusAccountingGuard) UserSessions(user string) []Session {
	return nil
}

// Pending returns the retry queue.
func (g *RadiusAccountingGuard) Pending() []AcctRequest {
	return nil
}

// Retry flushes the retry queue and returns the number of retried requests.
func (g *RadiusAccountingGuard) Retry() int {
	return 0
}

// Restore loads the session table and retry queue after a restart.
func (g *RadiusAccountingGuard) Restore(sessions []Session, pending []AcctRequest) error {
	return errors.New("not implemented")
}

// ExpireIdle stops the sessions that have been idle longer than the configured
// timeout and returns them.
func (g *RadiusAccountingGuard) ExpireIdle(now time.Time) []Session {
	return nil
}

// Snapshot returns a copy of the session table.
func (g *RadiusAccountingGuard) Snapshot() []Session {
	return nil
}

// ForwardedCount reports how many requests were actually forwarded upstream.
func (g *RadiusAccountingGuard) ForwardedCount() int {
	return 0
}

// LastForwarded returns the last request sent upstream.
func (g *RadiusAccountingGuard) LastForwarded() (AcctRequest, bool) {
	return AcctRequest{}, false
}
