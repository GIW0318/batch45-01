package task

import (
	"errors"
	"time"
)

// Config holds tunnel handshake limits.
type Config struct {
	HandshakeTimeout time.Duration
	MaxRetransmits   int
	ReplayWindow     uint64
	IdleTimeout      time.Duration
	// SharedKey is the cluster-wide handshake/cookie key material.
	SharedKey []byte
}

// Initiation describes one handshake attempt initiated by this side.
type Initiation struct {
	Peer           string
	SessionID      uint32
	EphemeralPub   string
	InitiatorIndex uint32
	Attempt        int
}

// HandshakeGuard owns handshake state, replay windows, cookies, MACs, peers
// and timers of the tunnel.
type HandshakeGuard struct {
	cfg Config
}

// NewHandshakeGuard returns a guard configured by cfg.
func NewHandshakeGuard(cfg Config) *HandshakeGuard {
	return &HandshakeGuard{cfg: cfg}
}

// NewHandshakeGuardFromSnapshot restores handshake sessions and replay windows
// from a snapshot.
func NewHandshakeGuardFromSnapshot(cfg Config, data []byte) (*HandshakeGuard, error) {
	return nil, errors.New("not implemented")
}

// Initiate starts a handshake with peer.
func (g *HandshakeGuard) Initiate(peer string, now time.Time) (Initiation, error) {
	return Initiation{}, errors.New("not implemented")
}

// Retransmit resends the pending handshake initiation. It must reuse the
// original session id and ephemeral public key so the responder does not treat
// the retransmission as a new session.
func (g *HandshakeGuard) Retransmit(peer string, now time.Time) (Initiation, error) {
	return Initiation{}, errors.New("not implemented")
}

// PendingSessions returns the number of half-open handshakes for peer that are
// still inside the handshake timeout at now.
func (g *HandshakeGuard) PendingSessions(peer string, now time.Time) (int, error) {
	return 0, errors.New("not implemented")
}

// Respond completes an incoming initiation identified by initiatorIndex and
// returns the responder session id.
func (g *HandshakeGuard) Respond(peer string, initiatorIndex uint32, ephemeralPub string, now time.Time) (uint32, error) {
	return 0, errors.New("not implemented")
}

// RespondConcurrent resolves the case where both sides initiated at the same
// time. It returns the single surviving session id; no second session may be
// created.
func (g *HandshakeGuard) RespondConcurrent(peer string, theirSessionID uint32, now time.Time) (uint32, error) {
	return 0, errors.New("not implemented")
}

// ActiveSessions returns the number of established sessions for peer.
func (g *HandshakeGuard) ActiveSessions(peer string) (int, error) {
	return 0, errors.New("not implemented")
}

// NoteInbound records an inbound packet of the given kind ("data" or
// "keepalive"). Only data may reset the idle timer.
func (g *HandshakeGuard) NoteInbound(peer, kind string, now time.Time) error {
	return errors.New("not implemented")
}

// IdleFor returns how long peer has been idle at now.
func (g *HandshakeGuard) IdleFor(peer string, now time.Time) (time.Duration, error) {
	return 0, errors.New("not implemented")
}

// CookieReply mints a cookie reply bound to sourceAddr.
func (g *HandshakeGuard) CookieReply(peer, sourceAddr string, now time.Time) ([]byte, error) {
	return nil, errors.New("not implemented")
}

// VerifyCookie checks a cookie reply against the current source address.
func (g *HandshakeGuard) VerifyCookie(peer, sourceAddr string, cookie []byte, now time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

// ComputeMAC computes the handshake MAC over the body, including both the
// sender and the receiver index.
func (g *HandshakeGuard) ComputeMAC(peer string, senderIndex, receiverIndex uint32, body []byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

// VerifyMAC checks a handshake MAC, including the receiver index.
func (g *HandshakeGuard) VerifyMAC(peer string, senderIndex, receiverIndex uint32, body, mac []byte) (bool, error) {
	return false, errors.New("not implemented")
}

// AcceptCounter reports whether counter is acceptable in the replay window of
// the given key epoch. The window is shared by all message types of that epoch.
func (g *HandshakeGuard) AcceptCounter(peer string, epoch uint32, msgType uint8, counter uint64) (bool, error) {
	return false, errors.New("not implemented")
}

// RotateKey starts a new key epoch and returns its number.
func (g *HandshakeGuard) RotateKey(peer string) (uint32, error) {
	return 0, errors.New("not implemented")
}

// DrainKey marks the previous epoch as drained.
func (g *HandshakeGuard) DrainKey(peer string) error {
	return errors.New("not implemented")
}

// RetireKey removes an epoch. Packets from a retired epoch must be rejected.
func (g *HandshakeGuard) RetireKey(peer string, epoch uint32) error {
	return errors.New("not implemented")
}

// RegisterPeer binds a peer name to a public key. A public key may belong to
// exactly one peer.
func (g *HandshakeGuard) RegisterPeer(name, publicKey string) error {
	return errors.New("not implemented")
}

// DeleteTunnel removes a tunnel and stops all of its timers.
func (g *HandshakeGuard) DeleteTunnel(name string) error {
	return errors.New("not implemented")
}

// ActiveTimers returns the number of running retransmit/expiry timers.
func (g *HandshakeGuard) ActiveTimers() (int, error) {
	return 0, errors.New("not implemented")
}

// Snapshot serialises sessions and replay windows so that a restart resumes
// without accepting previously seen counters.
func (g *HandshakeGuard) Snapshot() ([]byte, error) {
	return nil, errors.New("not implemented")
}
