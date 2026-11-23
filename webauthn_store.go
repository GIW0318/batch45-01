package task

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config holds WebAuthn relying party policy.
type Config struct {
	RPID                 string
	Origin               string
	RequireUV            bool
	AllowSelfAttestation bool
	TrustedRoots         []string
	ChallengeTTL         time.Duration
}

// Attestation describes the attestation statement of a registration.
type Attestation struct {
	Format     string
	Chain      []string
	SelfSigned bool
	// Root is the trust anchor of the chain.
	Root string
}

// Registration is one authenticator registration request.
type Registration struct {
	TenantID     string
	UserID       string
	Username     string
	CredentialID string
	UserHandle   string
	PublicKey    []byte
	Origin       string
	// RPIDHash is the lower-case hex SHA-256 of the relying party id.
	RPIDHash    string
	UV          bool
	BE          bool
	BS          bool
	Attestation Attestation
	Challenge   string
	Counter     uint32
}

// Credential is a stored credential.
type Credential struct {
	TenantID     string
	UserID       string
	Username     string
	CredentialID string
	UserHandle   string
	Origin       string
	BE           bool
	BS           bool
	Frozen       bool
	Counter      uint32
}

// AuthResult is the outcome of an authentication.
type AuthResult struct {
	OK      bool
	Reason  string
	Counter uint32
}

// CredentialStore holds WebAuthn credentials.
type CredentialStore struct {
	mu          sync.Mutex
	cfg         Config
	credentials map[string]*storedCredential
	handles     map[string]map[string]string
	challenges  map[string]challengeEntry
	lastCreated time.Time
}

// storedCredential is the internal record kept per credential id.
type storedCredential struct {
	TenantID     string    `json:"tenant_id"`
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	CredentialID string    `json:"credential_id"`
	UserHandle   string    `json:"user_handle"`
	Origin       string    `json:"origin"`
	PublicKey    []byte    `json:"public_key"`
	RPIDHash     string    `json:"rp_id_hash"`
	BE           bool      `json:"be"`
	BS           bool      `json:"bs"`
	Frozen       bool      `json:"frozen"`
	Counter      uint32    `json:"counter"`
	CreatedAt    time.Time `json:"created_at"`
}

// challengeEntry is a one-shot registration challenge.
type challengeEntry struct {
	UserID    string
	ExpiresAt time.Time
}

// snapshotData is the persisted form of the store.
type snapshotData struct {
	Credentials []storedCredential           `json:"credentials"`
	Handles     map[string]map[string]string `json:"handles"`
}

// NewCredentialStore returns a store configured by cfg.
func NewCredentialStore(cfg Config) *CredentialStore {
	return &CredentialStore{
		cfg:         cfg,
		credentials: make(map[string]*storedCredential),
		handles:     make(map[string]map[string]string),
		challenges:  make(map[string]challengeEntry),
	}
}

// NewCredentialStoreFromSnapshot restores a store from a snapshot.
func NewCredentialStoreFromSnapshot(cfg Config, data []byte) *CredentialStore {
	g := NewCredentialStore(cfg)
	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return g
	}
	for i := range snap.Credentials {
		rec := snap.Credentials[i]
		copied := rec
		g.credentials[copied.CredentialID] = &copied
		if copied.CreatedAt.After(g.lastCreated) {
			g.lastCreated = copied.CreatedAt
		}
	}
	for tenant, handles := range snap.Handles {
		bound := make(map[string]string, len(handles))
		for handle, userID := range handles {
			bound[handle] = userID
		}
		g.handles[tenant] = bound
	}
	return g
}

