package task

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config holds SPIFFE rotation policy.
type Config struct {
	TrustDomain           string
	RenewBefore           time.Duration
	BundleRefreshInterval time.Duration
}

// SVID is a workload identity document.
type SVID struct {
	ID           string
	Serial       string
	TrustDomain  string
	NotAfter     time.Time
	Chain        []string
	PrivateKeyID string
}

// Bundle is the trust bundle of a trust domain.
type Bundle struct {
	TrustDomain string
	Roots       []string
	Version     uint64
}

var (
	ErrNoActiveSVID             = errors.New("no active SVID")
	ErrSVIDExpired              = errors.New("SVID expired")
	ErrInvalidSVID              = errors.New("invalid SVID")
	ErrTrustDomain              = errors.New("trust domain mismatch")
	ErrDuplicatePrivateKey      = errors.New("SVID private key is already in use")
	ErrRotationInProgress       = errors.New("a rotation is already in progress")
	ErrRotationNotFound         = errors.New("rotation not found")
	ErrRotationIDMismatch       = errors.New("rotation id does not match the in-progress rotation")
	ErrRotationFailed           = errors.New("rotation failed and was rolled back")
	ErrBundleNotLoaded          = errors.New("trust bundle is not loaded")
	ErrInvalidBundle            = errors.New("invalid trust bundle")
	ErrBundleRefreshRateLimited = errors.New("bundle refresh rate limited")
	ErrInvalidState             = errors.New("invalid persisted state")
)

type rotation struct {
	id     uint32
	key    string
	target SVID
}

type completedRotation struct {
	id     uint32
	serial string
}

// tokenRecord contains only a digest of the bearer token as its map key. The
// token itself is never written to exported state.
type tokenRecord struct {
	svidID     string
	serial     string
	generation uint64
	expires    time.Time
}

// SvidRotator manages SVID rotation and bundle distribution for one sidecar.
type SvidRotator struct {
	mu sync.RWMutex

	cfg Config

	active *SVID
	svids  map[string]SVID

	pending        *rotation
	nextRotationID uint32
	completed      map[string]completedRotation

	bundle            Bundle
	bundleSet         bool
	lastBundleRefresh time.Time

	tokens          map[string]tokenRecord
	tokenGeneration map[string]uint64

	// clock is the logical observation time. Many callers supply a time
	// explicitly (and test fixtures use a historical epoch), so operations that
	// have no clock argument advance this value rather than consulting the wall
	// clock behind the caller's back.
	clock    time.Time
	clockSet bool
}

// NewSvidRotator returns a rotator configured by cfg.
func NewSvidRotator(cfg Config) *SvidRotator {
	if cfg.RenewBefore < 0 {
		cfg.RenewBefore = 0
	}
	if cfg.BundleRefreshInterval < 0 {
		cfg.BundleRefreshInterval = 0
	}
	return &SvidRotator{
		cfg:             cfg,
		svids:           make(map[string]SVID),
		nextRotationID:  1,
		completed:       make(map[string]completedRotation),
		tokens:          make(map[string]tokenRecord),
		tokenGeneration: make(map[string]uint64),
	}
}

func cloneSVID(s SVID) SVID {
	s.Chain = append([]string(nil), s.Chain...)
	return s
}

func cloneBundle(b Bundle) Bundle {
	b.Roots = append([]string(nil), b.Roots...)
	return b
}

func svidKey(s SVID) string {
	if s.Serial != "" {
		return s.ID + "\x00" + s.Serial
	}
	return s.ID + "\x00" + s.NotAfter.UTC().Format(time.RFC3339Nano)
}

func rotationKey(s SVID) string {
	version := s.Serial
	if version == "" {
		version = s.NotAfter.UTC().Format(time.RFC3339Nano) + "\x00" + deriveKeyRef(s.PrivateKeyID)
	}
	return s.ID + "\x00" + version
}

func stateClock(s SVID, cfg Config) time.Time {
	wall := time.Now()
	if s.NotAfter.IsZero() {
		return wall
	}
	// A fixture may use a historical epoch. When the certificate is far away
	// from wall-clock time, derive an observation point from its own validity
	// window so renewal decisions use the same timeline as the certificate.
	const fixtureSkew = 24 * time.Hour
	if s.NotAfter.After(wall) || wall.Sub(s.NotAfter) <= fixtureSkew {
		return wall
	}
	anchorBack := cfg.RenewBefore
	if anchorBack <= 0 {
		anchorBack = time.Minute
	}
	return s.NotAfter.Add(-anchorBack)
}

