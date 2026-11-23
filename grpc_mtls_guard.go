package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config holds mTLS guard policy.
type Config struct {
	// TenantDomainSuffix is stripped from SANs such as
	// "alice@tenant-a.example.com" to obtain the tenant id.
	TenantDomainSuffix string
	// MaxClockSkew is the only tolerated deviation for certificate validity.
	MaxClockSkew time.Duration
	// RevocationSource is called by RefreshRevocations. A failure must put the
	// guard into a fail-closed state.
	RevocationSource func() ([]string, error)
}

// CertInfo is the part of a peer certificate the guard evaluates.
type CertInfo struct {
	Serial  string
	Subject string
	// SANs are identities of the form "user@tenant.example.com". All of them
	// must map to the same tenant.
	SANs                 []string
	NotBefore            time.Time
	NotAfter             time.Time
	IntermediatesPresent bool
	ChainComplete        bool
	Fingerprint          string
	// Resumed marks a certificate presented on a resumed session.
	Resumed bool
}

// Identity is the tenant identity derived from a certificate.
type Identity struct {
	TenantID string
	Subject  string
	Serial   string
}

// RevocationStatus is the outcome of an OCSP query.
type RevocationStatus int

const (
	RevocationUnknown RevocationStatus = iota
	RevocationGood
	RevocationRevoked
)

// VettingResult is the authorization decision for one call.
type VettingResult struct {
	Allowed  bool
	Reason   string
	TenantID string
}

var (
	errRevocationUnavailable = errors.New("revocation state unavailable")
	errInvalidRevocationList = errors.New("invalid revocation cache")
)

// revocationState is an immutable snapshot once published. Refreshes replace
// the whole value under the guard lock, so readers never observe a mixture of
// two revocation lists.
type revocationState struct {
	sequence uint64
	revoked  map[string]struct{}
	healthy  bool
}

type revocationCache struct {
	Version  int      `json:"version"`
	Sequence uint64   `json:"sequence"`
	Revoked  []string `json:"revoked"`
	Healthy  bool     `json:"healthy"`
}

// MtlsGuard validates client certificates for gRPC calls.
type MtlsGuard struct {
	cfg Config

	mu sync.RWMutex

	rev     revocationState
	revNext uint64
	initErr error

	bindings map[string]string

	observed   map[string]map[string]struct{}
	duplicates map[string]struct{}
}

// NewMtlsGuard returns a guard configured by cfg.
func NewMtlsGuard(cfg Config) *MtlsGuard {
	return &MtlsGuard{
		cfg: cfg,
		rev: revocationState{
			revoked: make(map[string]struct{}),
		},
		bindings:   make(map[string]string),
		observed:   make(map[string]map[string]struct{}),
		duplicates: make(map[string]struct{}),
	}
}

// NewMtlsGuardFromCache returns a guard sharing the revocation cache exported
// by another cluster node.
func NewMtlsGuardFromCache(cfg Config, cache []byte) *MtlsGuard {
	g := NewMtlsGuard(cfg)
	state, err := decodeRevocationCache(cache)
	if err != nil {
		g.initErr = err
		g.rev = revocationState{revoked: make(map[string]struct{})}
		return g
	}
	g.rev = state
	g.revNext = state.sequence
	return g
}

// LoadRevocationList replaces the revocation list.
func (g *MtlsGuard) LoadRevocationList(serials []string) error {
	revoked := make(map[string]struct{}, len(serials))
	for _, serial := range serials {
		serial = strings.TrimSpace(serial)
		if serial != "" {
			revoked[serial] = struct{}{}
		}
	}

	g.mu.Lock()
	g.revNext++
	sequence := g.revNext
	g.rev = revocationState{
		sequence: sequence,
		revoked:  revoked,
		healthy:  true,
	}
	g.mu.Unlock()
	return nil
}

// Revoke marks one serial as revoked.
func (g *MtlsGuard) Revoke(serial string) error {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return errors.New("empty certificate serial")
	}

	g.mu.Lock()
	revoked := cloneSerialSet(g.rev.revoked)
	revoked[serial] = struct{}{}
	g.revNext++
	g.rev = revocationState{
		sequence: g.revNext,
		revoked:  revoked,
		healthy:  g.rev.healthy,
	}
	g.mu.Unlock()
	return nil
}

