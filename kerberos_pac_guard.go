package task

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	// PAC parsing is bounded so a hostile PAC cannot make the gateway allocate
	// an unbounded amount of memory or recurse without limit.
	maxPACBytes    = 64 << 10
	maxPACDepth    = 16
	maxPACGroups   = 4096
	maxReferralHop = 16
	maxSnapshotLen = 8 << 20

	// Replay entries are deliberately retained for the lifetime of this guard
	// instance. An authenticator nonce is single-use; snapshotting this set is
	// what makes a restart unable to resurrect a nonce.
	snapshotVersion = 1
)

// Config holds Kerberos PAC validation policy.
type Config struct {
	MaxSkew   time.Duration
	PACMaxAge time.Duration
	// AllowedEnctypes lists the ticket encryption types that may be accepted.
	AllowedEnctypes []string
}

// PAC is the privilege attribute certificate carried in a ticket.
type PAC struct {
	LogonServer string
	LogonDomain string
	// UserSID and GroupSIDs are domain qualified, e.g.
	// "domain-a/S-1-5-21-1-2-3-1001".
	UserSID         string
	GroupSIDs       []string
	KDCChecksum     string
	ServiceChecksum string
	IssuedAt        time.Time
	ExpiresAt       time.Time
	// DelegationAllowed is the PAC delegates flag.
	DelegationAllowed bool
}

// Ticket is a service ticket.
type Ticket struct {
	ID      string
	SPN     string
	Enctype string
	PAC     PAC
	// ReferralPath is the list of domains the referral ticket walked through.
	ReferralPath []string
}

// Authenticator is the client authenticator that accompanies a ticket.
type Authenticator struct {
	ClientPrincipal string
	Nonce           string
	Timestamp       time.Time
}

// ServicePrincipal is the service the ticket is presented to.
type ServicePrincipal struct {
	Name                 string
	TrustedForDelegation bool
}

// ValidationResult is the outcome of PAC validation.
type ValidationResult struct {
	OK        bool
	Reason    string
	Delegated bool
}

// PacGuard validates Kerberos PACs at the service gateway.
type PacGuard struct {
	mu      sync.Mutex
	cfg     Config
	trusted map[string]struct{}
	replay  map[string]struct{}
	results map[string]ValidationResult
}

// NewPacGuard returns a guard configured by cfg.
func NewPacGuard(cfg Config) *PacGuard {
	if cfg.MaxSkew < 0 {
		cfg.MaxSkew = 0
	}
	cfg.AllowedEnctypes = append([]string(nil), cfg.AllowedEnctypes...)
	return &PacGuard{
		cfg:     cfg,
		trusted: make(map[string]struct{}),
		replay:  make(map[string]struct{}),
		results: make(map[string]ValidationResult),
	}
}

// NewPacGuardFromSnapshot restores the replay cache from a snapshot.
func NewPacGuardFromSnapshot(cfg Config, data []byte) *PacGuard {
	g := NewPacGuard(cfg)
	if len(data) == 0 || len(data) > maxSnapshotLen {
		return g
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return g
	}
	for _, nonce := range state.Replay {
		if nonce != "" {
			g.replay[nonce] = struct{}{}
		}
	}
	for id, result := range state.Results {
		g.results[id] = result
	}
	return g
}

// TrustDomain declares a domain that group SIDs and referral paths may
// reference.
func (g *PacGuard) TrustDomain(domain string) error {
	normalized, err := normalizeDomain(domain)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureStateLocked()
	g.trusted[normalized] = struct{}{}
	return nil
}

// Validate checks a ticket and returns the authorization decision.
//
// Both the KDC and the service checksum must be present, the enctype must be
// strong, the PAC must be fresh, every SID domain and referral hop must be
// trusted, the logon information must be complete, the SPN must match the
// service case-insensitively, delegation must only be granted to trusted
// services, the authenticator must be fresh and unused, and the ticket must
// match the authenticator nonce.
func (g *PacGuard) Validate(t Ticket, a Authenticator, svc ServicePrincipal, now time.Time) (ValidationResult, error) {
	// The replay decision and the result-cache update must be atomic with
	// respect to other validations, otherwise two concurrent requests can both
	// observe an unused nonce or overwrite each other's cache entry.
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureStateLocked()

	result := g.validateLocked(t, a, svc, now)
	if t.ID != "" {
		g.results[t.ID] = result
	}
	return result, nil
}

