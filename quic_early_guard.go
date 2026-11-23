package task

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	defaultAmplificationFactor = 3
	defaultTicketLifetime      = 24 * time.Hour
	defaultMaxEarlyDataBytes   = 64 * 1024
	retryTokenVersion          = byte(1)
	retryTokenNonceSize        = 16
	retryTokenMACSize          = sha256.Size
	retryTokenHeaderSize       = 1 + 8 + retryTokenNonceSize
	retryTokenClockSkew        = 5 * time.Second
	snapshotVersion            = 1
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

type ticketState struct {
	id          string
	connID      string
	keyID       string
	issuedAt    time.Time
	fallbackKey []byte
	used        bool
}

type connState struct {
	currentPath     string
	pathValidated   bool
	addressVerified bool
	receivedBytes   int64
	sentBytes       int64
	pending         [][]byte
	deliverable     [][]byte
	nextStreamID    uint64
	congestionBytes int64
}

// EarlyDataGuard arbitrates 0-RTT resumption, replay protection,
// anti-amplification accounting, Retry tokens, path validation after
// migration, stream numbering, congestion accounting and connection close.
type EarlyDataGuard struct {
	mu sync.Mutex

	cfg      Config
	retryKey []byte
	initErr  error

	ticketKeys         map[string][]byte
	currentTicketKeyID string
	tickets            map[string]*ticketState
	replays            map[string]time.Time
	conns              map[string]*connState
	closed             map[string]bool
}

// NewEarlyDataGuard returns a guard configured by cfg.
func NewEarlyDataGuard(cfg Config) *EarlyDataGuard {
	cfg = normaliseConfig(cfg)
	g := &EarlyDataGuard{
		cfg:        cfg,
		retryKey:   cloneBytes(cfg.RetryTokenKey),
		ticketKeys: make(map[string][]byte),
		tickets:    make(map[string]*ticketState),
		replays:    make(map[string]time.Time),
		conns:      make(map[string]*connState),
		closed:     make(map[string]bool),
	}
	g.currentTicketKeyID, _, g.initErr = g.newTicketKeyLocked()
	return g
}

func normaliseConfig(cfg Config) Config {
	if cfg.AmplificationFactor <= 0 {
		cfg.AmplificationFactor = defaultAmplificationFactor
	}
	if cfg.TicketLifetime <= 0 {
		cfg.TicketLifetime = defaultTicketLifetime
	}
	if cfg.MaxEarlyDataBytes <= 0 {
		cfg.MaxEarlyDataBytes = defaultMaxEarlyDataBytes
	}
	return cfg
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	return append([]byte(nil), in...)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}
	return b, nil
}

func (g *EarlyDataGuard) newTicketKeyLocked() (string, []byte, error) {
	for {
		idBytes, err := randomBytes(16)
		if err != nil {
			return "", nil, err
		}
		key, err := randomBytes(32)
		if err != nil {
			return "", nil, err
		}
		id := hex.EncodeToString(idBytes)
		if _, exists := g.ticketKeys[id]; exists {
			continue
		}
		g.ticketKeys[id] = key
		return id, key, nil
	}
}

func (g *EarlyDataGuard) newTicketIDLocked() (string, error) {
	for {
		b, err := randomBytes(16)
		if err != nil {
			return "", err
		}
		id := hex.EncodeToString(b)
		if _, exists := g.tickets[id]; !exists {
			return id, nil
		}
	}
}

func (g *EarlyDataGuard) ensureTicketKeyLocked() error {
	if _, ok := g.ticketKeys[g.currentTicketKeyID]; ok {
		return nil
	}
	id, _, err := g.newTicketKeyLocked()
	if err != nil {
		return err
	}
	g.currentTicketKeyID = id
	return nil
}

func rejected(reason string) EarlyResult {
	return EarlyResult{
		Accepted:        false,
		Buffered:        false,
		Reason:          reason,
		RetransmitAfter: true,
	}
}

