package task

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
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

	mu            sync.Mutex
	keys          map[uint16][]byte
	activeVersion uint16
	hasActive     bool
	earlySuites   map[uint16]bool
	tickets       map[string]*ticketState
	psks          map[string]bool
	sessions      map[string]*sessionState
	clock         uint64
}

type ticketState struct {
	used      bool
	firstUse  time.Time
	earlyData bool
	revoked   bool
}

type sessionState struct {
	id         string
	clientID   string
	active     bool
	lastAccess uint64
}

// ticketPlain is the plaintext sealed inside a ticket.
type ticketPlain struct {
	ID              string `json:"id"`
	ClientID        string `json:"cid"`
	SNI             string `json:"sni"`
	ALPN            string `json:"alpn"`
	CertFingerprint string `json:"fp"`
	IssuedAt        int64  `json:"iat"`
}

// keyRingExport is the wire format shared between cluster nodes.
type keyRingExport struct {
	Active    uint16            `json:"active"`
	HasActive bool              `json:"has_active"`
	Keys      map[string]string `json:"keys"`
}

func newTicketGuard(cfg Config) *TicketGuard {
	return &TicketGuard{
		cfg:         cfg,
		keys:        make(map[uint16][]byte),
		earlySuites: make(map[uint16]bool),
		tickets:     make(map[string]*ticketState),
		psks:        make(map[string]bool),
		sessions:    make(map[string]*sessionState),
	}
}

// NewTicketGuard returns a guard with its own ticket key ring.
func NewTicketGuard(cfg Config) *TicketGuard {
	return newTicketGuard(cfg)
}

// NewTicketGuardWithKeyRing returns a guard sharing a key ring exported from
// another node.
func NewTicketGuardWithKeyRing(cfg Config, ring []byte) *TicketGuard {
	g := newTicketGuard(cfg)
	var exported keyRingExport
	if err := json.Unmarshal(ring, &exported); err != nil {
		return g
	}
	for versionStr, keyB64 := range exported.Keys {
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(keyB64)
		if err != nil || len(key) == 0 {
			continue
		}
		g.keys[uint16(version)] = key
	}
	g.activeVersion = exported.Active
	g.hasActive = exported.HasActive
	return g
}

// AddTicketKey installs key material for a ticket version.
func (g *TicketGuard) AddTicketKey(version uint16, key []byte) error {
	if len(key) == 0 {
		return errors.New("ticket key must not be empty")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keys[version] = append([]byte(nil), key...)
	if !g.hasActive {
		g.activeVersion = version
		g.hasActive = true
	}
	return nil
}

// RotateTicketKey makes version the active issuance key and keeps the previous
// key available for the configured grace period.
func (g *TicketGuard) RotateTicketKey(version uint16, key []byte) error {
	if len(key) == 0 {
		return errors.New("ticket key must not be empty")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keys[version] = append([]byte(nil), key...)
	g.activeVersion = version
	g.hasActive = true
	// Previous keys stay in the ring so in-flight resumptions keep working;
	// they are removed only by an explicit RetireTicketKey once the
	// configured RotationGrace has elapsed.
	return nil
}

// RetireTicketKey removes a version from the ring immediately.
func (g *TicketGuard) RetireTicketKey(version uint16) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.keys[version]; !ok {
		return errors.New("unknown ticket key version")
	}
	delete(g.keys, version)
	if g.hasActive && g.activeVersion == version {
		g.hasActive = false
	}
	return nil
}

// ExportKeyRing serialises the key ring so other cluster nodes can open
// tickets issued here.
func (g *TicketGuard) ExportKeyRing() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	exported := keyRingExport{
		Active:    g.activeVersion,
		HasActive: g.hasActive,
		Keys:      make(map[string]string, len(g.keys)),
	}
	for version, key := range g.keys {
		exported.Keys[strconv.Itoa(int(version))] = base64.StdEncoding.EncodeToString(key)
	}
	return json.Marshal(exported)
}

// IssueTicket seals a new ticket bound to the client, SNI, ALPN and client
// certificate fingerprint.
func (g *TicketGuard) IssueTicket(req IssueRequest) (Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.hasActive {
		return Ticket{}, errors.New("no active ticket key")
	}
	key, ok := g.keys[g.activeVersion]
	if !ok {
		return Ticket{}, errors.New("active ticket key missing")
	}
	plain := ticketPlain{
		ID:              randomHex(16),
		ClientID:        req.ClientID,
		SNI:             req.SNI,
		ALPN:            req.ALPN,
		CertFingerprint: req.CertFingerprint,
		IssuedAt:        req.Now.Unix(),
	}
	encoded, err := json.Marshal(plain)
	if err != nil {
		return Ticket{}, err
	}
	sealed, err := sealTicket(key, encoded)
	if err != nil {
		return Ticket{}, err
	}
	ticket := Ticket{ID: plain.ID, Version: g.activeVersion, Sealed: sealed}
	g.tickets[ticket.ID] = &ticketState{}
	g.psks[ticket.ID] = true
	return ticket, nil
}