func (g *PacGuard) validateLocked(t Ticket, a Authenticator, svc ServicePrincipal, now time.Time) ValidationResult {
	reject := func(reason string) ValidationResult {
		if t.ID != "" {
			reason = "ticket " + t.ID + ": " + reason
		}
		return ValidationResult{OK: false, Reason: reason}
	}

	if !g.enctypeAllowed(t.Enctype) {
		return reject("ticket enctype is missing, weak, or not allowed")
	}

	ticketSPN, err := g.NormalizeSPN(t.SPN)
	if err != nil {
		return reject("ticket SPN is invalid")
	}
	serviceSPN, err := g.NormalizeSPN(svc.Name)
	if err != nil {
		return reject("service SPN is invalid")
	}
	if ticketSPN != serviceSPN {
		return reject("ticket SPN does not match the service")
	}

	if strings.TrimSpace(t.PAC.LogonServer) == "" {
		return reject("PAC logon server is missing")
	}
	if strings.TrimSpace(t.PAC.LogonDomain) == "" {
		return reject("PAC logon domain is missing")
	}
	logonDomain, err := normalizeDomain(t.PAC.LogonDomain)
	if err != nil || !g.domainTrusted(logonDomain) {
		return reject("PAC logon domain is not trusted")
	}

	if strings.TrimSpace(t.PAC.UserSID) == "" {
		return reject("PAC user SID is missing")
	}
	if !g.sidTrusted(t.PAC.UserSID) {
		return reject("PAC user SID domain is not trusted")
	}
	if len(t.PAC.GroupSIDs) > maxPACGroups {
		return reject("PAC contains too many group SIDs")
	}
	for _, sid := range t.PAC.GroupSIDs {
		if !g.sidTrusted(sid) {
			return reject("PAC group SID domain is not trusted")
		}
	}

	if strings.TrimSpace(t.PAC.KDCChecksum) == "" {
		return reject("PAC KDC checksum is missing")
	}
	if strings.TrimSpace(t.PAC.ServiceChecksum) == "" {
		return reject("PAC service checksum is missing")
	}

	if t.PAC.IssuedAt.IsZero() {
		return reject("PAC issue time is missing")
	}
	if t.PAC.ExpiresAt.IsZero() {
		return reject("PAC expiry time is missing")
	}
	if !t.PAC.ExpiresAt.After(t.PAC.IssuedAt) {
		return reject("PAC expiry is not after its issue time")
	}
	if !now.Before(t.PAC.ExpiresAt) {
		return reject("PAC has expired")
	}
	if now.Before(t.PAC.IssuedAt.Add(-g.cfg.MaxSkew)) {
		return reject("PAC issue time is in the future")
	}
	if g.cfg.PACMaxAge > 0 && now.Sub(t.PAC.IssuedAt) > g.cfg.PACMaxAge {
		return reject("PAC is older than the allowed maximum age")
	}

	if len(t.ReferralPath) > maxReferralHop {
		return reject("referral path exceeds the maximum number of hops")
	}
	for _, hop := range t.ReferralPath {
		domain, err := normalizeDomain(hop)
		if err != nil || !g.domainTrusted(domain) {
			return reject("referral path contains an untrusted domain")
		}
	}

	if t.PAC.DelegationAllowed && !svc.TrustedForDelegation {
		return reject("delegation requested by an untrusted service")
	}

	if strings.TrimSpace(a.Nonce) == "" {
		return reject("authenticator nonce is missing")
	}
	if a.Timestamp.IsZero() {
		return reject("authenticator timestamp is missing")
	}
	skew := now.Sub(a.Timestamp)
	if skew < 0 {
		skew = -skew
	}
	if skew > g.cfg.MaxSkew {
		return reject("authenticator timestamp is outside the allowed clock skew")
	}

	// Replay is keyed by authenticator nonce, never by ticket id. A ticket may
	// legitimately be presented with a fresh authenticator.
	if _, used := g.replay[a.Nonce]; used {
		return reject("authenticator nonce has already been used")
	}
	g.replay[a.Nonce] = struct{}{}

	return ValidationResult{
		OK:        true,
		Delegated: t.PAC.DelegationAllowed && svc.TrustedForDelegation,
	}
}

// NormalizeSPN returns the canonical form of a service principal name.
func (g *PacGuard) NormalizeSPN(spn string) (string, error) {
	normalized := strings.TrimSpace(spn)
	if normalized == "" {
		return "", fmt.Errorf("spn is empty")
	}
	if strings.IndexFunc(normalized, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", fmt.Errorf("spn contains whitespace or control characters")
	}
	if strings.Count(normalized, "/") != 1 {
		return "", fmt.Errorf("spn must contain exactly one service separator")
	}
	parts := strings.SplitN(normalized, "/", 2)
	if parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("spn service and host must be non-empty")
	}
	return strings.ToLower(normalized), nil
}

// CachedResult returns the cached validation result for a ticket.
func (g *PacGuard) CachedResult(ticketID string) (ValidationResult, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureStateLocked()
	result, ok := g.results[ticketID]
	return result, ok, nil
}

type snapshotState struct {
	Version int                         `json:"version"`
	Replay  []string                    `json:"replay"`
	Results map[string]ValidationResult `json:"results,omitempty"`
}

// Snapshot serialises the replay cache.
func (g *PacGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureStateLocked()

	nonces := make([]string, 0, len(g.replay))
	for nonce := range g.replay {
		nonces = append(nonces, nonce)
	}
	sort.Strings(nonces)

	results := make(map[string]ValidationResult, len(g.results))
	for id, result := range g.results {
		results[id] = result
	}

	return json.Marshal(snapshotState{
		Version: snapshotVersion,
		Replay:  nonces,
		Results: results,
	})
}