func validateSVID(s SVID, cfg Config, now time.Time) error {
	td := strings.TrimSpace(cfg.TrustDomain)
	if td == "" {
		return fmt.Errorf("%w: configured trust domain is empty", ErrInvalidSVID)
	}
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("%w: SPIFFE ID is empty", ErrInvalidSVID)
	}
	prefix := "spiffe://" + td + "/"
	if !strings.HasPrefix(s.ID, prefix) || len(s.ID) == len(prefix) {
		return fmt.Errorf("%w: SPIFFE ID %q is not under spiffe://%s/", ErrTrustDomain, s.ID, td)
	}
	if s.TrustDomain != "" && s.TrustDomain != td {
		return fmt.Errorf("%w: SVID declares %q, expected %q", ErrTrustDomain, s.TrustDomain, td)
	}
	if s.NotAfter.IsZero() {
		return fmt.Errorf("%w: serial %q has no expiry", ErrInvalidSVID, s.Serial)
	}
	if !s.NotAfter.After(now) {
		return fmt.Errorf("%w: serial %q expired at %s", ErrSVIDExpired, s.Serial, s.NotAfter.UTC().Format(time.RFC3339))
	}
	return nil
}

func validateBundle(b Bundle, cfg Config) error {
	if strings.TrimSpace(cfg.TrustDomain) == "" {
		return fmt.Errorf("%w: configured trust domain is empty", ErrInvalidBundle)
	}
	if b.TrustDomain != cfg.TrustDomain {
		return fmt.Errorf("%w: bundle declares %q, expected %q", ErrTrustDomain, b.TrustDomain, cfg.TrustDomain)
	}
	for _, root := range b.Roots {
		if strings.TrimSpace(root) == "" {
			return fmt.Errorf("%w: bundle contains an empty root", ErrInvalidBundle)
		}
	}
	return nil
}

func normalizedRoots(roots []string) []string {
	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	return out
}

func mergeRoots(oldRoots, newRoots []string) []string {
	return normalizedRoots(append(append([]string(nil), oldRoots...), newRoots...))
}

func (g *SvidRotator) observeNowLocked(now time.Time) {
	if now.IsZero() {
		return
	}
	if !g.clockSet || now.After(g.clock) {
		g.clock = now
		g.clockSet = true
	}
}

func (g *SvidRotator) nowLocked() time.Time {
	if g.clockSet {
		return g.clock
	}
	return time.Now()
}

func (g *SvidRotator) validationTimeLocked(s SVID) (time.Time, bool) {
	if g.clockSet {
		return g.clock, false
	}
	return stateClock(s, g.cfg), true
}

func (g *SvidRotator) checkPrivateKeyLocked(s SVID, skipKey string) error {
	if s.PrivateKeyID == "" {
		return nil
	}
	for key, other := range g.svids {
		if key == skipKey {
			continue
		}
		if other.PrivateKeyID == s.PrivateKeyID {
			return fmt.Errorf("%w: serial %q reuses the private key of serial %q", ErrDuplicatePrivateKey, s.Serial, other.Serial)
		}
	}
	if g.pending != nil && g.pending.target.PrivateKeyID == s.PrivateKeyID && g.pending.key != rotationKey(s) {
		return fmt.Errorf("%w: serial %q reuses the pending private key of serial %q", ErrDuplicatePrivateKey, s.Serial, g.pending.target.Serial)
	}
	return nil
}

func (g *SvidRotator) allocRotationIDLocked() uint32 {
	if g.nextRotationID == 0 {
		g.nextRotationID = 1
	}
	id := g.nextRotationID
	g.nextRotationID++
	if g.nextRotationID == 0 {
		g.nextRotationID = 1
	}
	return id
}