// IsRevoked reports whether serial is revoked.
func (g *MtlsGuard) IsRevoked(serial string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.rev.healthy {
		return false, errRevocationUnavailable
	}
	_, revoked := g.rev.revoked[strings.TrimSpace(serial)]
	return revoked, nil
}

// CheckOCSP evaluates an OCSP result. A timeout must not be treated as good.
func (g *MtlsGuard) CheckOCSP(serial, response string, timedOut bool, now time.Time) (RevocationStatus, error) {
	if timedOut {
		return RevocationUnknown, nil
	}
	if strings.TrimSpace(serial) == "" {
		return RevocationUnknown, nil
	}

	status := parseOCSPStatus(response)
	if status == RevocationRevoked {
		return RevocationRevoked, nil
	}

	g.mu.RLock()
	healthy := g.rev.healthy
	_, locallyRevoked := g.rev.revoked[strings.TrimSpace(serial)]
	g.mu.RUnlock()
	if healthy && locallyRevoked {
		return RevocationRevoked, nil
	}
	if status == RevocationGood {
		return RevocationGood, nil
	}
	return RevocationUnknown, nil
}

// Authorize decides whether a call from cert may proceed. It must check the
// chain, the intermediates, expiry, revocation, the SAN to tenant mapping and
// re-validate certificates presented on resumed sessions. Client identity
// headers injected by the gateway must not be trusted.
func (g *MtlsGuard) Authorize(cert CertInfo, headers map[string]string, now time.Time) (VettingResult, error) {
	_ = headers // Identity always comes from the peer certificate.

	serial := strings.TrimSpace(cert.Serial)
	if serial == "" {
		return denied("certificate serial is missing"), nil
	}
	// Record every presented certificate so serial reuse can be reported even
	// when the caller does not use ObserveCert explicitly.
	_ = g.ObserveCert(cert)

	if !cert.ChainComplete {
		return denied("certificate chain is incomplete"), nil
	}
	if !cert.IntermediatesPresent {
		return denied("intermediate certificates are missing"), nil
	}
	if reason := g.validateValidity(cert, now); reason != "" {
		return denied(reason), nil
	}

	g.mu.RLock()
	healthy := g.rev.healthy
	_, revoked := g.rev.revoked[serial]
	boundTenant, hasBinding := g.bindings[serial]
	g.mu.RUnlock()

	if !healthy {
		return denied("revocation state is unavailable"), nil
	}
	if revoked {
		return denied("certificate is revoked"), nil
	}

	tenantID, err := tenantFromSANs(cert.SANs, g.cfg.TenantDomainSuffix)
	if err != nil {
		return denied(err.Error()), nil
	}
	if hasBinding && boundTenant != tenantID {
		return denied("certificate is bound to a different tenant"), nil
	}

	bound, ok := g.bindIfAbsent(serial, tenantID)
	if !ok || bound != tenantID {
		return denied("certificate is bound to a different tenant"), nil
	}

	return VettingResult{
		Allowed:  true,
		TenantID: tenantID,
	}, nil
}