// Resume validates a ticket and returns the established session.
//
// The request must match the ALPN, SNI and certificate fingerprint recorded at
// issuance, the ticket key version must still be available, the ticket must not
// be replayed, and the returned session must request a key update.
func (g *TicketGuard) Resume(req ResumeRequest) (ResumeResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	reject := func(reason string) ResumeResult {
		return ResumeResult{OK: false, Reason: reason}
	}

	key, ok := g.keys[req.Ticket.Version]
	if !ok {
		return reject("unknown ticket key version"), nil
	}
	encoded, err := openTicket(key, req.Ticket.Sealed)
	if err != nil {
		return reject("ticket cannot be opened with this key"), nil
	}
	var plain ticketPlain
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return reject("malformed ticket"), nil
	}
	if plain.ID != req.Ticket.ID {
		return reject("ticket id mismatch"), nil
	}
	if plain.SNI != req.SNI {
		return reject("sni mismatch"), nil
	}
	if plain.ALPN != req.ALPN {
		return reject("alpn mismatch"), nil
	}
	if plain.CertFingerprint != req.CertFingerprint {
		return reject("client certificate mismatch"), nil
	}

	state, ok := g.tickets[plain.ID]
	if !ok {
		state = &ticketState{}
		g.tickets[plain.ID] = state
	}
	if state.revoked {
		return reject("ticket revoked"), nil
	}

	earlyAllowed := false
	if req.WantEarlyData {
		if len(g.earlySuites) == 0 {
			earlyAllowed = true
		} else {
			for _, suite := range req.OfferedSuites {
				if g.earlySuites[suite] {
					earlyAllowed = true
					break
				}
			}
		}
	}

	if state.used {
		if state.earlyData || req.WantEarlyData {
			// A ticket that carried (or is asked to carry) early data is
			// single-use: a second presentation is a replay, so revoke its
			// PSK.
			state.revoked = true
			g.psks[plain.ID] = false
			return reject("ticket replay detected"), nil
		}
		// The lifetime is measured from the first use, not from issuance.
		if req.Now.After(state.firstUse.Add(g.cfg.TicketLifetime)) {
			return reject("ticket expired"), nil
		}
	} else {
		state.used = true
		state.firstUse = req.Now
	}
	if req.WantEarlyData && earlyAllowed {
		state.earlyData = true
	}

	// The session cache key includes the client certificate fingerprint so
	// two certificates of the same client never share a session, and an
	// already established session is never replaced by a later resumption.
	cacheKey := plain.ClientID + "\x00" + plain.CertFingerprint
	g.clock++
	if sess, ok := g.sessions[cacheKey]; ok {
		sess.lastAccess = g.clock
		return ResumeResult{
			OK:               true,
			SessionID:        sess.id,
			NeedsKeyUpdate:   true,
			EarlyDataAllowed: earlyAllowed,
		}, nil
	}
	sess := &sessionState{
		id:         randomHex(16),
		clientID:   plain.ClientID,
		lastAccess: g.clock,
	}
	g.insertSession(cacheKey, sess)
	return ResumeResult{
		OK:               true,
		SessionID:        sess.id,
		NeedsKeyUpdate:   true,
		EarlyDataAllowed: earlyAllowed,
	}, nil
}

// insertSession stores a session, evicting the least recently used idle
// session when the cache is full. Active sessions are never evicted; if every
// cached session is active the new session is simply not cached.
func (g *TicketGuard) insertSession(cacheKey string, sess *sessionState) {
	if g.cfg.MaxSessions > 0 && len(g.sessions) >= g.cfg.MaxSessions {
		evictKey := ""
		var oldest uint64
		found := false
		for key, candidate := range g.sessions {
			if candidate.active {
				continue
			}
			if !found || candidate.lastAccess < oldest {
				found = true
				oldest = candidate.lastAccess
				evictKey = key
			}
		}
		if !found {
			return
		}
		delete(g.sessions, evictKey)
	}
	g.sessions[cacheKey] = sess
}

// SetEarlyDataSuites replaces the policy of cipher suites allowed for 0-RTT.
func (g *TicketGuard) SetEarlyDataSuites(suites []uint16) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.earlySuites = make(map[uint16]bool, len(suites))
	for _, suite := range suites {
		g.earlySuites[suite] = true
	}
	return nil
}

// SessionFor returns the session id currently bound to clientID, or "".
func (g *TicketGuard) SessionFor(clientID string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, sess := range g.sessions {
		if sess.clientID == clientID {
			return sess.id, nil
		}
	}
	return "", nil
}

// MarkSessionActive pins a session so that cache eviction skips it.
func (g *TicketGuard) MarkSessionActive(clientID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, sess := range g.sessions {
		if sess.clientID == clientID {
			sess.active = true
		}
	}
	return nil
}

// ActiveSessions returns the number of pinned sessions.
func (g *TicketGuard) ActiveSessions() (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, sess := range g.sessions {
		if sess.active {
			count++
		}
	}
	return count, nil
}

// CacheSize returns the number of cached sessions.
func (g *TicketGuard) CacheSize() (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sessions), nil
}

// PSKActive reports whether the PSK of a ticket may still be used.
func (g *TicketGuard) PSKActive(ticketID string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.psks[ticketID], nil
}

func deriveAEADKey(material []byte) []byte {
	sum := sha256.Sum256(material)
	return sum[:]
}

func sealTicket(material, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveAEADKey(material))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

func openTicket(material, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveAEADKey(material))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("sealed ticket too short")
	}
	nonce := sealed[:aead.NonceSize()]
	ciphertext := sealed[aead.NonceSize():]
	return aead.Open(nil, nonce, ciphertext, nil)
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
