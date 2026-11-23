package task

import (
	"errors"
	"time"
)

// Sentinel errors of the chain builder.
var (
	ErrNoTrustAnchor             = errors.New("no trusted path to a trust anchor")
	ErrChainTooDeep              = errors.New("chain exceeds max depth")
	ErrExpired                   = errors.New("certificate is expired")
	ErrPathLenViolation          = errors.New("path length constraint violated")
	ErrNameConstraintViolation   = errors.New("name constraint violated")
	ErrPolicyViolation           = errors.New("policy constraint violated")
	ErrRevoked                   = errors.New("certificate is revoked")
	ErrAnchorFingerprintMismatch = errors.New("trust anchor fingerprint mismatch")
	ErrMissingIntermediate       = errors.New("intermediate certificate missing")
)

// Cert is the subset of a certificate the builder reasons about.
type Cert struct {
	Subject          string
	Issuer           string
	Serial           string
	Fingerprint      string
	SubjectKeyID     string
	AuthorityKeyID   string
	IsCA             bool
	SelfSigned       bool
	PathLen          int
	NotBefore        time.Time
	NotAfter         time.Time
	SANs             []string
	Policies         []string
	RequiredPolicies []string
	PermittedDNS     []string
	Revoked          bool
	SignatureValid   bool
}

// BuilderOptions configures one chain build.
type BuilderOptions struct {
	MaxDepth      int
	AllowAIA      bool
	CurrentTime   time.Time
	PinnedAnchors []string
}

// ChainBuilder builds certificate paths to a pinned trust anchor.
type ChainBuilder struct {
}

// NewChainBuilder creates a builder with the given policy.
func NewChainBuilder(opts BuilderOptions) *ChainBuilder {
	return &ChainBuilder{}
}

// Build returns the chain from the leaf up to the trust anchor.
func (b *ChainBuilder) Build(leaf Cert, intermediates []Cert, anchors []Cert) ([]Cert, error) {
	return nil, errors.New("not implemented")
}