// IssueTicket mints a fresh single-use ticket for connID.
func (g *EarlyDataGuard) IssueTicket(connID string, now time.Time) (Ticket, error) {
	if connID == "" {
		return Ticket{}, errors.New("connID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.initErr != nil {
		return Ticket{}, g.initErr
	}
	if g.closed[connID] {
		return Ticket{}, fmt.Errorf("connection %q is closed", connID)
	}
	if err := g.ensureTicketKeyLocked(); err != nil {
		return Ticket{}, err
	}
	id, err := g.newTicketIDLocked()
	if err != nil {
		return Ticket{}, err
	}
	fallbackKey, err := randomBytes(32)
	if err != nil {
		return Ticket{}, err
	}

	ticket := &ticketState{
		id:          id,
		connID:      connID,
		keyID:       g.currentTicketKeyID,
		issuedAt:    now,
		fallbackKey: fallbackKey,
	}
	g.tickets[id] = ticket
	return Ticket{ID: id, KeyID: ticket.keyID, IssuedAt: now}, nil
}

// OpenEarlyData accepts one 0-RTT payload presented with ticketID.
//
// The payload must be buffered until the handshake is confirmed; it must not
// become deliverable before ConfirmHandshake. A ticket may be used at most
// once, even when two connections present it concurrently, and even when the
// client changed its network path.
func (g *EarlyDataGuard) OpenEarlyData(connID, ticketID string, payload []byte, now time.Time) (EarlyResult, error) {
	if connID == "" {
		return EarlyResult{}, errors.New("connID must not be empty")
	}
	if ticketID == "" {
		return EarlyResult{}, errors.New("ticketID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.replays[ticketID]; ok {
		return rejected("ticket replay detected"), nil
	}

	ticket, ok := g.tickets[ticketID]
	if !ok {
		return rejected("unknown or invalidated ticket"), nil
	}

	g.replays[ticketID] = now
	ticket.used = true

	if g.closed[connID] || g.closed[ticket.connID] {
		return rejected("connection is closed"), nil
	}
	if _, active := g.ticketKeys[ticket.keyID]; !active {
		return rejected("ticket encryption key is retired"), nil
	}
	if now.Before(ticket.issuedAt) {
		return rejected("ticket is not valid yet"), nil
	}
	if !now.Before(ticket.issuedAt.Add(g.cfg.TicketLifetime)) {
		return rejected("ticket has expired"), nil
	}
	if len(payload) > g.cfg.MaxEarlyDataBytes {
		return rejected("early data exceeds configured limit"), nil
	}

	conn, err := g.ensureConnLocked(connID)
	if err != nil {
		return EarlyResult{}, err
	}
	conn.pending = append(conn.pending, cloneBytes(payload))

	return EarlyResult{
		Accepted:        true,
		Buffered:        true,
		Reason:          "early data buffered until handshake confirmation",
		RetransmitAfter: false,
	}, nil
}

// ConfirmHandshake flushes the payloads buffered for connID and reports how
// many payloads became deliverable.
func (g *EarlyDataGuard) ConfirmHandshake(connID string) (int, error) {
	if connID == "" {
		return 0, errors.New("connID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, ok := g.conns[connID]
	if !ok {
		return 0, nil
	}
	if g.closed[connID] {
		return 0, fmt.Errorf("connection %q is closed", connID)
	}

	n := len(conn.pending)
	conn.deliverable = append(conn.deliverable, conn.pending...)
	conn.pending = nil
	return n, nil
}

// Deliverable returns the payloads that may be handed to the application.
func (g *EarlyDataGuard) Deliverable(connID string) ([][]byte, error) {
	if connID == "" {
		return nil, errors.New("connID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, ok := g.conns[connID]
	if !ok {
		return nil, nil
	}
	out := make([][]byte, len(conn.deliverable))
	for i, payload := range conn.deliverable {
		out[i] = cloneBytes(payload)
	}
	return out, nil
}

// FallbackKey returns the per-ticket fallback (0-RTT) key. Keys must never be
// shared between two different tickets.
func (g *EarlyDataGuard) FallbackKey(ticketID string) ([]byte, error) {
	if ticketID == "" {
		return nil, errors.New("ticketID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	ticket, ok := g.tickets[ticketID]
	if !ok {
		return nil, fmt.Errorf("ticket %q not found", ticketID)
	}
	if _, active := g.ticketKeys[ticket.keyID]; !active {
		return nil, fmt.Errorf("ticket %q encryption key is retired", ticketID)
	}
	if g.closed[ticket.connID] {
		return nil, fmt.Errorf("ticket %q belongs to a closed connection", ticketID)
	}
	return cloneBytes(ticket.fallbackKey), nil
}

// RecordReceived accounts n bytes received from the client.
func (g *EarlyDataGuard) RecordReceived(connID string, n int64) error {
	if connID == "" {
		return errors.New("connID must not be empty")
	}
	if n < 0 {
		return errors.New("received byte count must not be negative")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, err := g.ensureConnLocked(connID)
	if err != nil {
		return err
	}
	if conn.receivedBytes > math.MaxInt64-n {
		return errors.New("received byte counter overflow")
	}
	conn.receivedBytes += n
	return nil
}

// AllowSend reports whether n more bytes may be sent on connID under the
// byte-based anti-amplification limit. A successful check reserves the bytes,
// so repeated checks cannot exceed the window.
func (g *EarlyDataGuard) AllowSend(connID string, n int64) (bool, error) {
	if connID == "" {
		return false, errors.New("connID must not be empty")
	}
	if n < 0 {
		return false, errors.New("send byte count must not be negative")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, err := g.ensureConnLocked(connID)
	if err != nil {
		return false, err
	}
	if conn.addressVerified {
		return true, nil
	}

	limit := saturatingMultiply(conn.receivedBytes, int64(g.cfg.AmplificationFactor))
	if conn.sentBytes > limit || n > limit-conn.sentBytes {
		return false, nil
	}
	conn.sentBytes += n
	return true, nil
}

func saturatingMultiply(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func (g *EarlyDataGuard) ensureConnLocked(connID string) (*connState, error) {
	if g.closed[connID] {
		return nil, fmt.Errorf("connection %q is closed", connID)
	}
	if conn := g.conns[connID]; conn != nil {
		return conn, nil
	}
	conn := &connState{}
	g.conns[connID] = conn
	return conn, nil
}

func retryTokenMAC(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("gsb-quic-retry-token-v1"))
	mac.Write(payload)
	return mac.Sum(nil)
}

// IssueRetryToken mints a Retry token bound to clientAddr. Any cluster node
// configured with the same RetryTokenKey must be able to verify it.
func (g *EarlyDataGuard) IssueRetryToken(clientAddr string, now time.Time) (string, error) {
	if clientAddr == "" {
		return "", errors.New("clientAddr must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.retryKey) == 0 {
		return "", errors.New("RetryTokenKey is not configured")
	}

	nonce, err := randomBytes(retryTokenNonceSize)
	if err != nil {
		return "", err
	}
	payload := make([]byte, retryTokenHeaderSize+len(clientAddr))
	payload[0] = retryTokenVersion
	binary.BigEndian.PutUint64(payload[1:9], uint64(now.UnixNano()))
	copy(payload[9:retryTokenHeaderSize], nonce)
	copy(payload[retryTokenHeaderSize:], clientAddr)

	token := make([]byte, 0, len(payload)+retryTokenMACSize)
	token = append(token, payload...)
	token = append(token, retryTokenMAC(g.retryKey, payload)...)
	return base64.RawURLEncoding.EncodeToString(token), nil
}

// VerifyRetryToken reports whether token is valid for clientAddr.
func (g *EarlyDataGuard) VerifyRetryToken(token, clientAddr string, now time.Time) (bool, error) {
	if token == "" || clientAddr == "" {
		return false, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.retryKey) == 0 {
		return false, errors.New("RetryTokenKey is not configured")
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < retryTokenHeaderSize+retryTokenMACSize {
		return false, nil
	}

	payload := raw[:len(raw)-retryTokenMACSize]
	gotMAC := raw[len(raw)-retryTokenMACSize:]
	if payload[0] != retryTokenVersion {
		return false, nil
	}
	if !hmac.Equal(gotMAC, retryTokenMAC(g.retryKey, payload)) {
		return false, nil
	}
	if string(payload[retryTokenHeaderSize:]) != clientAddr {
		return false, nil
	}

	issuedAt := time.Unix(0, int64(binary.BigEndian.Uint64(payload[1:9])))
	if now.Add(retryTokenClockSkew).Before(issuedAt) {
		return false, nil
	}
	if !now.Before(issuedAt.Add(g.cfg.TicketLifetime)) {
		return false, nil
	}
	return true, nil
}

// MigrateConnection moves connID to newPath. The new path must be validated
// before packets are accepted on it.
func (g *EarlyDataGuard) MigrateConnection(connID, newPath string) error {
	if connID == "" || newPath == "" {
		return errors.New("connID and newPath must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, err := g.ensureConnLocked(connID)
	if err != nil {
		return err
	}
	conn.currentPath = newPath
	conn.pathValidated = false
	conn.addressVerified = false
	conn.receivedBytes = 0
	conn.sentBytes = 0
	return nil
}

// ValidatePath performs path validation for connID on path.
func (g *EarlyDataGuard) ValidatePath(connID, path string) error {
	if connID == "" || path == "" {
		return errors.New("connID and path must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, ok := g.conns[connID]
	if !ok {
		var err error
		conn, err = g.ensureConnLocked(connID)
		if err != nil {
			return err
		}
	}
	if conn.currentPath == "" {
		conn.currentPath = path
	}
	if conn.currentPath != path {
		return fmt.Errorf("path %q is not the active path for connection %q", path, connID)
	}
	conn.pathValidated = true
	conn.addressVerified = true
	return nil
}

// AcceptPathPacket reports whether a packet that arrived on path for connID may
// be processed.
func (g *EarlyDataGuard) AcceptPathPacket(connID, path string) (bool, error) {
	if connID == "" || path == "" {
		return false, errors.New("connID and path must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed[connID] {
		return false, nil
	}
	conn, ok := g.conns[connID]
	if !ok {
		g.conns[connID] = &connState{currentPath: path, pathValidated: true}
		return true, nil
	}
	if conn.currentPath == "" {
		conn.currentPath = path
		conn.pathValidated = true
		return true, nil
	}
	return conn.currentPath == path && conn.pathValidated, nil
}

// RotateTicketKey installs a new ticket encryption key and returns its id.
func (g *EarlyDataGuard) RotateTicketKey() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.initErr != nil {
		return "", g.initErr
	}
	id, _, err := g.newTicketKeyLocked()
	if err != nil {
		return "", err
	}
	g.currentTicketKeyID = id
	return id, nil
}

// RetireTicketKey removes keyID from the key ring. Tickets sealed with it must
// no longer open.
func (g *EarlyDataGuard) RetireTicketKey(keyID string) error {
	if keyID == "" {
		return errors.New("keyID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.ticketKeys[keyID]; !ok {
		return fmt.Errorf("ticket key %q not found", keyID)
	}

	if keyID == g.currentTicketKeyID {
		id, _, err := g.newTicketKeyLocked()
		if err != nil {
			return err
		}
		g.currentTicketKeyID = id
	}
	delete(g.ticketKeys, keyID)
	return nil
}

// AllocateStream returns the next stream ID for connID. 0-RTT and 1-RTT
// allocations on the same connection must never return the same ID.
func (g *EarlyDataGuard) AllocateStream(connID string, early bool) (uint64, error) {
	if connID == "" {
		return 0, errors.New("connID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, err := g.ensureConnLocked(connID)
	if err != nil {
		return 0, err
	}
	if conn.nextStreamID > ^uint64(0)-4 {
		return 0, errors.New("stream ID space exhausted")
	}
	id := conn.nextStreamID
	conn.nextStreamID += 4
	return id, nil
}

// AccountPacket records bytes sent in a packet. 0-RTT packets must not consume
// congestion window.
func (g *EarlyDataGuard) AccountPacket(connID string, n int64, early bool) error {
	if connID == "" {
		return errors.New("connID must not be empty")
	}
	if n < 0 {
		return errors.New("packet byte count must not be negative")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, err := g.ensureConnLocked(connID)
	if err != nil {
		return err
	}
	if early {
		return nil
	}
	if conn.congestionBytes > math.MaxInt64-n {
		return errors.New("congestion byte counter overflow")
	}
	conn.congestionBytes += n
	return nil
}

// CongestionBytes returns the bytes currently counted against the congestion
// window for connID.
func (g *EarlyDataGuard) CongestionBytes(connID string) (int64, error) {
	if connID == "" {
		return 0, errors.New("connID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	conn, ok := g.conns[connID]
	if !ok {
		return 0, nil
	}
	return conn.congestionBytes, nil
}

// CloseConnection tears connID down and invalidates its unused tickets.
func (g *EarlyDataGuard) CloseConnection(connID string) error {
	if connID == "" {
		return errors.New("connID must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.closed[connID] = true
	delete(g.conns, connID)
	for id, ticket := range g.tickets {
		if ticket.connID == connID && !ticket.used {
			delete(g.tickets, id)
		}
	}
	return nil
}

type snapshotTicket struct {
	ID          string `json:"id"`
	ConnID      string `json:"conn_id"`
	KeyID       string `json:"key_id"`
	IssuedAt    int64  `json:"issued_at"`
	FallbackKey []byte `json:"fallback_key"`
	Used        bool   `json:"used"`
}

type snapshotConn struct {
	CurrentPath     string   `json:"current_path"`
	PathValidated   bool     `json:"path_validated"`
	AddressVerified bool     `json:"address_verified"`
	ReceivedBytes   int64    `json:"received_bytes"`
	SentBytes       int64    `json:"sent_bytes"`
	Pending         [][]byte `json:"pending"`
	Deliverable     [][]byte `json:"deliverable"`
	NextStreamID    uint64   `json:"next_stream_id"`
	CongestionBytes int64    `json:"congestion_bytes"`
}

type snapshotState struct {
	Version            int                       `json:"version"`
	TicketKeys         map[string][]byte         `json:"ticket_keys"`
	CurrentTicketKeyID string                    `json:"current_ticket_key_id"`
	Tickets            map[string]snapshotTicket `json:"tickets"`
	Replays            map[string]int64          `json:"replays"`
	Connections        map[string]snapshotConn   `json:"connections"`
	Closed             map[string]bool           `json:"closed"`
}

// Snapshot serialises guard state, including the replay cache, so that a
// restarted cluster node keeps rejecting replayed 0-RTT data.
func (g *EarlyDataGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := snapshotState{
		Version:            snapshotVersion,
		TicketKeys:         make(map[string][]byte, len(g.ticketKeys)),
		CurrentTicketKeyID: g.currentTicketKeyID,
		Tickets:            make(map[string]snapshotTicket, len(g.tickets)),
		Replays:            make(map[string]int64, len(g.replays)),
		Connections:        make(map[string]snapshotConn, len(g.conns)),
		Closed:             make(map[string]bool, len(g.closed)),
	}
	for id, key := range g.ticketKeys {
		state.TicketKeys[id] = cloneBytes(key)
	}
	for id, ticket := range g.tickets {
		state.Tickets[id] = snapshotTicket{
			ID:          ticket.id,
			ConnID:      ticket.connID,
			KeyID:       ticket.keyID,
			IssuedAt:    ticket.issuedAt.UnixNano(),
			FallbackKey: cloneBytes(ticket.fallbackKey),
			Used:        ticket.used,
		}
	}
	for id, at := range g.replays {
		state.Replays[id] = at.UnixNano()
	}
	for id, conn := range g.conns {
		state.Connections[id] = snapshotConn{
			CurrentPath:     conn.currentPath,
			PathValidated:   conn.pathValidated,
			AddressVerified: conn.addressVerified,
			ReceivedBytes:   conn.receivedBytes,
			SentBytes:       conn.sentBytes,
			Pending:         clonePayloads(conn.pending),
			Deliverable:     clonePayloads(conn.deliverable),
			NextStreamID:    conn.nextStreamID,
			CongestionBytes: conn.congestionBytes,
		}
	}
	for id, closed := range g.closed {
		state.Closed[id] = closed
	}

	return json.Marshal(state)
}

func clonePayloads(in [][]byte) [][]byte {
	if in == nil {
		return nil
	}
	out := make([][]byte, len(in))
	for i, payload := range in {
		out[i] = cloneBytes(payload)
	}
	return out
}

// NewEarlyDataGuardFromSnapshot rebuilds a guard from a snapshot previously
// produced by Snapshot, including the replay cache.
func NewEarlyDataGuardFromSnapshot(cfg Config, data []byte) (*EarlyDataGuard, error) {
	if len(data) == 0 {
		return nil, errors.New("empty snapshot")
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	if state.Version != snapshotVersion {
		return nil, fmt.Errorf("unsupported snapshot version %d", state.Version)
	}

	cfg = normaliseConfig(cfg)
	g := &EarlyDataGuard{
		cfg:        cfg,
		retryKey:   cloneBytes(cfg.RetryTokenKey),
		ticketKeys: make(map[string][]byte, len(state.TicketKeys)),
		tickets:    make(map[string]*ticketState, len(state.Tickets)),
		replays:    make(map[string]time.Time, len(state.Replays)),
		conns:      make(map[string]*connState, len(state.Connections)),
		closed:     make(map[string]bool, len(state.Closed)),
	}
	for id, key := range state.TicketKeys {
		g.ticketKeys[id] = cloneBytes(key)
	}
	g.currentTicketKeyID = state.CurrentTicketKeyID
	if err := g.ensureTicketKeyLocked(); err != nil {
		return nil, err
	}
	for id, ticket := range state.Tickets {
		g.tickets[id] = &ticketState{
			id:          ticket.ID,
			connID:      ticket.ConnID,
			keyID:       ticket.KeyID,
			issuedAt:    time.Unix(0, ticket.IssuedAt),
			fallbackKey: cloneBytes(ticket.FallbackKey),
			used:        ticket.Used,
		}
	}
	for id, at := range state.Replays {
		g.replays[id] = time.Unix(0, at)
	}
	for id, conn := range state.Connections {
		g.conns[id] = &connState{
			currentPath:     conn.CurrentPath,
			pathValidated:   conn.PathValidated,
			addressVerified: conn.AddressVerified,
			receivedBytes:   conn.ReceivedBytes,
			sentBytes:       conn.SentBytes,
			pending:         clonePayloads(conn.Pending),
			deliverable:     clonePayloads(conn.Deliverable),
			nextStreamID:    conn.NextStreamID,
			congestionBytes: conn.CongestionBytes,
		}
	}
	for id, closed := range state.Closed {
		g.closed[id] = closed
	}
	return g, nil
}