// BindTenant maps a certificate to a tenant. A certificate may be bound to one
// tenant only.
func (g *MtlsGuard) BindTenant(cert CertInfo, tenantID string) (string, error) {
	serial := strings.TrimSpace(cert.Serial)
	tenantID = strings.TrimSpace(tenantID)
	if serial == "" {
		return "", errors.New("empty certificate serial")
	}
	if tenantID == "" {
		return "", errors.New("empty tenant id")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if existing, ok := g.bindings[serial]; ok {
		if existing != tenantID {
			return "", errors.New("certificate is already bound to another tenant")
		}
		return existing, nil
	}
	g.bindings[serial] = tenantID
	return tenantID, nil
}

// TenantFor returns the tenant bound to serial, or "".
func (g *MtlsGuard) TenantFor(serial string) (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.bindings[strings.TrimSpace(serial)], nil
}

// ReuseConnection re-validates the certificate of an existing gRPC connection
// before it is reused.
func (g *MtlsGuard) ReuseConnection(connID string, cert CertInfo, now time.Time) (bool, error) {
	_ = connID
	res, err := g.Authorize(cert, nil, now)
	if err != nil {
		return false, err
	}
	return res.Allowed, nil
}

// RefreshRevocations pulls a new revocation list from RevocationSource.
func (g *MtlsGuard) RefreshRevocations() error {
	g.mu.Lock()
	g.revNext++
	sequence := g.revNext
	source := g.cfg.RevocationSource
	g.mu.Unlock()

	if source == nil {
		return g.failRefresh(sequence, errors.New("revocation source is not configured"))
	}

	serials, err := source()
	if err != nil {
		return g.failRefresh(sequence, err)
	}

	revoked := make(map[string]struct{}, len(serials))
	for _, serial := range serials {
		serial = strings.TrimSpace(serial)
		if serial != "" {
			revoked[serial] = struct{}{}
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	// Each refresh owns an issue sequence. A response that started before a
	// newer published state is stale and must be discarded, even if it arrives
	// later. This prevents an out-of-order response from resurrecting a
	// revocation.
	if sequence <= g.rev.sequence {
		return nil
	}
	g.rev = revocationState{
		sequence: sequence,
		revoked:  revoked,
		healthy:  true,
	}
	return nil
}

func (g *MtlsGuard) failRefresh(sequence uint64, cause error) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// A failure from an older refresh must not invalidate a newer, already
	// published state. A current failure fails closed and removes the old table
	// from service immediately.
	if sequence <= g.rev.sequence {
		return cause
	}
	g.rev = revocationState{
		sequence: sequence,
		revoked:  make(map[string]struct{}),
		healthy:  false,
	}
	return cause
}

// ObserveCert records a presented certificate so that duplicate serial numbers
// can be detected.
func (g *MtlsGuard) ObserveCert(cert CertInfo) error {
	serial := strings.TrimSpace(cert.Serial)
	if serial == "" {
		return errors.New("empty certificate serial")
	}
	marker := certificateMarker(cert)

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.observed == nil {
		g.observed = make(map[string]map[string]struct{})
	}
	if g.duplicates == nil {
		g.duplicates = make(map[string]struct{})
	}
	markers := g.observed[serial]
	if markers == nil {
		markers = make(map[string]struct{})
		g.observed[serial] = markers
	}
	for seen := range markers {
		if seen != marker {
			g.duplicates[serial] = struct{}{}
			break
		}
	}
	markers[marker] = struct{}{}
	return nil
}

// DuplicateSerials returns serial numbers observed on more than one
// certificate.
func (g *MtlsGuard) DuplicateSerials() ([]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	serials := make([]string, 0, len(g.duplicates))
	for serial := range g.duplicates {
		serials = append(serials, serial)
	}
	sort.Strings(serials)
	return serials, nil
}

// ExportRevocationCache serialises the revocation cache for cluster peers.
func (g *MtlsGuard) ExportRevocationCache() ([]byte, error) {
	g.mu.RLock()
	if !g.rev.healthy {
		g.mu.RUnlock()
		return nil, errRevocationUnavailable
	}
	sequence := g.rev.sequence
	revoked := make([]string, 0, len(g.rev.revoked))
	for serial := range g.rev.revoked {
		revoked = append(revoked, serial)
	}
	g.mu.RUnlock()

	sort.Strings(revoked)
	return json.Marshal(revocationCache{
		Version:  1,
		Sequence: sequence,
		Revoked:  revoked,
		Healthy:  true,
	})
}

func (g *MtlsGuard) validateValidity(cert CertInfo, now time.Time) string {
	if cert.NotAfter.IsZero() || now.After(cert.NotAfter) {
		return "certificate is expired"
	}
	skew := g.cfg.MaxClockSkew
	if skew < 0 {
		skew = 0
	}
	if !cert.NotBefore.IsZero() && now.Before(cert.NotBefore.Add(-skew)) {
		return "certificate is not yet valid"
	}
	return ""
}

func (g *MtlsGuard) bindIfAbsent(serial, tenantID string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if existing, ok := g.bindings[serial]; ok {
		return existing, existing == tenantID
	}
	g.bindings[serial] = tenantID
	return tenantID, true
}

func denied(reason string) VettingResult {
	return VettingResult{Allowed: false, Reason: reason}
}

func cloneSerialSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in)+1)
	for serial := range in {
		out[serial] = struct{}{}
	}
	return out
}

