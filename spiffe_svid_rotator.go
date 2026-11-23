package task

import (
	"errors"
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

// SvidRotator manages SVID rotation and bundle distribution for one sidecar.
type SvidRotator struct {
	cfg Config
}

// NewSvidRotator returns a rotator configured by cfg.
func NewSvidRotator(cfg Config) *SvidRotator {
	return &SvidRotator{cfg: cfg}
}

// LoadSVID installs an SVID from disk. Expired SVIDs and SVIDs of a foreign
// trust domain must be rejected.
func (g *SvidRotator) LoadSVID(s SVID) error {
	return errors.New("not implemented")
}

// ActiveSVID returns the SVID currently used for new connections.
func (g *SvidRotator) ActiveSVID() (SVID, error) {
	return SVID{}, errors.New("not implemented")
}

// SVIDs returns every SVID still usable by the sidecar.
func (g *SvidRotator) SVIDs() ([]SVID, error) {
	return nil, errors.New("not implemented")
}

// BeginRotation starts rotating to next and returns a rotation id. The old
// SVID must stay available until the new one is committed. Only one rotation
// may be in progress at a time.
func (g *SvidRotator) BeginRotation(next SVID) (uint32, error) {
	return 0, errors.New("not implemented")
}

// CommitRotation activates the SVID of a rotation and stops using the old one
// for new connections.
func (g *SvidRotator) CommitRotation(id uint32) error {
	return errors.New("not implemented")
}

// RollbackRotation aborts a rotation and keeps the old SVID.
func (g *SvidRotator) RollbackRotation(id uint32) error {
	return errors.New("not implemented")
}

// UpdateBundle atomically replaces the trust bundle; readers must never see a
// mixture of old and new roots.
func (g *SvidRotator) UpdateBundle(b Bundle) error {
	return errors.New("not implemented")
}

// RefreshBundle merges a (possibly partial) bundle received from the bundle
// endpoint and is rate limited by BundleRefreshInterval.
func (g *SvidRotator) RefreshBundle(b Bundle, now time.Time) error {
	return errors.New("not implemented")
}

// BundleSnapshot returns the current trust bundle.
func (g *SvidRotator) BundleSnapshot() (Bundle, error) {
	return Bundle{}, errors.New("not implemented")
}

// VerifyPeerChain validates a peer certificate chain for a SPIFFE ID.
func (g *SvidRotator) VerifyPeerChain(spiffeID string, chain []string) (bool, error) {
	return false, errors.New("not implemented")
}

// IssueToken mints an authorization token bound to the given SVID.
func (g *SvidRotator) IssueToken(svidID string, now time.Time) (string, error) {
	return "", errors.New("not implemented")
}

// RevokeTokensForSVID revokes every token issued under svidID.
func (g *SvidRotator) RevokeTokensForSVID(svidID string) error {
	return errors.New("not implemented")
}

// TokenValid reports whether tok is still accepted.
func (g *SvidRotator) TokenValid(tok string, now time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

// NeedsRenewal reports whether the active SVID must be renewed before it
// expires.
func (g *SvidRotator) NeedsRenewal(now time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

// Restore loads persisted sidecar state. The rotator must only report Ready
// after the SVIDs are loaded.
func (g *SvidRotator) Restore(state []byte) error {
	return errors.New("not implemented")
}

// ExportState serialises the sidecar state for Restore.
func (g *SvidRotator) ExportState() ([]byte, error) {
	return nil, errors.New("not implemented")
}

// Ready reports whether the sidecar may start listening.
func (g *SvidRotator) Ready() (bool, error) {
	return false, errors.New("not implemented")
}

// ReplicaFingerprint identifies the identity this replica would present, so
// that load-balanced replicas stay consistent.
func (g *SvidRotator) ReplicaFingerprint() (string, error) {
	return "", errors.New("not implemented")
}
