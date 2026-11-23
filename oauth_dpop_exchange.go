package task

import (
	"container/heap"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinels returned by Exchange. Callers must be able to tell a replayed proof
// from a proof with a broken signature.
var (
	ErrProofInvalid = errors.New("dpop: invalid proof")
	ErrSignature    = errors.New("dpop: invalid signature")
	ErrReplay       = errors.New("dpop: replayed proof")
	ErrNonce        = errors.New("dpop: invalid nonce")
)

const (
	defaultMaxSkew    = 30 * time.Second
	defaultJTITTL     = 5 * time.Minute
	defaultNonceTTL   = time.Minute
	defaultMaxJTIs    = 4096
	defaultMaxNonces  = 4096
	issuedTokenTTL    = time.Hour
	snapshotVersion   = 1
	randomValueBytes  = 32
	jtiTombstoneRatio = 2
)

// Config holds DPoP policy.
type Config struct {
	// MaxSkew is the tolerated clock difference for the proof iat claim.
	MaxSkew time.Duration
	// JTITTL is how long a proof id is remembered.
	JTITTL time.Duration
	// NonceTTL is how long a server-issued nonce stays usable.
	NonceTTL time.Duration
}

// Proof is a DPoP proof as presented by the client.
type Proof struct {
	JTI string
	// HTM is the HTTP method the proof was made for.
	HTM string
	// HTU is the full HTTP URI the proof was made for.
	HTU string
	// ATH is the lower-case hex SHA-256 of the access token.
	ATH string
	// Alg is the signature algorithm; "none" must never be accepted.
	Alg        string
	Thumbprint string
	IAT        time.Time
	Nonce      string
	TenantID   string
	// SignatureValid is the result of signature verification.
	SignatureValid bool
}

// Token is an access or refresh token bound to a key thumbprint.
type Token struct {
	Value      string
	TenantID   string
	Thumbprint string
	ExpiresAt  time.Time
}

// ExchangeRequest is the token request a proof is presented with.
type ExchangeRequest struct {
	Method       string
	URL          string
	AccessToken  string
	RefreshToken string
	TenantID     string
}

type accessTokenRecord struct {
	tenantID   string
	thumbprint string
	expiresAt  time.Time
}

type refreshTokenRecord struct {
	tenantID   string
	thumbprint string
}

type nonceRecord struct {
	tenantID  string
	expiresAt time.Time
}

type jtiRecord struct {
	jti       string
	expiresAt time.Time
	sequence  uint64
}

type jtiQueue []*jtiRecord

func (q jtiQueue) Len() int { return len(q) }

func (q jtiQueue) Less(i, j int) bool {
	if q[i].expiresAt.Equal(q[j].expiresAt) {
		return q[i].sequence < q[j].sequence
	}
	return q[i].expiresAt.Before(q[j].expiresAt)
}

func (q jtiQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *jtiQueue) Push(x any) { *q = append(*q, x.(*jtiRecord)) }

func (q *jtiQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return item
}

// DpopExchange validates DPoP proofs and performs token exchange.
type DpopExchange struct {
	cfg Config

	mu sync.Mutex

	// jtis is the bounded replay cache. The heap always contains exactly one
	// entry per live cache record and is ordered by expiry.
	jtis     map[string]*jtiRecord
	jtiHeap  jtiQueue
	sequence uint64

	maxJTIs   int
	maxNonces int

	// tombstones remember recently evicted proof ids for one TTL so a replay
	// made possible by capacity eviction can be counted internally. This cache
	// is bounded independently of the active replay cache.
	tombstones    map[string]*jtiRecord
	tombstoneHeap jtiQueue

	jtiEvictions   uint64
	evictedReplays uint64

	nonces map[string]*nonceRecord

	accessTokens  map[string]accessTokenRecord
	refreshTokens map[string]refreshTokenRecord

	// clockOffset keeps Exchange's explicit clock and JTIUsed/IssueNonce's
	// implicit wall clock in the same time domain.
	clockOffset time.Duration
	lastNow     time.Time
	haveClock   bool
}

// NewDpopExchange returns an exchange configured by cfg.
func NewDpopExchange(cfg Config) *DpopExchange {
	if cfg.MaxSkew <= 0 {
		cfg.MaxSkew = defaultMaxSkew
	}
	if cfg.JTITTL <= 0 {
		cfg.JTITTL = defaultJTITTL
	}
	if cfg.NonceTTL <= 0 {
		cfg.NonceTTL = defaultNonceTTL
	}
	return &DpopExchange{
		cfg:           cfg,
		jtis:          make(map[string]*jtiRecord),
		tombstones:    make(map[string]*jtiRecord),
		nonces:        make(map[string]*nonceRecord),
		accessTokens:  make(map[string]accessTokenRecord),
		refreshTokens: make(map[string]refreshTokenRecord),
		maxJTIs:       defaultMaxJTIs,
		maxNonces:     defaultMaxNonces,
	}
}

// NewDpopExchangeFromSnapshot restores an exchange, including the used proof
// ids, from a snapshot.
func NewDpopExchangeFromSnapshot(cfg Config, data []byte) (*DpopExchange, error) {
	if len(data) == 0 {
		return nil, errors.New("dpop: empty snapshot")
	}
	var snap exchangeSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("dpop: decode snapshot: %w", err)
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("dpop: unsupported snapshot version %d", snap.Version)
	}

	g := NewDpopExchange(cfg)
	g.clockOffset = time.Duration(snap.ClockOffset)
	g.haveClock = snap.HaveClock
	g.sequence = snap.Sequence
	g.jtiEvictions = snap.JTIEvictions
	g.evictedReplays = snap.EvictedReplays
	now := g.issueTimeLocked()

	for _, item := range snap.JTIs {
		if item.JTI == "" {
			continue
		}
		expiry := time.Unix(0, item.ExpiresAt)
		if !expiry.After(now) {
			continue
		}
		if existing, ok := g.jtis[item.JTI]; ok && !existing.expiresAt.Before(expiry) {
			continue
		}
		record := &jtiRecord{jti: item.JTI, expiresAt: expiry, sequence: g.nextSequenceLocked()}
		g.jtis[item.JTI] = record
		heap.Push(&g.jtiHeap, record)
	}
	g.trimJTIsLocked(now)

	for _, item := range snap.EvictedJTIs {
		if item.JTI == "" {
			continue
		}
		expiry := time.Unix(0, item.ExpiresAt)
		if !expiry.After(now) {
			continue
		}
		g.addTombstoneLocked(item.JTI, expiry)
	}
	g.trimTombstonesLocked()

	for _, item := range snap.Nonces {
		if item.Value == "" {
			continue
		}
		expiry := time.Unix(0, item.ExpiresAt)
		if !expiry.After(now) {
			continue
		}
		g.nonces[item.Value] = &nonceRecord{tenantID: item.TenantID, expiresAt: expiry}
	}
	g.trimNoncesLocked()

	for _, item := range snap.AccessTokens {
		if item.Value == "" {
			continue
		}
		var expiry time.Time
		if item.ExpiresAt != 0 {
			expiry = time.Unix(0, item.ExpiresAt)
			if !expiry.After(now) {
				continue
			}
		}
		g.accessTokens[item.Value] = accessTokenRecord{
			tenantID:   item.TenantID,
			thumbprint: item.Thumbprint,
			expiresAt:  expiry,
		}
	}
	for _, item := range snap.RefreshTokens {
		if item.Value == "" {
			continue
		}
		g.refreshTokens[item.Value] = refreshTokenRecord{
			tenantID:   item.TenantID,
			thumbprint: item.Thumbprint,
		}
	}
	return g, nil
}

