package task

import (
	"errors"
	"time"
)

// Config holds ticket policy limits.
type Config struct {
	// TicketLifetime is measured from the first successful use of a ticket.
	TicketLifetime time.Duration
	// RotationGrace keeps a previous ticket key usable after rotation so that
	// resumptions already in flight do not fail.
	RotationGrace time.Duration
	// MaxSessions bounds the session cache. Active sessions must not be
	// evicted to make room.
	MaxSessions int
}

// IssueRequest describes a new session ticket.
type IssueRequest struct {
	ClientID        string
	SNI             string
	ALPN            string
	CertFingerprint string
	Now             time.Time
}

// Ticket is the sealed resumption ticket returned to the client.
type Ticket struct {
	ID      string
	Version uint16
	Sealed  []byte
}

// ResumeRequest is presented when a client tries to resume.
type ResumeRequest struct {
	Ticket          Ticket
	SNI             string
	ALPN            string
	CertFingerprint string
	// OfferedSuites are the cipher suites the client is willing to use for
	// early data.
	OfferedSuites []uint16
	WantEarlyData bool
	Now           time.Time
}

// ResumeResult is the outcome of a resumption attempt.
type ResumeResult struct {
	OK               bool
	Reason           string
	SessionID        string
	NeedsKeyUpdate   bool
	EarlyDataAllowed bool
}

// TicketGuard issues and validates TLS 1.3 session tickets.
type TicketGuard struct {
	cfg Config
}

// NewTicketGuard returns a guard with its own ticket key ring.
func NewTicketGuard(cfg Config) *TicketGuard {
	return &TicketGuard{cfg: cfg}
}

// NewTicketGuardWithKeyRing returns a guard sharing a key ring exported from
// another node.
func NewTicketGuardWithKeyRing(cfg Config, ring []byte) *TicketGuard {
	return &TicketGuard{cfg: cfg}
}

// AddTicketKey installs key material for a ticket version.
func (g *TicketGuard) AddTicketKey(version uint16, key []byte) error {
	return errors.New("not implemented")
}

// RotateTicketKey makes version the active issuance key and keeps the previous
// key available for the configured grace period.
func (g *TicketGuard) RotateTicketKey(version uint16, key []byte) error {
	return errors.New("not implemented")
}

// RetireTicketKey removes a version from the ring immediately.
func (g *TicketGuard) RetireTicketKey(version uint16) error {
	return errors.New("not implemented")
}

// ExportKeyRing serialises the key ring so other cluster nodes can open
// tickets issued here.
func (g *TicketGuard) ExportKeyRing() ([]byte, error) {
	return nil, errors.New("not implemented")
}

// IssueTicket seals a new ticket bound to the client, SNI, ALPN and client
// certificate fingerprint.
func (g *TicketGuard) IssueTicket(req IssueRequest) (Ticket, error) {
	return Ticket{}, errors.New("not implemented")
}

// Resume validates a ticket and returns the established session.
//
// The request must match the ALPN, SNI and certificate fingerprint recorded at
// issuance, the ticket key version must still be available, the ticket must not
// be replayed, and the returned session must request a key update.
func (g *TicketGuard) Resume(req ResumeRequest) (ResumeResult, error) {
	return ResumeResult{}, errors.New("not implemented")
}

// SetEarlyDataSuites replaces the policy of cipher suites allowed for 0-RTT.
func (g *TicketGuard) SetEarlyDataSuites(suites []uint16) error {
	return errors.New("not implemented")
}

// SessionFor returns the session id currently bound to clientID, or "".
func (g *TicketGuard) SessionFor(clientID string) (string, error) {
	return "", errors.New("not implemented")
}

// MarkSessionActive pins a session so that cache eviction skips it.
func (g *TicketGuard) MarkSessionActive(clientID string) error {
	return errors.New("not implemented")
}

// ActiveSessions returns the number of pinned sessions.
func (g *TicketGuard) ActiveSessions() (int, error) {
	return 0, errors.New("not implemented")
}

// CacheSize returns the number of cached sessions.
func (g *TicketGuard) CacheSize() (int, error) {
	return 0, errors.New("not implemented")
}

// PSKActive reports whether the PSK of a ticket may still be used.
func (g *TicketGuard) PSKActive(ticketID string) (bool, error) {
	return false, errors.New("not implemented")
}
