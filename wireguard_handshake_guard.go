package task

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultHandshakeTimeout = 5 * time.Second
	defaultReplayWindow     = uint64(64)
	snapshotVersion         = 1
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

type pendingSession struct {
	sessionID      uint32
	ephemeralPub   string
	initiatorIndex uint32
	attempt        int
	createdAt      time.Time
	lastSent       time.Time
	timerID        uint64
}

type activeSession struct {
	sessionID       uint32
	remoteIndex     uint32
	remoteEphemeral string
	createdAt       time.Time
}

type replayWindow struct {
	max    uint64
	maxSet bool
	seen   map[uint64]struct{}
}

type peerState struct {
	name        string
	publicKey   string
	pending     map[uint32]*pendingSession
	active      map[uint32]*activeSession
	epochs      map[uint32]*replayWindow
	drained     map[uint32]bool
	current     uint32
	previous    uint32
	lastEpoch   uint32
	lastData    time.Time
	hasLastData bool
}

// HandshakeGuard owns handshake state, replay windows, cookies, MACs, peers
// and timers of the tunnel.
type HandshakeGuard struct {
	mu            sync.Mutex
	cfg           Config
	peers         map[string]*peerState
	publicKeys    map[string]string
	timers        map[uint64]struct{}
	nextSessionID uint32
	nextTimerID   uint64
}

// NewHandshakeGuard returns a guard configured by cfg.
func NewHandshakeGuard(cfg Config) *HandshakeGuard {
	cfg = normalizeConfig(cfg)
	return &HandshakeGuard{
		cfg:        cfg,
		peers:      make(map[string]*peerState),
		publicKeys: make(map[string]string),
		timers:     make(map[uint64]struct{}),
	}
}

