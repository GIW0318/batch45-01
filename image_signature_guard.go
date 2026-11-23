package task

import (
	"errors"
	"time"
)

var errNotImplemented = errors.New("not implemented")

// TrustRoot is one trusted signing key for an issuer.
type TrustRoot struct {
	Issuer    string
	KeyID     string
	PublicKey string
	NotBefore time.Time
	NotAfter  time.Time
}

// Signature is a signature attached to a manifest.
type Signature struct {
	Issuer         string
	KeyID          string
	ManifestDigest string
	Critical       map[string]string
	SignedAt       time.Time
	Bytes          []byte
}

// Manifest is one architecture manifest of an image.
type Manifest struct {
	Digest            string
	Arch              string
	AttestationDigest string
}

// ImageRef identifies an image that is being pulled.
type ImageRef struct {
	Registry string
	Repo     string
	Tag      string
	Digest   string
	Arch     string
}

// Attestation is a build attestation bound to an image digest.
type Attestation struct {
	SubjectDigest string
	PayloadDigest string
}

// CacheEntry is a cached verification result.
type CacheEntry struct {
	Ref       string
	Digest    string
	Verified  bool
	ExpiresAt time.Time
}

// GuardPolicy is the admission policy of the signature guard.
type GuardPolicy struct {
	AllowedIssuers     []string
	RequireAttestation bool
	CacheTTL           time.Duration
}

// ImageSignatureGuard verifies image signatures before admission.
type ImageSignatureGuard struct{}

// NewImageSignatureGuard returns a guard for the given policy.
func NewImageSignatureGuard(policy GuardPolicy) *ImageSignatureGuard {
	return &ImageSignatureGuard{}
}

// AddTrustRoot registers or rotates a trust root.
func (g *ImageSignatureGuard) AddTrustRoot(root TrustRoot) error {
	return errNotImplemented
}

// TrustRoots returns the currently trusted roots.
func (g *ImageSignatureGuard) TrustRoots() []TrustRoot {
	return nil
}

// Verify checks one signature against one manifest.
func (g *ImageSignatureGuard) Verify(ref ImageRef, sig Signature, man Manifest, now time.Time) error {
	return errNotImplemented
}

// VerifyMultiArch verifies every architecture manifest of an image.
func (g *ImageSignatureGuard) VerifyMultiArch(ref ImageRef, mans []Manifest, sigs []Signature, now time.Time) error {
	return errNotImplemented
}

// VerifyAttestation checks that an attestation is bound to the image digest.
func (g *ImageSignatureGuard) VerifyAttestation(imageDigest string, att Attestation) error {
	return errNotImplemented
}

// AdmitRef reports whether the image may be pulled.
func (g *ImageSignatureGuard) AdmitRef(ref ImageRef, sig Signature, man Manifest, att Attestation, now time.Time) bool {
	return false
}

// CachePut stores a verification result.
func (g *ImageSignatureGuard) CachePut(e CacheEntry) error {
	return errNotImplemented
}

// CacheGet returns a cached verification result if it is still valid.
func (g *ImageSignatureGuard) CacheGet(ref, digest string, now time.Time) (CacheEntry, bool) {
	return CacheEntry{}, false
}

// ExportRoots serialises the trust roots.
func (g *ImageSignatureGuard) ExportRoots() ([]byte, error) {
	return nil, errNotImplemented
}

// ImportRoots restores trust roots from a serialised form.
func (g *ImageSignatureGuard) ImportRoots(data []byte) error {
	return errNotImplemented
}
