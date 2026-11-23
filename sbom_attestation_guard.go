package task

import (
	"errors"
	"time"
)

var errNotImplemented = errors.New("not implemented")

// Component is one software component of an SBOM.
type Component struct {
	Name    string
	Version string
}

// Dependency is a directed edge: From depends on To.
type Dependency struct {
	From string
	To   string
}

// SBOM is a bill of materials for one subject digest.
type SBOM struct {
	Format        string
	SubjectDigest string
	Components    []Component
	Deps          []Dependency
	CreatedAt     time.Time
}

// Attestation is a signed statement about a build artifact.
type Attestation struct {
	ID            string
	Tenant        string
	SubjectDigest string
	PayloadDigest string
	SBOM          SBOM
	Materials     []string
	BuildParams   map[string]string
	SignedAt      time.Time
	ExpiresAt     time.Time
	Signature     []byte
	Revoked       bool
}

// AuditQuery selects attestations for audit.
type AuditQuery struct {
	Tenant         string
	SubjectDigest  string
	IncludeRevoked bool
}

// SbomGuard stores and validates SBOM attestations.
type SbomGuard struct{}

// NewSbomGuard returns an empty guard.
func NewSbomGuard() *SbomGuard {
	return &SbomGuard{}
}

// Submit validates and stores an attestation.
func (g *SbomGuard) Submit(att Attestation, now time.Time) error {
	return errNotImplemented
}

// Get returns the stored attestation of a tenant for a digest.
func (g *SbomGuard) Get(tenant, digest string) (*Attestation, error) {
	return nil, errNotImplemented
}

// Audit lists attestations matching the query.
func (g *SbomGuard) Audit(q AuditQuery) ([]Attestation, error) {
	return nil, errNotImplemented
}

// Merge combines two attestations of the same subject digest.
func (g *SbomGuard) Merge(a, b Attestation) (Attestation, error) {
	return Attestation{}, errNotImplemented
}

// ValidateSBOM validates components, versions and formats.
func (g *SbomGuard) ValidateSBOM(s SBOM) error {
	return errNotImplemented
}

// DependencyOrder returns the components in dependency order.
func (g *SbomGuard) DependencyOrder(s SBOM) ([]string, error) {
	return nil, errNotImplemented
}

// RedactParams removes secrets from build parameters.
func (g *SbomGuard) RedactParams(p map[string]string) map[string]string {
	return nil
}

// Revoke marks an attestation as revoked.
func (g *SbomGuard) Revoke(id string) error {
	return errNotImplemented
}

// SBOMDigest returns the canonical digest of an SBOM payload.
func (g *SbomGuard) SBOMDigest(s SBOM) string {
	return ""
}
