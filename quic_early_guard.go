package task

import (
	"errors"
	"time"
)

// Config holds the runtime limits of the 0-RTT gateway.
//
//   - AmplificationFactor is the maximum number of bytes the server may send
//     for every byte received before the client address is validated. Zero
//     means the protocol default of 3.
//   - TicketLifetime bounds how long a resumption ticket may be presented.
//   - MaxEarlyDataBytes bounds the size of a single 0-RTT payload.
type Config struct {
	AmplificationFactor int
	TicketLifetime      time.Duration
	MaxEarlyDataBytes   int
	// RetryTokenKey is the cluster-wide HMAC key used for Retry tokens so that
	// any node can verify a token minted by another node.
	RetryTokenKey []byte
}

// Ticket is a single-use resumption ticket.
type Ticket struct {
	ID       string
	KeyID    string
	IssuedAt time.Time
}

// EarlyResult is the decision returned for one 0-RTT payload.
//
// Accepted reports whether the payload was accepted. Buffered reports that the
// payload is held until the handshake is confirmed. RetransmitAfter tells the
// client that the data must be resent in 1-RTT after the handshake.
type EarlyResult struct {
	Accepted        bool
	Buffered        bool
	Reason          string
	RetransmitAfter bool
}

// EarlyDataGuard arbitrates 0-RTT resumption, replay protection,
// anti-amplification accounting, Retry tokens, path validation after
// migration, stream numbering, congestion accounting and connection close.
type EarlyDataGuard struct {
	cfg Config
}

// NewEarlyDataGuard returns a guard configured by cfg.
func NewEarlyDataGuard(cfg Config) *EarlyDataGuard {
	return &EarlyDataGuard{cfg: cfg}
}

// NewEarlyDataGuardFromSnapshot rebuilds a guard from a snapshot previously
// produced by Snapshot, including the replay cache.
func NewEarlyDataGuardFromSnapshot(cfg Config, data []byte) (*EarlyDataGuard, error) {
	return nil, errors.New("not implemented")
}

// IssueTicket mints a fresh single-use ticket for connID.
func (g *EarlyDataGuard) IssueTicket(connID string, now time.Time) (Ticket, error) {
	return Ticket{}, errors.New("not implemented")
}

// OpenEarlyData accepts one 0-RTT payload presented with ticketID.
//
// The payload must be buffered until the handshake is confirmed; it must not
// become deliverable before ConfirmHandshake. A ticket may be used at most
// once, even when two connections present it concurrently, and even when the
// client changed its network path.
func (g *EarlyDataGuard) OpenEarlyData(connID, ticketID string, payload []byte, now time.Time) (EarlyResult, error) {
	return EarlyResult{}, errors.New("not implemented")
}

// ConfirmHandshake flushes the payloads buffered for connID and reports how
// many payloads became deliverable.
func (g *EarlyDataGuard) ConfirmHandshake(connID string) (int, error) {
	return 0, errors.New("not implemented")
}

// Deliverable returns the payloads that may be handed to the application.
func (g *EarlyDataGuard) Deliverable(connID string) ([][]byte, error) {
	return nil, errors.New("not implemented")
}

// FallbackKey returns the per-ticket fallback (0-RTT) key. Keys must never be
// shared between two different tickets.
func (g *EarlyDataGuard) FallbackKey(ticketID string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

// RecordReceived accounts n bytes received from the client.
func (g *EarlyDataGuard) RecordReceived(connID string, n int64) error {
	return errors.New("not implemented")
}

// AllowSend reports whether n more bytes may be sent on connID under the
// byte-based anti-amplification limit.
func (g *EarlyDataGuard) AllowSend(connID string, n int64) (bool, error) {
	return false, errors.New("not implemented")
}

// IssueRetryToken mints a Retry token bound to clientAddr. Any cluster node
// configured with the same RetryTokenKey must be able to verify it.
func (g *EarlyDataGuard) IssueRetryToken(clientAddr string, now time.Time) (string, error) {
	return "", errors.New("not implemented")
}

// VerifyRetryToken reports whether token is valid for clientAddr.
func (g *EarlyDataGuard) VerifyRetryToken(token, clientAddr string, now time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

// MigrateConnection moves connID to newPath. The new path must be validated
// before packets are accepted on it.
func (g *EarlyDataGuard) MigrateConnection(connID, newPath string) error {
	return errors.New("not implemented")
}

// ValidatePath performs path validation for connID on path.
func (g *EarlyDataGuard) ValidatePath(connID, path string) error {
	return errors.New("not implemented")
}

// AcceptPathPacket reports whether a packet that arrived on path for connID may
// be processed.
func (g *EarlyDataGuard) AcceptPathPacket(connID, path string) (bool, error) {
	return false, errors.New("not implemented")
}

// RotateTicketKey installs a new ticket encryption key and returns its id.
func (g *EarlyDataGuard) RotateTicketKey() (string, error) {
	return "", errors.New("not implemented")
}

// RetireTicketKey removes keyID from the key ring. Tickets sealed with it must
// no longer open.
func (g *EarlyDataGuard) RetireTicketKey(keyID string) error {
	return errors.New("not implemented")
}

// AllocateStream returns the next stream ID for connID. 0-RTT and 1-RTT
// allocations on the same connection must never return the same ID.
func (g *EarlyDataGuard) AllocateStream(connID string, early bool) (uint64, error) {
	return 0, errors.New("not implemented")
}

// AccountPacket records bytes sent in a packet. 0-RTT packets must not consume
// congestion window.
func (g *EarlyDataGuard) AccountPacket(connID string, n int64, early bool) error {
	return errors.New("not implemented")
}

// CongestionBytes returns the bytes currently counted against the congestion
// window for connID.
func (g *EarlyDataGuard) CongestionBytes(connID string) (int64, error) {
	return 0, errors.New("not implemented")
}

// CloseConnection tears connID down and invalidates its unused tickets.
func (g *EarlyDataGuard) CloseConnection(connID string) error {
	return errors.New("not implemented")
}

// Snapshot serialises guard state, including the replay cache, so that a
// restarted cluster node keeps rejecting replayed 0-RTT data.
func (g *EarlyDataGuard) Snapshot() ([]byte, error) {
	return nil, errors.New("not implemented")
}