// ParsePAC parses one JSON-encoded PAC. Input size and JSON nesting depth are
// bounded before decoding; empty, oversized, over-nested, or truncated input
// is always an error and is never turned into an empty PAC.
func ParsePAC(data []byte) (PAC, error) {
	if len(data) == 0 {
		return PAC{}, fmt.Errorf("pac: empty input")
	}
	if len(data) > maxPACBytes {
		return PAC{}, fmt.Errorf("pac: input size %d exceeds limit %d", len(data), maxPACBytes)
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return PAC{}, fmt.Errorf("pac: empty input")
	}
	if trimmed[0] != '{' {
		return PAC{}, fmt.Errorf("pac: input is not a JSON object")
	}
	if err := checkJSONDepth(trimmed, maxPACDepth); err != nil {
		return PAC{}, err
	}

	var pac PAC
	if err := json.Unmarshal(trimmed, &pac); err != nil {
		return PAC{}, fmt.Errorf("pac: malformed input: %w", err)
	}
	if len(pac.GroupSIDs) > maxPACGroups {
		return PAC{}, fmt.Errorf("pac: %d group SIDs exceeds limit %d", len(pac.GroupSIDs), maxPACGroups)
	}
	if pac.LogonServer == "" && pac.LogonDomain == "" && pac.UserSID == "" &&
		len(pac.GroupSIDs) == 0 && pac.KDCChecksum == "" && pac.ServiceChecksum == "" &&
		pac.IssuedAt.IsZero() && pac.ExpiresAt.IsZero() && !pac.DelegationAllowed {
		return PAC{}, fmt.Errorf("pac: empty PAC")
	}
	return pac, nil
}

// ParsePAC parses a PAC using the package-level parser.
func (g *PacGuard) ParsePAC(data []byte) (PAC, error) {
	return ParsePAC(data)
}

func checkJSONDepth(data []byte, limit int) error {
	depth := 0
	inString := false
	escaped := false
	for _, b := range data {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > limit {
				return fmt.Errorf("pac: JSON nesting depth exceeds limit %d", limit)
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return fmt.Errorf("pac: malformed JSON nesting")
			}
		}
	}
	if inString {
		return fmt.Errorf("pac: truncated JSON string")
	}
	if depth != 0 {
		return fmt.Errorf("pac: truncated JSON structure")
	}
	return nil
}

func (g *PacGuard) enctypeAllowed(enctype string) bool {
	enctype = strings.ToLower(strings.TrimSpace(enctype))
	if enctype == "" {
		return false
	}
	for _, weak := range []string{"rc4", "arcfour", "des", "3des"} {
		if strings.HasPrefix(enctype, weak) {
			return false
		}
	}
	if len(g.cfg.AllowedEnctypes) == 0 {
		return strings.HasPrefix(enctype, "aes128-") || strings.HasPrefix(enctype, "aes256-")
	}
	for _, allowed := range g.cfg.AllowedEnctypes {
		if strings.EqualFold(strings.TrimSpace(allowed), enctype) {
			return true
		}
	}
	return false
}

func (g *PacGuard) sidTrusted(sid string) bool {
	domain, _, err := splitDomainSID(sid)
	if err != nil {
		return false
	}
	return g.domainTrusted(domain)
}

func (g *PacGuard) domainTrusted(domain string) bool {
	_, ok := g.trusted[domain]
	return ok
}

func (g *PacGuard) ensureStateLocked() {
	if g.trusted == nil {
		g.trusted = make(map[string]struct{})
	}
	if g.replay == nil {
		g.replay = make(map[string]struct{})
	}
	if g.results == nil {
		g.results = make(map[string]ValidationResult)
	}
}

func normalizeDomain(domain string) (string, error) {
	normalized := strings.TrimSpace(domain)
	normalized = strings.TrimSuffix(normalized, ".")
	if normalized == "" {
		return "", fmt.Errorf("domain is empty")
	}
	if strings.ContainsAny(normalized, `/\@`) {
		return "", fmt.Errorf("domain contains an invalid separator")
	}
	if strings.IndexFunc(normalized, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", fmt.Errorf("domain contains whitespace or control characters")
	}
	return strings.ToLower(normalized), nil
}

func splitDomainSID(value string) (string, string, error) {
	qualified := strings.TrimSpace(value)
	separator := strings.LastIndexAny(qualified, `/\`)
	if separator <= 0 || separator == len(qualified)-1 {
		return "", "", fmt.Errorf("sid is not domain qualified")
	}
	domain, err := normalizeDomain(qualified[:separator])
	if err != nil {
		return "", "", err
	}
	sid := qualified[separator+1:]
	if !validSID(sid) {
		return "", "", fmt.Errorf("invalid SID")
	}
	return domain, sid, nil
}

func validSID(sid string) bool {
	if len(sid) < 3 || (sid[0] != 'S' && sid[0] != 's') || sid[1] != '-' {
		return false
	}
	parts := strings.Split(sid[2:], "-")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