// NewHandshakeGuardFromSnapshot restores handshake sessions and replay windows
// from a snapshot.
func NewHandshakeGuardFromSnapshot(cfg Config, data []byte) (*HandshakeGuard, error) {
	if len(data) == 0 {
		return nil, errors.New("empty handshake snapshot")
	}

	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode handshake snapshot: %w", err)
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("unsupported handshake snapshot version %d", snap.Version)
	}
	if cfg.ReplayWindow == 0 && snap.ReplayWindow > 0 {
		cfg.ReplayWindow = snap.ReplayWindow
	}

	g := NewHandshakeGuard(cfg)
	g.nextSessionID = snap.NextSessionID
	g.nextTimerID = snap.NextTimerID
	usedSessions := make(map[uint32]string)

	for _, peerSnap := range snap.Peers {
		if peerSnap.Name == "" {
			return nil, errors.New("snapshot contains a peer with an empty name")
		}
		if _, exists := g.peers[peerSnap.Name]; exists {
			return nil, fmt.Errorf("snapshot contains duplicate peer %q", peerSnap.Name)
		}
		publicKey := canonicalPublicKey(peerSnap.PublicKey)
		if publicKey != "" {
			if owner, exists := g.publicKeys[publicKey]; exists && owner != peerSnap.Name {
				return nil, fmt.Errorf("snapshot public key is shared by %q and %q", owner, peerSnap.Name)
			}
			g.publicKeys[publicKey] = peerSnap.Name
		}

		peer := &peerState{
			name:        peerSnap.Name,
			publicKey:   publicKey,
			pending:     make(map[uint32]*pendingSession),
			active:      make(map[uint32]*activeSession),
			epochs:      make(map[uint32]*replayWindow),
			drained:     make(map[uint32]bool),
			current:     peerSnap.CurrentEpoch,
			previous:    peerSnap.PreviousEpoch,
			lastEpoch:   peerSnap.LastEpoch,
			lastData:    peerSnap.LastData,
			hasLastData: peerSnap.HasLastData,
		}

		for _, pendingSnap := range peerSnap.Pending {
			if pendingSnap.SessionID == 0 {
				return nil, fmt.Errorf("snapshot peer %q has a zero session id", peerSnap.Name)
			}
			if _, exists := peer.pending[pendingSnap.SessionID]; exists {
				return nil, fmt.Errorf("snapshot peer %q has duplicate pending session %d", peerSnap.Name, pendingSnap.SessionID)
			}
			if owner, exists := usedSessions[pendingSnap.SessionID]; exists {
				return nil, fmt.Errorf("snapshot session id %d is shared by %q and %q", pendingSnap.SessionID, owner, peerSnap.Name)
			}
			pending := &pendingSession{
				sessionID:      pendingSnap.SessionID,
				ephemeralPub:   pendingSnap.EphemeralPub,
				initiatorIndex: pendingSnap.InitiatorIndex,
				attempt:        pendingSnap.Attempt,
				createdAt:      pendingSnap.CreatedAt,
				lastSent:       pendingSnap.LastSent,
			}
			if pendingSnap.TimerID == 0 {
				pending.timerID = g.addTimerLocked()
			} else {
				if _, exists := g.timers[pendingSnap.TimerID]; exists {
					return nil, fmt.Errorf("snapshot contains duplicate timer id %d", pendingSnap.TimerID)
				}
				pending.timerID = pendingSnap.TimerID
				g.timers[pending.timerID] = struct{}{}
				if pending.timerID > g.nextTimerID {
					g.nextTimerID = pending.timerID
				}
			}
			peer.pending[pending.sessionID] = pending
			usedSessions[pending.sessionID] = peerSnap.Name
		}

		for _, activeSnap := range peerSnap.Active {
			if activeSnap.SessionID == 0 {
				return nil, fmt.Errorf("snapshot peer %q has a zero active session id", peerSnap.Name)
			}
			if _, exists := peer.active[activeSnap.SessionID]; exists {
				return nil, fmt.Errorf("snapshot peer %q has duplicate active session %d", peerSnap.Name, activeSnap.SessionID)
			}
			if owner, exists := usedSessions[activeSnap.SessionID]; exists {
				return nil, fmt.Errorf("snapshot session id %d is shared by %q and %q", activeSnap.SessionID, owner, peerSnap.Name)
			}
			peer.active[activeSnap.SessionID] = &activeSession{
				sessionID:       activeSnap.SessionID,
				remoteIndex:     activeSnap.RemoteIndex,
				remoteEphemeral: activeSnap.RemoteEphemeral,
				createdAt:       activeSnap.CreatedAt,
			}
			usedSessions[activeSnap.SessionID] = peerSnap.Name
		}

		for _, epochSnap := range peerSnap.Epochs {
			if _, exists := peer.epochs[epochSnap.Epoch]; exists {
				return nil, fmt.Errorf("snapshot peer %q has duplicate epoch %d", peerSnap.Name, epochSnap.Epoch)
			}
			window := &replayWindow{
				max:    epochSnap.Max,
				maxSet: epochSnap.MaxSet,
				seen:   make(map[uint64]struct{}, len(epochSnap.Seen)),
			}
			for _, counter := range epochSnap.Seen {
				window.seen[counter] = struct{}{}
			}
			peer.epochs[epochSnap.Epoch] = window
			if epochSnap.Epoch > peer.lastEpoch {
				peer.lastEpoch = epochSnap.Epoch
			}
		}

		for _, epoch := range peerSnap.Drained {
			peer.drained[epoch] = true
			if epoch > peer.lastEpoch {
				peer.lastEpoch = epoch
			}
		}
		if peer.current > peer.lastEpoch {
			peer.lastEpoch = peer.current
		}
		if peer.previous > peer.lastEpoch {
			peer.lastEpoch = peer.previous
		}
		g.peers[peer.name] = peer
	}

	if maxID := maxSessionID(g); g.nextSessionID < maxID {
		g.nextSessionID = maxID
	}
	if g.nextSessionID == ^uint32(0) {
		return nil, errors.New("handshake session id space exhausted")
	}

	return g, nil
}