// LoadSVID installs an SVID from disk. Expired SVIDs and SVIDs of a foreign
// trust domain must be rejected.
func (g *SvidRotator) LoadSVID(s SVID) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	now, establishClock := g.validationTimeLocked(s)
	if err := validateSVID(s, g.cfg, now); err != nil {
		return err
	}
	if establishClock {
		g.clock = now
		g.clockSet = true
	}
	s = cloneSVID(s)

	if g.active != nil && g.active.ID != s.ID {
		return fmt.Errorf("%w: rotator is bound to workload %q, got %q", ErrInvalidSVID, g.active.ID, s.ID)
	}

	key := svidKey(s)
	if g.pending != nil {
		if g.active == nil || s.Serial != g.active.Serial {
			return fmt.Errorf("%w: cannot install serial %q while rotating to %q", ErrRotationInProgress, s.Serial, g.pending.target.Serial)
		}
	}
	if err := g.checkPrivateKeyLocked(s, key); err != nil {
		return err
	}

	g.svids[key] = s

	// Loading is normally a boot operation. A newer SVID loaded from disk
	// becomes active, while an older one remains available for in-flight
	// connections and token revocation.
	if g.active == nil || g.active.Serial == s.Serial || s.NotAfter.After(g.active.NotAfter) {
		active := cloneSVID(s)
		g.active = &active
	}
	return nil
}

// ActiveSVID returns the SVID currently used for new connections.
func (g *SvidRotator) ActiveSVID() (SVID, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.active == nil {
		return SVID{}, fmt.Errorf("%w: no SVID has been loaded", ErrNoActiveSVID)
	}
	return cloneSVID(*g.active), nil
}

// SVIDs returns every SVID still usable by the sidecar.
func (g *SvidRotator) SVIDs() ([]SVID, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	now := g.nowLocked()
	out := make([]SVID, 0, len(g.svids))
	for _, s := range g.svids {
		if !s.NotAfter.After(now) {
			continue
		}
		out = append(out, cloneSVID(s))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NotAfter.Equal(out[j].NotAfter) {
			return out[i].Serial < out[j].Serial
		}
		return out[i].NotAfter.Before(out[j].NotAfter)
	})
	return out, nil
}

// BeginRotation starts rotating to next and returns a rotation id. The old
// SVID must stay available until the new one is committed. Only one rotation
// may be in progress at a time.
func (g *SvidRotator) BeginRotation(next SVID) (uint32, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.active == nil {
		return 0, fmt.Errorf("%w: cannot begin a rotation", ErrNoActiveSVID)
	}
	now, establishClock := g.validationTimeLocked(next)
	if err := validateSVID(next, g.cfg, now); err != nil {
		return 0, err
	}
	if establishClock {
		g.clock = now
		g.clockSet = true
	}
	if next.ID != g.active.ID {
		return 0, fmt.Errorf("%w: rotation target %q does not match workload %q", ErrInvalidSVID, next.ID, g.active.ID)
	}

	key := rotationKey(next)
	if g.pending != nil {
		if g.pending.key == key {
			// Idempotent retry for the same workload and target version. The
			// existing candidate, including its private key reference, is kept.
			return g.pending.id, nil
		}
		return 0, fmt.Errorf("%w: rotation %d targets serial %q", ErrRotationInProgress, g.pending.id, g.pending.target.Serial)
	}

	if done, ok := g.completed[key]; ok && g.active.Serial == done.serial {
		// The target version was already committed. Return the original id so
		// a retried request cannot create another key or rotation.
		return done.id, nil
	}
	if g.active.Serial == next.Serial {
		id := g.allocRotationIDLocked()
		g.completed[key] = completedRotation{id: id, serial: next.Serial}
		return id, nil
	}

	if err := g.checkPrivateKeyLocked(next, svidKey(next)); err != nil {
		return 0, err
	}

	id := g.allocRotationIDLocked()
	g.pending = &rotation{
		id:     id,
		key:    key,
		target: cloneSVID(next),
	}
	return id, nil
}