// RegisterAccessToken records a token that proofs may be bound to via ath.
func (g *DpopExchange) RegisterAccessToken(t Token) error {
	if t.Value == "" {
		return errors.New("dpop: empty access token")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.accessTokens[t.Value] = accessTokenRecord{
		tenantID:   t.TenantID,
		thumbprint: t.Thumbprint,
		expiresAt:  t.ExpiresAt,
	}
	return nil
}

// RegisterRefreshToken records a refresh token bound to a key thumbprint.
func (g *DpopExchange) RegisterRefreshToken(value, thumbprint, tenantID string) error {
	if value == "" || thumbprint == "" || tenantID == "" {
		return errors.New("dpop: refresh token, thumbprint and tenant are required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshTokens[value] = refreshTokenRecord{
		tenantID:   tenantID,
		thumbprint: thumbprint,
	}
	return nil
}

// IssueNonce mints a server nonce.
func (g *DpopExchange) IssueNonce() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.issueTimeLocked()
	g.pruneNoncesLocked(now)

	var nonce string
	for {
		value, err := randomValue("")
		if err != nil {
			return "", err
		}
		if _, exists := g.nonces[value]; !exists {
			nonce = value
			break
		}
	}

	for len(g.nonces) >= g.maxNonces {
		g.evictEarliestNonceLocked()
	}
	g.nonces[nonce] = &nonceRecord{expiresAt: now.Add(g.cfg.NonceTTL)}
	return nonce, nil
}

// Exchange validates proof against req and returns a new access token.
//
// The proof must match the method and full URL exactly, carry the ath of the
// presented access token, be bound to the registered thumbprint, use a real
// signature algorithm, stay inside the clock skew, come from the same tenant,
// present a fresh server nonce, and use an unseen jti. The refresh token is
// rotated on success.
func (g *DpopExchange) Exchange(proof Proof, req ExchangeRequest, now time.Time) (Token, error) {
	if now.IsZero() {
		now = time.Now()
	}

	if !validSignatureAlgorithm(proof.Alg) {
		return Token{}, ErrProofInvalid
	}
	if !proof.SignatureValid {
		return Token{}, ErrSignature
	}
	if proof.JTI == "" {
		return Token{}, ErrProofInvalid
	}
	if req.Method == "" || req.URL == "" || proof.HTM != req.Method || proof.HTU != req.URL {
		return Token{}, ErrProofInvalid
	}
	if proof.TenantID == "" || req.TenantID == "" || proof.TenantID != req.TenantID {
		return Token{}, ErrProofInvalid
	}
	if proof.Thumbprint == "" {
		return Token{}, ErrProofInvalid
	}

	skew := now.Sub(proof.IAT)
	if skew < 0 {
		skew = -skew
	}
	if skew > g.cfg.MaxSkew {
		return Token{}, ErrProofInvalid
	}

	if req.AccessToken == "" || req.RefreshToken == "" {
		return Token{}, ErrProofInvalid
	}
	if !constantTimeEqual(strings.ToLower(proof.ATH), accessTokenHash(req.AccessToken)) {
		return Token{}, ErrProofInvalid
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.clockOffset = now.Sub(time.Now())
	g.lastNow = now
	g.haveClock = true
	g.pruneExpiredLocked(now)

	access, ok := g.accessTokens[req.AccessToken]
	if !ok || access.tenantID != req.TenantID || access.thumbprint != proof.Thumbprint {
		return Token{}, ErrProofInvalid
	}
	if !access.expiresAt.IsZero() && !access.expiresAt.After(now) {
		return Token{}, ErrProofInvalid
	}

	// Replay detection precedes nonce and refresh-token checks so an exact
	// replay is always reported as ErrReplay, never as a stale nonce/token.
	if _, used := g.jtis[proof.JTI]; used {
		return Token{}, ErrReplay
	}

	nonce, ok := g.nonces[proof.Nonce]
	if !ok {
		return Token{}, ErrNonce
	}
	if !nonce.expiresAt.After(now) {
		delete(g.nonces, proof.Nonce)
		return Token{}, ErrNonce
	}
	if nonce.tenantID == "" {
		nonce.tenantID = proof.TenantID
	} else if nonce.tenantID != proof.TenantID {
		return Token{}, ErrNonce
	}

	refresh, ok := g.refreshTokens[req.RefreshToken]
	if !ok || refresh.tenantID != req.TenantID || refresh.thumbprint != proof.Thumbprint {
		return Token{}, ErrProofInvalid
	}

	accessValue, err := randomValue("at_")
	if err != nil {
		return Token{}, err
	}
	refreshValue, err := randomValue("rt_")
	if err != nil {
		return Token{}, err
	}

	g.consumeJTILocked(proof.JTI, now)
	delete(g.nonces, proof.Nonce)

	g.refreshTokens[refreshValue] = refreshTokenRecord{
		tenantID:   refresh.tenantID,
		thumbprint: refresh.thumbprint,
	}
	delete(g.refreshTokens, req.RefreshToken)

	token := Token{
		Value:      accessValue,
		TenantID:   access.tenantID,
		Thumbprint: access.thumbprint,
		ExpiresAt:  now.Add(issuedTokenTTL),
	}
	g.accessTokens[token.Value] = accessTokenRecord{
		tenantID:   token.TenantID,
		thumbprint: token.Thumbprint,
		expiresAt:  token.ExpiresAt,
	}
	return token, nil
}

// JTIUsed reports whether a proof id has already been consumed and is still
// inside its TTL.
func (g *DpopExchange) JTIUsed(jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneJTIsLocked(g.logicalNowLocked())
	_, used := g.jtis[jti]
	return used, nil
}

// Snapshot serialises the used proof ids so that a restarted server keeps
// rejecting replays.
func (g *DpopExchange) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.logicalNowLocked()
	g.pruneExpiredLocked(now)

	snap := exchangeSnapshot{
		Version:        snapshotVersion,
		ClockOffset:    int64(g.clockOffset),
		HaveClock:      g.haveClock,
		Sequence:       g.sequence,
		JTIEvictions:   g.jtiEvictions,
		EvictedReplays: g.evictedReplays,
	}

	for _, record := range g.jtis {
		snap.JTIs = append(snap.JTIs, snapshotJTI{JTI: record.jti, ExpiresAt: record.expiresAt.UnixNano()})
	}
	sort.Slice(snap.JTIs, func(i, j int) bool {
		if snap.JTIs[i].ExpiresAt == snap.JTIs[j].ExpiresAt {
			return snap.JTIs[i].JTI < snap.JTIs[j].JTI
		}
		return snap.JTIs[i].ExpiresAt < snap.JTIs[j].ExpiresAt
	})

	for _, record := range g.tombstones {
		snap.EvictedJTIs = append(snap.EvictedJTIs, snapshotJTI{JTI: record.jti, ExpiresAt: record.expiresAt.UnixNano()})
	}
	sort.Slice(snap.EvictedJTIs, func(i, j int) bool {
		if snap.EvictedJTIs[i].ExpiresAt == snap.EvictedJTIs[j].ExpiresAt {
			return snap.EvictedJTIs[i].JTI < snap.EvictedJTIs[j].JTI
		}
		return snap.EvictedJTIs[i].ExpiresAt < snap.EvictedJTIs[j].ExpiresAt
	})

	for value, record := range g.nonces {
		snap.Nonces = append(snap.Nonces, snapshotNonce{
			Value:     value,
			TenantID:  record.tenantID,
			ExpiresAt: record.expiresAt.UnixNano(),
		})
	}
	sort.Slice(snap.Nonces, func(i, j int) bool {
		if snap.Nonces[i].ExpiresAt == snap.Nonces[j].ExpiresAt {
			return snap.Nonces[i].Value < snap.Nonces[j].Value
		}
		return snap.Nonces[i].ExpiresAt < snap.Nonces[j].ExpiresAt
	})

	for value, record := range g.accessTokens {
		item := snapshotAccessToken{
			Value:      value,
			TenantID:   record.tenantID,
			Thumbprint: record.thumbprint,
		}
		if !record.expiresAt.IsZero() {
			item.ExpiresAt = record.expiresAt.UnixNano()
		}
		snap.AccessTokens = append(snap.AccessTokens, item)
	}
	sort.Slice(snap.AccessTokens, func(i, j int) bool {
		return snap.AccessTokens[i].Value < snap.AccessTokens[j].Value
	})

	for value, record := range g.refreshTokens {
		snap.RefreshTokens = append(snap.RefreshTokens, snapshotRefreshToken{
			Value:      value,
			TenantID:   record.tenantID,
			Thumbprint: record.thumbprint,
		})
	}
	sort.Slice(snap.RefreshTokens, func(i, j int) bool {
		return snap.RefreshTokens[i].Value < snap.RefreshTokens[j].Value
	})

	return json.Marshal(snap)
}

type exchangeSnapshot struct {
	Version        int                    `json:"version"`
	ClockOffset    int64                  `json:"clock_offset_ns"`
	HaveClock      bool                   `json:"have_clock"`
	Sequence       uint64                 `json:"sequence,omitempty"`
	JTIEvictions   uint64                 `json:"jti_evictions,omitempty"`
	EvictedReplays uint64                 `json:"evicted_replays,omitempty"`
	JTIs           []snapshotJTI          `json:"jtis,omitempty"`
	EvictedJTIs    []snapshotJTI          `json:"evicted_jtis,omitempty"`
	Nonces         []snapshotNonce        `json:"nonces,omitempty"`
	AccessTokens   []snapshotAccessToken  `json:"access_tokens,omitempty"`
	RefreshTokens  []snapshotRefreshToken `json:"refresh_tokens,omitempty"`
}

type snapshotJTI struct {
	JTI       string `json:"jti"`
	ExpiresAt int64  `json:"expires_at"`
}

type snapshotNonce struct {
	Value     string `json:"value"`
	TenantID  string `json:"tenant_id"`
	ExpiresAt int64  `json:"expires_at"`
}

type snapshotAccessToken struct {
	Value      string `json:"value"`
	TenantID   string `json:"tenant_id"`
	Thumbprint string `json:"thumbprint"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

type snapshotRefreshToken struct {
	Value      string `json:"value"`
	TenantID   string `json:"tenant_id"`
	Thumbprint string `json:"thumbprint"`
}

func (g *DpopExchange) logicalNowLocked() time.Time {
	return time.Now().Add(g.clockOffset)
}

func (g *DpopExchange) issueTimeLocked() time.Time {
	now := g.logicalNowLocked()
	if !g.haveClock || g.lastNow.IsZero() {
		return now
	}
	// Exchange sets its clock explicitly. When a nonce is minted immediately
	// afterwards, keep the sub-second wall-clock drift out of the nonce's
	// expiry so a nonce minted at T expires exactly at T+NonceTTL.
	if elapsed := now.Sub(g.lastNow); elapsed >= 0 && elapsed < time.Second {
		return g.lastNow
	}
	return now
}

func (g *DpopExchange) nextSequenceLocked() uint64 {
	sequence := g.sequence
	g.sequence++
	return sequence
}

func (g *DpopExchange) pruneExpiredLocked(now time.Time) {
	g.pruneJTIsLocked(now)
	g.pruneTombstonesLocked(now)
	g.pruneNoncesLocked(now)
	for value, record := range g.accessTokens {
		if !record.expiresAt.IsZero() && !record.expiresAt.After(now) {
			delete(g.accessTokens, value)
		}
	}
}

func (g *DpopExchange) pruneJTIsLocked(now time.Time) {
	for g.jtiHeap.Len() > 0 {
		record := g.jtiHeap[0]
		if record.expiresAt.After(now) {
			break
		}
		heap.Pop(&g.jtiHeap)
		if current, ok := g.jtis[record.jti]; ok && current == record {
			delete(g.jtis, record.jti)
		}
	}
}

func (g *DpopExchange) pruneTombstonesLocked(now time.Time) {
	for g.tombstoneHeap.Len() > 0 {
		record := g.tombstoneHeap[0]
		if record.expiresAt.After(now) {
			break
		}
		heap.Pop(&g.tombstoneHeap)
		if current, ok := g.tombstones[record.jti]; ok && current == record {
			delete(g.tombstones, record.jti)
		}
	}
}

func (g *DpopExchange) pruneNoncesLocked(now time.Time) {
	for value, record := range g.nonces {
		if !record.expiresAt.After(now) {
			delete(g.nonces, value)
		}
	}
}

func (g *DpopExchange) consumeJTILocked(jti string, now time.Time) {
	g.pruneJTIsLocked(now)

	if tombstone, ok := g.tombstones[jti]; ok {
		if tombstone.expiresAt.After(now) {
			g.evictedReplays++
		}
		delete(g.tombstones, jti)
	}

	for len(g.jtis) >= g.maxJTIs {
		record := heap.Pop(&g.jtiHeap).(*jtiRecord)
		if current, ok := g.jtis[record.jti]; !ok || current != record {
			continue
		}
		delete(g.jtis, record.jti)
		g.jtiEvictions++
		g.addTombstoneLocked(record.jti, record.expiresAt)
	}

	record := &jtiRecord{
		jti:       jti,
		expiresAt: now.Add(g.cfg.JTITTL),
		sequence:  g.nextSequenceLocked(),
	}
	g.jtis[jti] = record
	heap.Push(&g.jtiHeap, record)
}

func (g *DpopExchange) addTombstoneLocked(jti string, expiresAt time.Time) {
	if jti == "" || !expiresAt.After(g.logicalNowLocked()) {
		return
	}
	if existing, ok := g.tombstones[jti]; ok {
		if !existing.expiresAt.Before(expiresAt) {
			return
		}
		delete(g.tombstones, jti)
	}

	for len(g.tombstones) >= g.maxJTIs {
		record := heap.Pop(&g.tombstoneHeap).(*jtiRecord)
		if current, ok := g.tombstones[record.jti]; ok && current == record {
			delete(g.tombstones, record.jti)
		}
	}
	record := &jtiRecord{
		jti:       jti,
		expiresAt: expiresAt,
		sequence:  g.nextSequenceLocked(),
	}
	g.tombstones[jti] = record
	heap.Push(&g.tombstoneHeap, record)
	if g.tombstoneHeap.Len() > jtiTombstoneRatio*g.maxJTIs {
		g.rebuildTombstoneHeapLocked()
	}
}

func (g *DpopExchange) rebuildTombstoneHeapLocked() {
	records := make(jtiQueue, 0, len(g.tombstones))
	for _, record := range g.tombstones {
		records = append(records, record)
	}
	heap.Init(&records)
	g.tombstoneHeap = records
}

func (g *DpopExchange) trimJTIsLocked(now time.Time) {
	g.pruneJTIsLocked(now)
	for len(g.jtis) > g.maxJTIs {
		record := heap.Pop(&g.jtiHeap).(*jtiRecord)
		if current, ok := g.jtis[record.jti]; ok && current == record {
			delete(g.jtis, record.jti)
			g.jtiEvictions++
		}
	}
}

func (g *DpopExchange) trimTombstonesLocked() {
	g.pruneTombstonesLocked(g.logicalNowLocked())
	for len(g.tombstones) > g.maxJTIs {
		record := heap.Pop(&g.tombstoneHeap).(*jtiRecord)
		if current, ok := g.tombstones[record.jti]; ok && current == record {
			delete(g.tombstones, record.jti)
		}
	}
}

func (g *DpopExchange) trimNoncesLocked() {
	g.pruneNoncesLocked(g.logicalNowLocked())
	for len(g.nonces) > g.maxNonces {
		g.evictEarliestNonceLocked()
	}
}

func (g *DpopExchange) evictEarliestNonceLocked() {
	var earliest string
	var earliestExpiry time.Time
	for value, record := range g.nonces {
		if earliest == "" || record.expiresAt.Before(earliestExpiry) {
			earliest = value
			earliestExpiry = record.expiresAt
		}
	}
	if earliest != "" {
		delete(g.nonces, earliest)
	}
}

func validSignatureAlgorithm(alg string) bool {
	switch alg {
	case "RS256", "RS384", "RS512",
		"PS256", "PS384", "PS512",
		"ES256", "ES384", "ES512",
		"EdDSA":
		return true
	default:
		return false
	}
}

func accessTokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randomValue(prefix string) (string, error) {
	buf := make([]byte, randomValueBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("dpop: random value: %w", err)
	}
	return prefix + hex.EncodeToString(buf), nil
}
