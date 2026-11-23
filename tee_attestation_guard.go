package task

import (
	"errors"
	"time"
)

var (
	ErrChainInvalid   = errors.New("attestation: certificate chain does not reach the root")
	ErrNonceMismatch  = errors.New("attestation: report nonce does not match the request")
	ErrNonceReplay    = errors.New("attestation: nonce already used")
	ErrMeasurement    = errors.New("attestation: enclave measurement not allowed")
	ErrTCBTooOld      = errors.New("attestation: TCB version below the required floor")
	ErrReportExpired  = errors.New("attestation: report too old")
	ErrTEEType        = errors.New("attestation: TEE type not allowed")
	ErrPublicKeyBind  = errors.New("attestation: report public key not bound to the TLS certificate")
	ErrReportRejected = errors.New("attestation: report rejected")
)

// Report is a parsed TEE attestation report.
type Report struct {
	Type        string
	Measurement string
	TCBVersion  string
	Nonce       string
	PublicKey   []byte
	IssuedAt    time.Time
	Chain       []string
}

// Policy is the verifier policy.
type Policy struct {
	AllowedMeasurements []string
	MinTCBVersion       string
	MaxAge              time.Duration
	AllowedTypes        []string
	RootCert            string
	Revoked             []string
}

// Result is the outcome of a successful verification.
type Result struct {
	Measurement string
	Type        string
	Verified    bool
	CacheKey    string
}

// TeeAttestationGuard verifies TEE reports against a policy.
type TeeAttestationGuard struct{}

// NewTeeAttestationGuard creates a verifier with a fixed clock origin.
func NewTeeAttestationGuard(p Policy, now time.Time) (*TeeAttestationGuard, error) {
	return nil, errors.New("not implemented")
}

// Verify performs the full check: TEE type, chain to root, nonce binding and
// freshness, measurement allowlist and revocation, TCB floor, report age and
// optional TLS public key binding. A nonce may be accepted only once.
func (g *TeeAttestationGuard) Verify(r Report, expectedNonce string, tlsPubKey []byte) (Result, error) {
	return Result{}, errors.New("not implemented")
}

// VerifyCertChain accepts the chain in any order as long as it reaches root.
func (g *TeeAttestationGuard) VerifyCertChain(chain []string, root string) error {
	return errors.New("not implemented")
}

// BindPublicKey checks the report key against the TLS certificate key.
func (g *TeeAttestationGuard) BindPublicKey(r Report, tlsPubKey []byte) error {
	return errors.New("not implemented")
}

// CacheKey returns the cache partition key: the measurement must be part of
// it, the nonce must not be the only component.
func (g *TeeAttestationGuard) CacheKey(r Report) string {
	return ""
}

// Restart simulates a process restart. The nonce cache must survive it.
func (g *TeeAttestationGuard) Restart() error {
	return errors.New("not implemented")
}

// SetSoftwareFallback toggles the fallback. Even when enabled, a failed
// hardware report must never be reported as verified.
func (g *TeeAttestationGuard) SetSoftwareFallback(enabled bool) {
}

// NonceUsed reports whether a nonce was already consumed.
func (g *TeeAttestationGuard) NonceUsed(nonce string) bool {
	return false
}

// Revoke adds a measurement to the revocation list.
func (g *TeeAttestationGuard) Revoke(measurement string) error {
	return errors.New("not implemented")
}