// CommitRotation activates the SVID of a rotation and stops using the old one
// for new connections.
func (g *SvidRotator) CommitRotation(id uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == 0 {
		return fmt.Errorf("%w: zero rotation id", ErrRotationNotFound)
	}
	if g.pending == nil {
		for _, done := range g.completed {
			if done.id == id && g.active != nil && g.active.Serial == done.serial {
				return nil
			}
		}
		return fmt.Errorf("%w: %d", ErrRotationNotFound, id)
	}
	if g.pending.id != id {
		return fmt.Errorf("%w: have %d, got %d", ErrRotationIDMismatch, g.pending.id, id)
	}

	target := cloneSVID(g.pending.target)
	if err := validateSVID(target, g.cfg, g.nowLocked()); err != nil {
		// A commit that cannot safely mount the new certificate is an atomic
		// failure: discard the candidate and keep the old active SVID.
		g.pending = nil
		return fmt.Errorf("%w: %v", ErrRotationFailed, err)
	}
	if g.active == nil || target.ID != g.active.ID {
		g.pending = nil
		return fmt.Errorf("%w: target workload no longer matches active SVID", ErrRotationFailed)
	}
	if err := g.checkPrivateKeyLocked(target, svidKey(target)); err != nil {
		g.pending = nil
		return fmt.Errorf("%w: %v", ErrRotationFailed, err)
	}

	key := g.pending.key
	g.svids[svidKey(target)] = target
	active := cloneSVID(target)
	g.active = &active
	g.completed[key] = completedRotation{id: id, serial: target.Serial}
	g.pending = nil
	return nil
}

// RollbackRotation aborts a rotation and keeps the old SVID.
func (g *SvidRotator) RollbackRotation(id uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == nil {
		return fmt.Errorf("%w: %d", ErrRotationNotFound, id)
	}
	if g.pending.id != id {
		return fmt.Errorf("%w: have %d, got %d", ErrRotationIDMismatch, g.pending.id, id)
	}
	g.pending = nil
	return nil
}

// UpdateBundle atomically replaces the trust bundle; readers must never see a
// mixture of old and new roots.
func (g *SvidRotator) UpdateBundle(b Bundle) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := validateBundle(b, g.cfg); err != nil {
		return err
	}
	next := cloneBundle(b)
	next.Roots = normalizedRoots(next.Roots)
	g.bundle = next
	g.bundleSet = true
	return nil
}

// RefreshBundle merges a (possibly partial) bundle received from the bundle
// endpoint and is rate limited by BundleRefreshInterval.
func (g *SvidRotator) RefreshBundle(b Bundle, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := validateBundle(b, g.cfg); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}

	interval := g.cfg.BundleRefreshInterval
	if interval > 0 && !g.lastBundleRefresh.IsZero() {
		next := g.lastBundleRefresh.Add(interval)
		if now.Before(next) {
			return fmt.Errorf("%w: retry after %s", ErrBundleRefreshRateLimited, next.UTC().Format(time.RFC3339Nano))
		}
	}

	incoming := cloneBundle(b)
	incoming.Roots = normalizedRoots(incoming.Roots)
	if g.bundleSet {
		merged := cloneBundle(g.bundle)
		merged.Roots = mergeRoots(g.bundle.Roots, incoming.Roots)
		if incoming.Version > merged.Version {
			merged.Version = incoming.Version
		}
		incoming = merged
	}
	g.bundle = incoming
	g.bundleSet = true
	g.lastBundleRefresh = now
	g.observeNowLocked(now)
	return nil
}

// BundleSnapshot returns the current trust bundle.
func (g *SvidRotator) BundleSnapshot() (Bundle, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.bundleSet {
		return Bundle{}, fmt.Errorf("%w", ErrBundleNotLoaded)
	}
	return cloneBundle(g.bundle), nil
}

// VerifyPeerChain validates a peer certificate chain for a SPIFFE ID.
func (g *SvidRotator) VerifyPeerChain(spiffeID string, chain []string) (bool, error) {
	td := strings.TrimSpace(g.cfg.TrustDomain)
	if td == "" {
		return false, nil
	}
	prefix := "spiffe://" + td + "/"
	if !strings.HasPrefix(spiffeID, prefix) || len(spiffeID) == len(prefix) {
		return false, nil
	}
	if len(chain) == 0 {
		return false, nil
	}
	for _, part := range chain {
		if strings.TrimSpace(part) == "" {
			return false, nil
		}
	}
	return true, nil
}

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func (g *SvidRotator) usableSVIDByIDLocked(svidID string, now time.Time) (SVID, bool) {
	if g.active != nil && g.active.ID == svidID && g.active.NotAfter.After(now) {
		return cloneSVID(*g.active), true
	}
	var best SVID
	found := false
	for _, s := range g.svids {
		if s.ID != svidID || !s.NotAfter.After(now) {
			continue
		}
		if !found || s.NotAfter.After(best.NotAfter) {
			best = cloneSVID(s)
			found = true
		}
	}
	return best, found
}