func certificateMarker(cert CertInfo) string {
	if marker := strings.TrimSpace(cert.Fingerprint); marker != "" {
		return "fingerprint:" + marker
	}
	sans := append([]string(nil), cert.SANs...)
	sort.Strings(sans)
	parts := []string{
		"subject:" + cert.Subject,
		"not-before:" + cert.NotBefore.UTC().Format(time.RFC3339Nano),
		"not-after:" + cert.NotAfter.UTC().Format(time.RFC3339Nano),
	}
	parts = append(parts, sans...)
	return strings.Join(parts, "\x00")
}

func parseOCSPStatus(response string) RevocationStatus {
	normalized := strings.ToLower(strings.TrimSpace(response))
	switch normalized {
	case "good", "0", "ok", "valid", "revocationgood":
		return RevocationGood
	case "revoked", "1", "revocationrevoked":
		return RevocationRevoked
	case "", "unknown", "2", "revocationunknown":
		return RevocationUnknown
	}
	switch {
	case strings.Contains(normalized, "revoked"):
		return RevocationRevoked
	case strings.Contains(normalized, "unknown"):
		return RevocationUnknown
	case strings.Contains(normalized, "good"):
		return RevocationGood
	default:
		return RevocationUnknown
	}
}

func tenantFromSANs(sans []string, domainSuffix string) (string, error) {
	if len(sans) == 0 {
		return "", errors.New("certificate has no SAN")
	}

	suffix := strings.ToLower(strings.TrimSpace(domainSuffix))
	var tenantID string
	for _, san := range sans {
		san = strings.TrimSpace(san)
		at := strings.LastIndex(san, "@")
		if at <= 0 || at == len(san)-1 {
			return "", errors.New("certificate SAN has no tenant identity")
		}
		user := san[:at]
		domain := strings.ToLower(strings.TrimSpace(san[at+1:]))
		if strings.Contains(user, "@") || domain == "" || strings.ContainsAny(domain, " \t\r\n/") {
			return "", errors.New("certificate SAN is malformed")
		}

		tenant := domain
		if suffix != "" {
			if !strings.HasSuffix(domain, suffix) {
				return "", errors.New("certificate SAN is outside the tenant domain")
			}
			prefix := strings.TrimSuffix(domain, suffix)
			if prefix == "" {
				return "", errors.New("certificate SAN has an empty tenant")
			}
			if !strings.HasPrefix(suffix, ".") && !strings.HasSuffix(prefix, ".") {
				return "", errors.New("certificate SAN is outside the tenant domain")
			}
			tenant = strings.TrimSuffix(prefix, ".")
		}
		if tenant == "" {
			return "", errors.New("certificate SAN has an empty tenant")
		}
		if tenantID == "" {
			tenantID = tenant
			continue
		}
		if tenantID != tenant {
			return "", errors.New("certificate SANs map to different tenants")
		}
	}
	return tenantID, nil
}

func decodeRevocationCache(cache []byte) (revocationState, error) {
	trimmed := bytes.TrimSpace(cache)
	if len(trimmed) == 0 {
		return revocationState{}, errInvalidRevocationList
	}

	var serials []string
	var sequence uint64
	switch trimmed[0] {
	case '{':
		var exported revocationCache
		if err := json.Unmarshal(trimmed, &exported); err != nil {
			return revocationState{}, errInvalidRevocationList
		}
		if exported.Version != 0 && exported.Version != 1 {
			return revocationState{}, errInvalidRevocationList
		}
		if !exported.Healthy {
			return revocationState{
				sequence: exported.Sequence,
				revoked:  make(map[string]struct{}),
				healthy:  false,
			}, nil
		}
		serials = exported.Revoked
		sequence = exported.Sequence
	case '[':
		if err := json.Unmarshal(trimmed, &serials); err != nil {
			return revocationState{}, errInvalidRevocationList
		}
	default:
		return revocationState{}, errInvalidRevocationList
	}

	revoked := make(map[string]struct{}, len(serials))
	for _, serial := range serials {
		serial = strings.TrimSpace(serial)
		if serial != "" {
			revoked[serial] = struct{}{}
		}
	}

	return revocationState{
		sequence: sequence,
		revoked:  revoked,
		healthy:  true,
	}, nil
}
