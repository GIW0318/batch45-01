package task

import (
	"errors"
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

// MtlsGuard validates client certificates for gRPC calls.
type MtlsGuard struct {
	cfg Config
}

// NewMtlsGuard returns a guard configured by cfg.
func NewMtlsGuard(cfg Config) *MtlsGuard {
	return &MtlsGuard{cfg: cfg}
}

// NewMtlsGuardFromCache returns a guard sharing the revocation cache exported
// by another cluster node.
func NewMtlsGuardFromCache(cfg Config, cache []byte) *MtlsGuard {
	return &MtlsGuard{cfg: cfg}
}

// LoadRevocationList replaces the revocation list.
func (g *MtlsGuard) LoadRevocationList(serials []string) error {
	return errors.New("not implemented")
}

// Revoke marks one serial as revoked.
func (g *MtlsGuard) Revoke(serial string) error {
	return errors.New("not implemented")
}

// IsRevoked reports whether serial is revoked.
func (g *MtlsGuard) IsRevoked(serial string) (bool, error) {
	return false, errors.New("not implemented")
}

// CheckOCSP evaluates an OCSP result. A timeout must not be treated as good.
func (g *MtlsGuard) CheckOCSP(serial, response string, timedOut bool, now time.Time) (RevocationStatus, error) {
	return RevocationUnknown, errors.New("not implemented")
}

// Authorize decides whether a call from cert may proceed. It must check the
// chain, the intermediates, expiry, revocation, the SAN to tenant mapping and
// re-validate certificates presented on resumed sessions. Client identity
// headers injected by the gateway must not be trusted.
func (g *MtlsGuard) Authorize(cert CertInfo, headers map[string]string, now time.Time) (VettingResult, error) {
	return VettingResult{}, errors.New("not implemented")
}

// BindTenant maps a certificate to a tenant. A certificate may be bound to one
// tenant only.
func (g *MtlsGuard) BindTenant(cert CertInfo, tenantID string) (string, error) {
	return "", errors.New("not implemented")
}

// TenantFor returns the tenant bound to serial, or "".
func (g *MtlsGuard) TenantFor(serial string) (string, error) {
	return "", errors.New("not implemented")
}

// ReuseConnection re-validates the certificate of an existing gRPC connection
// before it is reused.
func (g *MtlsGuard) ReuseConnection(connID string, cert CertInfo, now time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

// RefreshRevocations pulls a new revocation list from RevocationSource.
func (g *MtlsGuard) RefreshRevocations() error {
	return errors.New("not implemented")
}

// ObserveCert records a presented certificate so that duplicate serial numbers
// can be detected.
func (g *MtlsGuard) ObserveCert(cert CertInfo) error {
	return errors.New("not implemented")
}

// DuplicateSerials returns serial numbers observed on more than one
// certificate.
func (g *MtlsGuard) DuplicateSerials() ([]string, error) {
	return nil, errors.New("not implemented")
}

// ExportRevocationCache serialises the revocation cache for cluster peers.
func (g *MtlsGuard) ExportRevocationCache() ([]byte, error) {
	return nil, errors.New("not implemented")
}