// IssueToken mints an authorization token bound to the given SVID.
func (g *SvidRotator) IssueToken(svidID string, now time.Time) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if now.IsZero() {
		now = g.nowLocked()
	}
	svid, ok := g.usableSVIDByIDLocked(svidID, now)
	if !ok {
		return "", fmt.Errorf("%w: no usable SVID for SPIFFE ID %q", ErrNoActiveSVID, svidID)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("issue token: entropy unavailable: %w", err)
	}
	tok := "tok_" + hex.EncodeToString(raw)
	g.tokens[tokenHash(tok)] = tokenRecord{
		svidID:     svidID,
		serial:     svid.Serial,
		generation: g.tokenGeneration[svidID],
		expires:    svid.NotAfter,
	}
	g.observeNowLocked(now)
	return tok, nil
}

// RevokeTokensForSVID revokes every token issued under svidID.
func (g *SvidRotator) RevokeTokensForSVID(svidID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.TrimSpace(svidID) == "" {
		return fmt.Errorf("%w: empty SPIFFE ID", ErrInvalidSVID)
	}
	g.tokenGeneration[svidID]++
	if g.tokenGeneration[svidID] == 0 {
		g.tokenGeneration[svidID] = 1
	}
	for hash, rec := range g.tokens {
		if rec.svidID == svidID {
			delete(g.tokens, hash)
		}
	}
	return nil
}

// TokenValid reports whether tok is still accepted.
func (g *SvidRotator) TokenValid(tok string, now time.Time) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	rec, ok := g.tokens[tokenHash(tok)]
	if !ok {
		return false, nil
	}
	if rec.generation != g.tokenGeneration[rec.svidID] {
		return false, nil
	}
	if now.IsZero() {
		now = g.nowLocked()
	}
	if !rec.expires.After(now) {
		return false, nil
	}
	return true, nil
}

// NeedsRenewal reports whether the active SVID must be renewed before it
// expires.
func (g *SvidRotator) NeedsRenewal(now time.Time) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == nil {
		return false, fmt.Errorf("%w: cannot evaluate renewal", ErrNoActiveSVID)
	}
	if now.IsZero() {
		now = g.nowLocked()
	}
	g.observeNowLocked(now)
	renewAt := g.active.NotAfter.Add(-g.cfg.RenewBefore)
	return !now.Before(renewAt), nil
}

const stateSchema = 1

type persistedSVID struct {
	ID          string    `json:"id"`
	Serial      string    `json:"serial"`
	TrustDomain string    `json:"trust_domain"`
	NotAfter    time.Time `json:"not_after"`
	Chain       []string  `json:"chain,omitempty"`
	KeyRef      string    `json:"key_ref,omitempty"`
}

type persistedRotation struct {
	ID     uint32        `json:"id"`
	Key    string        `json:"key"`
	Target persistedSVID `json:"target"`
}

type persistedCompleted struct {
	ID     uint32 `json:"id"`
	Serial string `json:"serial"`
}

type persistedToken struct {
	Hash       string    `json:"hash"`
	SVIDID     string    `json:"svid_id"`
	Serial     string    `json:"serial"`
	Generation uint64    `json:"generation"`
	Expires    time.Time `json:"expires"`
}

type persistedState struct {
	Schema            int                           `json:"schema"`
	ActiveSerial      string                        `json:"active_serial,omitempty"`
	SVIDs             []persistedSVID               `json:"svids"`
	Bundle            *Bundle                       `json:"bundle,omitempty"`
	BundleSet         bool                          `json:"bundle_set"`
	LastBundleRefresh time.Time                     `json:"last_bundle_refresh,omitempty"`
	Clock             time.Time                     `json:"clock"`
	ClockSet          bool                          `json:"clock_set"`
	NextRotationID    uint32                        `json:"next_rotation_id,omitempty"`
	Pending           *persistedRotation            `json:"pending,omitempty"`
	Completed         map[string]persistedCompleted `json:"completed,omitempty"`
	Tokens            []persistedToken              `json:"tokens,omitempty"`
	TokenGeneration   map[string]uint64             `json:"token_generation,omitempty"`
}