// Initiate starts a handshake with peer.
func (g *HandshakeGuard) Initiate(peer string, now time.Time) (Initiation, error) {
	peer = strings.TrimSpace(peer)
	if peer == "" {
		return Initiation{}, errors.New("peer name is empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.getOrCreatePeerLocked(peer, now)
	g.pruneExpiredLocked(state, now)
	markActivityIfUnset(state, now)

	ephemeral, err := newEphemeralPublicKey()
	if err != nil {
		return Initiation{}, err
	}

	for _, pending := range state.pending {
		g.removeTimerLocked(pending.timerID)
		delete(state.pending, pending.sessionID)
	}

	sessionID := g.newSessionIDLocked()
	pending := &pendingSession{
		sessionID:      sessionID,
		ephemeralPub:   ephemeral,
		initiatorIndex: sessionID,
		createdAt:      now,
		lastSent:       now,
	}
	pending.timerID = g.addTimerLocked()
	state.pending[sessionID] = pending

	return initiationFromPending(peer, pending), nil
}

// Retransmit resends the pending handshake initiation. It must reuse the
// original session id and ephemeral public key so the responder does not treat
// the retransmission as a new session.
func (g *HandshakeGuard) Retransmit(peer string, now time.Time) (Initiation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[strings.TrimSpace(peer)]
	if state == nil {
		return Initiation{}, fmt.Errorf("peer %q has no pending handshake", peer)
	}
	g.pruneExpiredLocked(state, now)

	pending := latestPending(state)
	if pending == nil {
		return Initiation{}, fmt.Errorf("peer %q has no pending handshake", peer)
	}
	if pending.attempt >= g.cfg.MaxRetransmits {
		return Initiation{}, fmt.Errorf("peer %q exceeded %d handshake retransmissions", peer, g.cfg.MaxRetransmits)
	}

	pending.attempt++
	pending.lastSent = now
	return initiationFromPending(peer, pending), nil
}

// PendingSessions returns the number of half-open handshakes for peer that are
// still inside the handshake timeout at now.
func (g *HandshakeGuard) PendingSessions(peer string, now time.Time) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[strings.TrimSpace(peer)]
	if state == nil {
		return 0, nil
	}
	g.pruneExpiredLocked(state, now)
	return len(state.pending), nil
}

// Respond completes an incoming initiation identified by initiatorIndex and
// returns the responder session id.
func (g *HandshakeGuard) Respond(peer string, initiatorIndex uint32, ephemeralPub string, now time.Time) (uint32, error) {
	peer = strings.TrimSpace(peer)
	if peer == "" {
		return 0, errors.New("peer name is empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.getOrCreatePeerLocked(peer, now)
	g.pruneExpiredLocked(state, now)
	markActivityIfUnset(state, now)

	if existing := findActiveByRemoteIndex(state, initiatorIndex); existing != nil {
		return existing.sessionID, nil
	}

	if pending := latestPending(state); pending != nil {
		g.promotePendingLocked(state, pending, initiatorIndex, ephemeralPub)
		return pending.sessionID, nil
	}

	sessionID := g.newSessionIDLocked()
	state.active[sessionID] = &activeSession{
		sessionID:       sessionID,
		remoteIndex:     initiatorIndex,
		remoteEphemeral: ephemeralPub,
		createdAt:       now,
	}
	return sessionID, nil
}

// RespondConcurrent resolves the case where both sides initiated at the same
// time. It returns the single surviving session id; no second session may be
// created.
func (g *HandshakeGuard) RespondConcurrent(peer string, theirSessionID uint32, now time.Time) (uint32, error) {
	peer = strings.TrimSpace(peer)
	if peer == "" {
		return 0, errors.New("peer name is empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.getOrCreatePeerLocked(peer, now)
	g.pruneExpiredLocked(state, now)
	markActivityIfUnset(state, now)

	if existing := findActiveByRemoteIndex(state, theirSessionID); existing != nil {
		return existing.sessionID, nil
	}
	if pending := latestPending(state); pending != nil {
		g.promotePendingLocked(state, pending, theirSessionID, "")
		return pending.sessionID, nil
	}

	if sessionID, ok := lowestActiveSessionID(state); ok {
		return sessionID, nil
	}

	return 0, fmt.Errorf("peer %q has no local simultaneous-initiation state", peer)
}

// ActiveSessions returns the number of established sessions for peer.
func (g *HandshakeGuard) ActiveSessions(peer string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[strings.TrimSpace(peer)]
	if state == nil {
		return 0, nil
	}
	return len(state.active), nil
}

// NoteInbound records an inbound packet of the given kind ("data" or
// "keepalive"). Only data may reset the idle timer.
func (g *HandshakeGuard) NoteInbound(peer, kind string, now time.Time) error {
	peer = strings.TrimSpace(peer)
	if peer == "" {
		return errors.New("peer name is empty")
	}
	if kind != "data" && kind != "keepalive" {
		return fmt.Errorf("unknown inbound packet kind %q", kind)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.getOrCreatePeerLocked(peer, now)
	if kind == "data" {
		state.lastData = now
		state.hasLastData = true
	}
	return nil
}

// IdleFor returns how long peer has been idle at now.
func (g *HandshakeGuard) IdleFor(peer string, now time.Time) (time.Duration, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[strings.TrimSpace(peer)]
	if state == nil || !state.hasLastData {
		return 0, nil
	}
	if now.Before(state.lastData) {
		return 0, nil
	}
	return now.Sub(state.lastData), nil
}

// CookieReply mints a cookie reply bound to sourceAddr.
func (g *HandshakeGuard) CookieReply(peer, sourceAddr string, now time.Time) ([]byte, error) {
	_ = now
	peer = strings.TrimSpace(peer)
	sourceAddr = strings.TrimSpace(sourceAddr)
	if peer == "" {
		return nil, errors.New("peer name is empty")
	}
	if sourceAddr == "" {
		return nil, errors.New("source address is empty")
	}
	return g.cookieFor(peer, sourceAddr), nil
}

// VerifyCookie checks a cookie reply against the current source address.
func (g *HandshakeGuard) VerifyCookie(peer, sourceAddr string, cookie []byte, now time.Time) (bool, error) {
	_ = now
	peer = strings.TrimSpace(peer)
	sourceAddr = strings.TrimSpace(sourceAddr)
	if peer == "" {
		return false, errors.New("peer name is empty")
	}
	if sourceAddr == "" || len(cookie) == 0 {
		return false, nil
	}
	return hmac.Equal(cookie, g.cookieFor(peer, sourceAddr)), nil
}

// ComputeMAC computes the handshake MAC over the body, including both the
// sender and the receiver index.
func (g *HandshakeGuard) ComputeMAC(peer string, senderIndex, receiverIndex uint32, body []byte) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.computeMACLocked(peer, senderIndex, receiverIndex, body)
}

// VerifyMAC checks a handshake MAC, including the receiver index.
func (g *HandshakeGuard) VerifyMAC(peer string, senderIndex, receiverIndex uint32, body, mac []byte) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	want, err := g.computeMACLocked(peer, senderIndex, receiverIndex, body)
	if err != nil {
		return false, err
	}
	return hmac.Equal(mac, want), nil
}

// AcceptCounter reports whether counter is acceptable in the replay window of
// the given key epoch. The window is shared by all message types of that epoch.
func (g *HandshakeGuard) AcceptCounter(peer string, epoch uint32, msgType uint8, counter uint64) (bool, error) {
	_ = msgType

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[strings.TrimSpace(peer)]
	if state == nil {
		return false, nil
	}
	window := state.epochs[epoch]
	if window == nil {
		return false, nil
	}
	return window.accept(counter, g.cfg.ReplayWindow), nil
}

// RotateKey starts a new key epoch and returns its number.
func (g *HandshakeGuard) RotateKey(peer string) (uint32, error) {
	peer = strings.TrimSpace(peer)
	if peer == "" {
		return 0, errors.New("peer name is empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.getOrCreatePeerLocked(peer, time.Time{})
	if state.lastEpoch == ^uint32(0) {
		return 0, errors.New("key epoch space exhausted")
	}
	next := state.lastEpoch + 1
	if next == 0 {
		return 0, errors.New("key epoch space exhausted")
	}

	state.previous = state.current
	state.current = next
	state.lastEpoch = next
	delete(state.drained, next)
	state.epochs[next] = &replayWindow{seen: make(map[uint64]struct{})}
	return next, nil
}

// DrainKey marks the previous epoch as drained.
func (g *HandshakeGuard) DrainKey(peer string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[strings.TrimSpace(peer)]
	if state == nil || state.previous == 0 {
		return fmt.Errorf("peer %q has no previous key epoch", peer)
	}
	state.drained[state.previous] = true
	return nil
}

// RetireKey removes an epoch. Packets from a retired epoch must be rejected.
func (g *HandshakeGuard) RetireKey(peer string, epoch uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[strings.TrimSpace(peer)]
	if state == nil {
		return nil
	}
	delete(state.epochs, epoch)
	delete(state.drained, epoch)
	if state.previous == epoch {
		state.previous = 0
	}
	if state.current == epoch {
		state.current = 0
	}
	return nil
}

// RegisterPeer binds a peer name to a public key. A public key may belong to
// exactly one peer.
func (g *HandshakeGuard) RegisterPeer(name, publicKey string) error {
	name = strings.TrimSpace(name)
	publicKey = canonicalPublicKey(publicKey)
	if name == "" {
		return errors.New("peer name is empty")
	}
	if publicKey == "" {
		return errors.New("public key is empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if owner, exists := g.publicKeys[publicKey]; exists && owner != name {
		return fmt.Errorf("public key already belongs to peer %q", owner)
	}

	state := g.peers[name]
	if state == nil {
		state = newPeerState(name)
		g.peers[name] = state
	}
	if state.publicKey != "" && state.publicKey != publicKey {
		return fmt.Errorf("peer %q already has a different public key", name)
	}

	state.publicKey = publicKey
	g.publicKeys[publicKey] = name
	return nil
}

// DeleteTunnel removes a tunnel and stops all of its timers.
func (g *HandshakeGuard) DeleteTunnel(name string) error {
	name = strings.TrimSpace(name)

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.peers[name]
	if state == nil {
		return nil
	}
	for _, pending := range state.pending {
		g.removeTimerLocked(pending.timerID)
	}
	if state.publicKey != "" {
		if owner, exists := g.publicKeys[state.publicKey]; exists && owner == name {
			delete(g.publicKeys, state.publicKey)
		}
	}
	delete(g.peers, name)
	return nil
}

// ActiveTimers returns the number of running retransmit/expiry timers.
func (g *HandshakeGuard) ActiveTimers() (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.timers), nil
}

// Snapshot serialises sessions and replay windows so that a restart resumes
// without accepting previously seen counters.
func (g *HandshakeGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap := snapshotData{
		Version:       snapshotVersion,
		ReplayWindow:  g.cfg.ReplayWindow,
		NextSessionID: g.nextSessionID,
		NextTimerID:   g.nextTimerID,
	}

	names := make([]string, 0, len(g.peers))
	for name := range g.peers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		state := g.peers[name]
		peerSnap := snapshotPeer{
			Name:          state.name,
			PublicKey:     state.publicKey,
			CurrentEpoch:  state.current,
			PreviousEpoch: state.previous,
			LastEpoch:     state.lastEpoch,
			LastData:      state.lastData,
			HasLastData:   state.hasLastData,
		}

		for _, id := range sortedSessionIDs(state.pending) {
			pending := state.pending[id]
			peerSnap.Pending = append(peerSnap.Pending, snapshotPending{
				SessionID:      pending.sessionID,
				EphemeralPub:   pending.ephemeralPub,
				InitiatorIndex: pending.initiatorIndex,
				Attempt:        pending.attempt,
				CreatedAt:      pending.createdAt,
				LastSent:       pending.lastSent,
				TimerID:        pending.timerID,
			})
		}

		activeIDs := make([]uint32, 0, len(state.active))
		for id := range state.active {
			activeIDs = append(activeIDs, id)
		}
		sort.Slice(activeIDs, func(i, j int) bool { return activeIDs[i] < activeIDs[j] })
		for _, id := range activeIDs {
			session := state.active[id]
			peerSnap.Active = append(peerSnap.Active, snapshotActive{
				SessionID:       session.sessionID,
				RemoteIndex:     session.remoteIndex,
				RemoteEphemeral: session.remoteEphemeral,
				CreatedAt:       session.createdAt,
			})
		}

		epochs := make([]uint32, 0, len(state.epochs))
		for epoch := range state.epochs {
			epochs = append(epochs, epoch)
		}
		sort.Slice(epochs, func(i, j int) bool { return epochs[i] < epochs[j] })
		for _, epoch := range epochs {
			window := state.epochs[epoch]
			seen := make([]uint64, 0, len(window.seen))
			for counter := range window.seen {
				seen = append(seen, counter)
			}
			sort.Slice(seen, func(i, j int) bool { return seen[i] < seen[j] })
			peerSnap.Epochs = append(peerSnap.Epochs, snapshotEpoch{
				Epoch:  epoch,
				Max:    window.max,
				MaxSet: window.maxSet,
				Seen:   seen,
			})
		}

		drained := make([]uint32, 0, len(state.drained))
		for epoch := range state.drained {
			drained = append(drained, epoch)
		}
		sort.Slice(drained, func(i, j int) bool { return drained[i] < drained[j] })
		peerSnap.Drained = drained

		snap.Peers = append(snap.Peers, peerSnap)
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("encode handshake snapshot: %w", err)
	}
	return data, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	if cfg.ReplayWindow == 0 {
		cfg.ReplayWindow = defaultReplayWindow
	}
	if cfg.MaxRetransmits < 0 {
		cfg.MaxRetransmits = 0
	}
	cfg.SharedKey = append([]byte(nil), cfg.SharedKey...)
	return cfg
}

func newPeerState(name string) *peerState {
	return &peerState{
		name:    name,
		pending: make(map[uint32]*pendingSession),
		active:  make(map[uint32]*activeSession),
		epochs:  make(map[uint32]*replayWindow),
		drained: make(map[uint32]bool),
	}
}

func (g *HandshakeGuard) getOrCreatePeerLocked(name string, now time.Time) *peerState {
	state := g.peers[name]
	if state != nil {
		return state
	}
	state = newPeerState(name)
	if !now.IsZero() {
		state.lastData = now
		state.hasLastData = true
	}
	g.peers[name] = state
	return state
}

func markActivityIfUnset(state *peerState, now time.Time) {
	if !state.hasLastData {
		state.lastData = now
		state.hasLastData = true
	}
}

func (g *HandshakeGuard) pruneExpiredLocked(state *peerState, now time.Time) {
	for id, pending := range state.pending {
		if now.Before(pending.createdAt.Add(g.cfg.HandshakeTimeout)) {
			continue
		}
		g.removeTimerLocked(pending.timerID)
		delete(state.pending, id)
	}
}

func (g *HandshakeGuard) promotePendingLocked(state *peerState, pending *pendingSession, remoteIndex uint32, remoteEphemeral string) {
	g.removeTimerLocked(pending.timerID)
	delete(state.pending, pending.sessionID)
	state.active[pending.sessionID] = &activeSession{
		sessionID:       pending.sessionID,
		remoteIndex:     remoteIndex,
		remoteEphemeral: remoteEphemeral,
		createdAt:       pending.createdAt,
	}
}

func (g *HandshakeGuard) newSessionIDLocked() uint32 {
	for {
		g.nextSessionID++
		if g.nextSessionID == 0 {
			continue
		}
		if !g.sessionIDInUseLocked(g.nextSessionID) {
			return g.nextSessionID
		}
	}
}

func (g *HandshakeGuard) sessionIDInUseLocked(id uint32) bool {
	for _, state := range g.peers {
		if _, exists := state.pending[id]; exists {
			return true
		}
		if _, exists := state.active[id]; exists {
			return true
		}
	}
	return false
}

func (g *HandshakeGuard) addTimerLocked() uint64 {
	for {
		g.nextTimerID++
		if g.nextTimerID == 0 {
			continue
		}
		if _, exists := g.timers[g.nextTimerID]; !exists {
			g.timers[g.nextTimerID] = struct{}{}
			return g.nextTimerID
		}
	}
}

func (g *HandshakeGuard) removeTimerLocked(id uint64) {
	delete(g.timers, id)
}

func (g *HandshakeGuard) cookieFor(peer, sourceAddr string) []byte {
	mac := hmac.New(sha256.New, g.cfg.SharedKey)
	_, _ = mac.Write([]byte("gsb-cookie-v1\x00"))
	_, _ = mac.Write([]byte(peer))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(sourceAddr))
	return mac.Sum(nil)
}

func (g *HandshakeGuard) computeMACLocked(peer string, senderIndex, receiverIndex uint32, body []byte) ([]byte, error) {
	peer = strings.TrimSpace(peer)
	state := g.peers[peer]
	if state == nil || state.publicKey == "" {
		return nil, fmt.Errorf("peer %q has no registered public key", peer)
	}

	key := make([]byte, 0, len(g.cfg.SharedKey)+1+len(state.publicKey))
	key = append(key, g.cfg.SharedKey...)
	key = append(key, 0)
	key = append(key, state.publicKey...)

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("gsb-handshake-mac-v1\x00"))
	var indexes [8]byte
	binary.BigEndian.PutUint32(indexes[0:4], senderIndex)
	binary.BigEndian.PutUint32(indexes[4:8], receiverIndex)
	_, _ = mac.Write(indexes[:])
	_, _ = mac.Write(body)
	return mac.Sum(nil), nil
}

func (w *replayWindow) accept(counter, size uint64) bool {
	if w.seen == nil {
		w.seen = make(map[uint64]struct{})
	}
	if size == 0 {
		size = defaultReplayWindow
	}

	if !w.maxSet {
		w.max = counter
		w.maxSet = true
		w.seen[counter] = struct{}{}
		return true
	}

	if counter > w.max {
		w.max = counter
		if w.max >= size {
			cutoff := w.max - size
			for seen := range w.seen {
				if seen <= cutoff {
					delete(w.seen, seen)
				}
			}
		}
	} else if w.max >= size && counter <= w.max-size {
		return false
	}

	if _, exists := w.seen[counter]; exists {
		return false
	}
	w.seen[counter] = struct{}{}
	return true
}

func initiationFromPending(peer string, pending *pendingSession) Initiation {
	return Initiation{
		Peer:           peer,
		SessionID:      pending.sessionID,
		EphemeralPub:   pending.ephemeralPub,
		InitiatorIndex: pending.initiatorIndex,
		Attempt:        pending.attempt,
	}
}

func latestPending(state *peerState) *pendingSession {
	var latest *pendingSession
	for _, pending := range state.pending {
		if latest == nil || pending.createdAt.After(latest.createdAt) ||
			(pending.createdAt.Equal(latest.createdAt) && pending.sessionID > latest.sessionID) {
			latest = pending
		}
	}
	return latest
}

func findActiveByRemoteIndex(state *peerState, remoteIndex uint32) *activeSession {
	for _, session := range state.active {
		if session.remoteIndex == remoteIndex {
			return session
		}
	}
	return nil
}

func lowestActiveSessionID(state *peerState) (uint32, bool) {
	var lowest uint32
	found := false
	for id := range state.active {
		if !found || id < lowest {
			lowest = id
			found = true
		}
	}
	return lowest, found
}

func newEphemeralPublicKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate ephemeral public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func sortedSessionIDs(sessions map[uint32]*pendingSession) []uint32 {
	ids := make([]uint32, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func maxSessionID(g *HandshakeGuard) uint32 {
	var max uint32
	for _, state := range g.peers {
		for id := range state.pending {
			if id > max {
				max = id
			}
		}
		for id := range state.active {
			if id > max {
				max = id
			}
		}
	}
	return max
}

func canonicalPublicKey(publicKey string) string {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" {
		return ""
	}
	if decoded, err := base64.StdEncoding.DecodeString(publicKey); err == nil {
		return base64.StdEncoding.EncodeToString(decoded)
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(publicKey); err == nil {
		return base64.StdEncoding.EncodeToString(decoded)
	}
	return publicKey
}

type snapshotData struct {
	Version       int            `json:"version"`
	ReplayWindow  uint64         `json:"replay_window,omitempty"`
	Peers         []snapshotPeer `json:"peers"`
	NextSessionID uint32         `json:"next_session_id"`
	NextTimerID   uint64         `json:"next_timer_id"`
}

type snapshotPeer struct {
	Name          string            `json:"name"`
	PublicKey     string            `json:"public_key,omitempty"`
	Pending       []snapshotPending `json:"pending,omitempty"`
	Active        []snapshotActive  `json:"active,omitempty"`
	Epochs        []snapshotEpoch   `json:"epochs,omitempty"`
	Drained       []uint32          `json:"drained,omitempty"`
	CurrentEpoch  uint32            `json:"current_epoch"`
	PreviousEpoch uint32            `json:"previous_epoch"`
	LastEpoch     uint32            `json:"last_epoch,omitempty"`
	LastData      time.Time         `json:"last_data,omitempty"`
	HasLastData   bool              `json:"has_last_data,omitempty"`
}

type snapshotPending struct {
	SessionID      uint32    `json:"session_id"`
	EphemeralPub   string    `json:"ephemeral_pub"`
	InitiatorIndex uint32    `json:"initiator_index"`
	Attempt        int       `json:"attempt"`
	CreatedAt      time.Time `json:"created_at"`
	LastSent       time.Time `json:"last_sent"`
	TimerID        uint64    `json:"timer_id,omitempty"`
}

type snapshotActive struct {
	SessionID       uint32    `json:"session_id"`
	RemoteIndex     uint32    `json:"remote_index"`
	RemoteEphemeral string    `json:"remote_ephemeral,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type snapshotEpoch struct {
	Epoch  uint32   `json:"epoch"`
	Max    uint64   `json:"max"`
	MaxSet bool     `json:"max_set"`
	Seen   []uint64 `json:"seen,omitempty"`
}