// BeginChallenge mints a registration challenge for userID.
func (g *CredentialStore) BeginChallenge(userID string) (string, error) {
	if userID == "" {
		return "", errors.New("challenge rejected: empty user id")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("challenge rejected: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	ttl := g.cfg.ChallengeTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	g.mu.Lock()
	g.challenges[token] = challengeEntry{UserID: userID, ExpiresAt: time.Now().Add(ttl)}
	g.mu.Unlock()
	return token, nil
}

// FinishRegistration validates and stores a registration.
//
// The challenge must be unexpired and bound to the user, the origin must match
// exactly, the RP ID hash must match, UV must be present when required, the
// attestation chain must be evaluated against the trusted roots, the credential
// id must be globally unique, and a user handle may belong to one account only.
func (g *CredentialStore) FinishRegistration(r Registration) (Credential, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	entry, ok := g.challenges[r.Challenge]
	if !ok {
		return Credential{}, errors.New("registration rejected: unknown or consumed challenge")
	}
	delete(g.challenges, r.Challenge)
	if time.Now().After(entry.ExpiresAt) {
		return Credential{}, errors.New("registration rejected: challenge expired")
	}
	if entry.UserID != r.UserID {
		return Credential{}, errors.New("registration rejected: challenge bound to another user")
	}
	if r.Origin != g.cfg.Origin {
		return Credential{}, errors.New("registration rejected: origin mismatch")
	}
	if !strings.EqualFold(r.RPIDHash, expectedRPIDHash(g.cfg.RPID)) {
		return Credential{}, errors.New("registration rejected: rp id hash mismatch")
	}
	if g.cfg.RequireUV && !r.UV {
		return Credential{}, errors.New("registration rejected: user verification required")
	}
	if err := g.checkAttestation(r.Attestation); err != nil {
		return Credential{}, err
	}
	if _, exists := g.credentials[r.CredentialID]; exists {
		return Credential{}, errors.New("registration rejected: credential id already registered")
	}
	bound := g.handles[r.TenantID]
	if bound == nil {
		bound = make(map[string]string)
		g.handles[r.TenantID] = bound
	}
	if owner, exists := bound[r.UserHandle]; exists && owner != r.UserID {
		return Credential{}, errors.New("registration rejected: user handle bound to another account")
	}

	now := time.Now()
	if !now.After(g.lastCreated) {
		now = g.lastCreated.Add(time.Nanosecond)
	}
	g.lastCreated = now

	rec := &storedCredential{
		TenantID:     r.TenantID,
		UserID:       r.UserID,
		Username:     r.Username,
		CredentialID: r.CredentialID,
		UserHandle:   r.UserHandle,
		Origin:       r.Origin,
		PublicKey:    append([]byte(nil), r.PublicKey...),
		RPIDHash:     strings.ToLower(r.RPIDHash),
		BE:           r.BE,
		BS:           r.BS,
		Counter:      r.Counter,
		CreatedAt:    now,
	}
	g.credentials[r.CredentialID] = rec
	bound[r.UserHandle] = r.UserID
	return toCredential(rec), nil
}

// checkAttestation evaluates the attestation statement against policy.
func (g *CredentialStore) checkAttestation(a Attestation) error {
	if a.SelfSigned {
		if !g.cfg.AllowSelfAttestation {
			return errors.New("registration rejected: self-signed attestation not allowed")
		}
		return nil
	}
	if len(a.Chain) == 0 {
		return errors.New("registration rejected: empty attestation chain")
	}
	if a.Chain[len(a.Chain)-1] != a.Root {
		return errors.New("registration rejected: attestation chain does not terminate at its root")
	}
	for _, root := range g.cfg.TrustedRoots {
		if a.Root == root {
			return nil
		}
	}
	return errors.New("registration rejected: attestation root is not trusted")
}

// Authenticate verifies an assertion against a stored credential.
func (g *CredentialStore) Authenticate(tenantID, credentialID, origin, rpIDHash string, uv bool, counter uint32) (AuthResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.credentials[credentialID]
	if !ok || rec.TenantID != tenantID {
		return AuthResult{OK: false, Reason: "credential not found"}, nil
	}
	if rec.Frozen {
		return AuthResult{OK: false, Reason: "credential frozen"}, nil
	}
	if origin != g.cfg.Origin {
		return AuthResult{OK: false, Reason: "origin does not match exactly"}, nil
	}
	if !strings.EqualFold(rpIDHash, rec.RPIDHash) {
		return AuthResult{OK: false, Reason: "rp id hash mismatch"}, nil
	}
	if g.cfg.RequireUV && !uv {
		return AuthResult{OK: false, Reason: "user verification required"}, nil
	}
	if counter <= rec.Counter {
		return AuthResult{OK: false, Reason: "signature counter did not strictly increase"}, nil
	}
	rec.Counter = counter
	return AuthResult{OK: true, Counter: rec.Counter}, nil
}

// Freeze marks a credential as unusable.
func (g *CredentialStore) Freeze(tenantID, credentialID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.credentials[credentialID]
	if !ok || rec.TenantID != tenantID {
		return errors.New("credential not found")
	}
	rec.Frozen = true
	return nil
}

// Discover lists the discoverable credentials of one user in one tenant.
func (g *CredentialStore) Discover(tenantID, userID string) ([]Credential, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	sorted := g.sortedFor(tenantID, userID)
	out := make([]Credential, 0, len(sorted))
	for _, rec := range sorted {
		out = append(out, toCredential(rec))
	}
	return out, nil
}

// DiscoverPage lists one page of a user's credentials after cursor.
func (g *CredentialStore) DiscoverPage(tenantID, userID, cursor string, limit int) ([]Credential, string, error) {
	if limit <= 0 {
		limit = 50
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	sorted := g.sortedFor(tenantID, userID)
	start := 0
	if cursor != "" {
		mark, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		start = sort.Search(len(sorted), func(i int) bool {
			rec := sorted[i]
			if !rec.CreatedAt.Equal(mark.CreatedAt) {
				return rec.CreatedAt.After(mark.CreatedAt)
			}
			return rec.CredentialID > mark.CredentialID
		})
	}
	end := start + limit
	if end > len(sorted) {
		end = len(sorted)
	}
	page := make([]Credential, 0, end-start)
	for _, rec := range sorted[start:end] {
		page = append(page, toCredential(rec))
	}
	next := ""
	if end < len(sorted) && end > start {
		next = encodeCursor(sorted[end-1])
	}
	return page, next, nil
}

// Snapshot serialises the stored public keys and credential records.
func (g *CredentialStore) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := snapshotData{
		Credentials: make([]storedCredential, 0, len(g.credentials)),
		Handles:     make(map[string]map[string]string, len(g.handles)),
	}
	for _, rec := range g.credentials {
		copied := *rec
		copied.PublicKey = append([]byte(nil), rec.PublicKey...)
		snap.Credentials = append(snap.Credentials, copied)
	}
	sort.Slice(snap.Credentials, func(i, j int) bool {
		a, b := snap.Credentials[i], snap.Credentials[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.CredentialID < b.CredentialID
	})
	for tenant, handles := range g.handles {
		bound := make(map[string]string, len(handles))
		for handle, userID := range handles {
			bound[handle] = userID
		}
		snap.Handles[tenant] = bound
	}
	return json.Marshal(snap)
}

// sortedFor returns the credentials of one user of one tenant ordered by
// creation time, ties broken by credential id. Callers must hold the lock.
func (g *CredentialStore) sortedFor(tenantID, userID string) []*storedCredential {
	var out []*storedCredential
	for _, rec := range g.credentials {
		if rec.TenantID == tenantID && rec.UserID == userID {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CredentialID < out[j].CredentialID
	})
	return out
}

// toCredential converts an internal record to the public view.
func toCredential(rec *storedCredential) Credential {
	return Credential{
		TenantID:     rec.TenantID,
		UserID:       rec.UserID,
		Username:     rec.Username,
		CredentialID: rec.CredentialID,
		UserHandle:   rec.UserHandle,
		Origin:       rec.Origin,
		BE:           rec.BE,
		BS:           rec.BS,
		Frozen:       rec.Frozen,
		Counter:      rec.Counter,
	}
}

// expectedRPIDHash returns the lower-case hex SHA-256 of the RP id.
func expectedRPIDHash(rpID string) string {
	sum := sha256.Sum256([]byte(rpID))
	return hex.EncodeToString(sum[:])
}

// pageCursor is the decoded form of a DiscoverPage cursor.
type pageCursor struct {
	CreatedAt    time.Time `json:"ts"`
	CredentialID string    `json:"id"`
}

// encodeCursor encodes the position after rec as an opaque cursor.
func encodeCursor(rec *storedCredential) string {
	raw, err := json.Marshal(pageCursor{CreatedAt: rec.CreatedAt, CredentialID: rec.CredentialID})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses an opaque cursor back into a position marker.
func decodeCursor(cursor string) (pageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return pageCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var mark pageCursor
	if err := json.Unmarshal(raw, &mark); err != nil {
		return pageCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	if mark.CreatedAt.IsZero() || mark.CredentialID == "" {
		return pageCursor{}, errors.New("invalid cursor: missing position")
	}
	return mark, nil
}