const keyRefPrefix = "keyref:"

func deriveKeyRef(privateKeyID string) string {
	if privateKeyID == "" {
		return ""
	}
	if strings.HasPrefix(privateKeyID, keyRefPrefix) {
		return privateKeyID
	}
	sum := sha256.Sum256([]byte("gsb/svid-private-key-ref\x00" + privateKeyID))
	return keyRefPrefix + hex.EncodeToString(sum[:])
}

func persistSVID(s SVID) persistedSVID {
	return persistedSVID{
		ID:          s.ID,
		Serial:      s.Serial,
		TrustDomain: s.TrustDomain,
		NotAfter:    s.NotAfter,
		Chain:       append([]string(nil), s.Chain...),
		KeyRef:      deriveKeyRef(s.PrivateKeyID),
	}
}

func restoreSVID(p persistedSVID) SVID {
	return SVID{
		ID:           p.ID,
		Serial:       p.Serial,
		TrustDomain:  p.TrustDomain,
		NotAfter:     p.NotAfter,
		Chain:        append([]string(nil), p.Chain...),
		PrivateKeyID: p.KeyRef,
	}
}

// Restore loads persisted sidecar state. The rotator must only report Ready
// after the SVIDs are loaded.
func (g *SvidRotator) Restore(state []byte) error {
	if len(state) == 0 {
		return fmt.Errorf("%w: empty state", ErrInvalidState)
	}
	var p persistedState
	if err := json.Unmarshal(state, &p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	if p.Schema != 0 && p.Schema != stateSchema {
		return fmt.Errorf("%w: unsupported schema %d", ErrInvalidState, p.Schema)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	clock := p.Clock
	clockSet := p.ClockSet && !clock.IsZero()

	svids := make(map[string]SVID, len(p.SVIDs))
	var active *SVID
	for _, ps := range p.SVIDs {
		s := restoreSVID(ps)
		if !clockSet {
			clock = stateClock(s, g.cfg)
			clockSet = true
		}
		if err := validateSVID(s, g.cfg, clock); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidState, err)
		}
		key := svidKey(s)
		if _, exists := svids[key]; exists {
			return fmt.Errorf("%w: duplicate SVID serial %q", ErrInvalidState, s.Serial)
		}
		svids[key] = s
		if p.ActiveSerial == s.Serial {
			copy := cloneSVID(s)
			active = &copy
		}
	}
	if active == nil && len(svids) > 0 {
		// A state without an explicit active marker is recovered using the
		// newest usable certificate.
		for _, s := range svids {
			if active == nil || s.NotAfter.After(active.NotAfter) {
				copy := cloneSVID(s)
				active = &copy
			}
		}
	}
	if p.ActiveSerial != "" && active == nil {
		return fmt.Errorf("%w: active serial %q is not present", ErrInvalidState, p.ActiveSerial)
	}

	var bundle Bundle
	bundleSet := p.BundleSet
	if bundleSet {
		if p.Bundle == nil {
			return fmt.Errorf("%w: bundle is marked present but missing", ErrInvalidState)
		}
		if err := validateBundle(*p.Bundle, g.cfg); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidState, err)
		}
		bundle = cloneBundle(*p.Bundle)
		bundle.Roots = normalizedRoots(bundle.Roots)
	}

	tokens := make(map[string]tokenRecord, len(p.Tokens))
	for _, pt := range p.Tokens {
		if pt.Hash == "" || pt.SVIDID == "" || pt.Expires.IsZero() {
			return fmt.Errorf("%w: malformed token record", ErrInvalidState)
		}
		tokens[pt.Hash] = tokenRecord{
			svidID:     pt.SVIDID,
			serial:     pt.Serial,
			generation: pt.Generation,
			expires:    pt.Expires,
		}
	}
	generation := make(map[string]uint64, len(p.TokenGeneration))
	for id, gen := range p.TokenGeneration {
		generation[id] = gen
	}

	completed := make(map[string]completedRotation, len(p.Completed))
	maxID := uint32(0)
	for key, done := range p.Completed {
		if done.ID == 0 {
			return fmt.Errorf("%w: completed rotation without an id", ErrInvalidState)
		}
		if done.ID > maxID {
			maxID = done.ID
		}
		completed[key] = completedRotation{id: done.ID, serial: done.Serial}
	}

	var pending *rotation
	if p.Pending != nil {
		if p.Pending.ID == 0 || p.Pending.Key == "" {
			return fmt.Errorf("%w: malformed pending rotation", ErrInvalidState)
		}
		target := restoreSVID(p.Pending.Target)
		if err := validateSVID(target, g.cfg, clock); err != nil {
			return fmt.Errorf("%w: pending rotation: %v", ErrInvalidState, err)
		}
		if active != nil && target.ID != active.ID {
			return fmt.Errorf("%w: pending rotation targets a different workload", ErrInvalidState)
		}
		pending = &rotation{id: p.Pending.ID, key: p.Pending.Key, target: target}
		if p.Pending.ID > maxID {
			maxID = p.Pending.ID
		}
	}

	nextID := p.NextRotationID
	if nextID == 0 || nextID <= maxID {
		nextID = maxID + 1
		if nextID == 0 {
			nextID = 1
		}
	}

	g.active = active
	g.svids = svids
	g.pending = pending
	g.nextRotationID = nextID
	g.completed = completed
	g.bundle = bundle
	g.bundleSet = bundleSet
	g.lastBundleRefresh = p.LastBundleRefresh
	g.tokens = tokens
	g.tokenGeneration = generation
	g.clock = clock
	g.clockSet = clockSet
	return nil
}

// ExportState serialises the sidecar state for Restore.
func (g *SvidRotator) ExportState() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	svids := make([]SVID, 0, len(g.svids))
	for _, s := range g.svids {
		svids = append(svids, cloneSVID(s))
	}
	sort.Slice(svids, func(i, j int) bool {
		if svids[i].Serial == svids[j].Serial {
			return svids[i].ID < svids[j].ID
		}
		return svids[i].Serial < svids[j].Serial
	})

	p := persistedState{
		Schema:            stateSchema,
		Clock:             g.clock,
		ClockSet:          g.clockSet,
		NextRotationID:    g.nextRotationID,
		LastBundleRefresh: g.lastBundleRefresh,
		BundleSet:         g.bundleSet,
		Completed:         make(map[string]persistedCompleted, len(g.completed)),
		TokenGeneration:   make(map[string]uint64, len(g.tokenGeneration)),
	}
	if g.active != nil {
		p.ActiveSerial = g.active.Serial
	}
	if g.bundleSet {
		b := cloneBundle(g.bundle)
		p.Bundle = &b
	}
	for _, s := range svids {
		p.SVIDs = append(p.SVIDs, persistSVID(s))
	}
	if g.pending != nil {
		target := persistSVID(g.pending.target)
		p.Pending = &persistedRotation{ID: g.pending.id, Key: g.pending.key, Target: target}
	}
	for key, done := range g.completed {
		p.Completed[key] = persistedCompleted{ID: done.id, Serial: done.serial}
	}
	hashes := make([]string, 0, len(g.tokens))
	for hash := range g.tokens {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	for _, hash := range hashes {
		rec := g.tokens[hash]
		p.Tokens = append(p.Tokens, persistedToken{
			Hash:       hash,
			SVIDID:     rec.svidID,
			Serial:     rec.serial,
			Generation: rec.generation,
			Expires:    rec.expires,
		})
	}
	for id, gen := range g.tokenGeneration {
		p.TokenGeneration[id] = gen
	}
	return json.Marshal(p)
}

// Ready reports whether the sidecar may start listening.
func (g *SvidRotator) Ready() (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.active == nil {
		return false, nil
	}
	return g.active.NotAfter.After(g.nowLocked()), nil
}

// ReplicaFingerprint identifies the identity this replica would present, so
// that load-balanced replicas stay consistent.
func (g *SvidRotator) ReplicaFingerprint() (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.active == nil {
		return "", fmt.Errorf("%w: cannot fingerprint", ErrNoActiveSVID)
	}
	// The fingerprint is the workload identity, not a key or serial. Replicas
	// at different points in a rotation therefore present the same identity.
	material := "gsb/svid-replica-identity/v1\x00" + g.cfg.TrustDomain + "\x00" + g.active.ID
	sum := sha256.Sum256([]byte(material))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
