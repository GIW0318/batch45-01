package task

import (
	"errors"
	"time"
)

// Config holds TOTP policy.
type Config struct {
	Period      time.Duration
	Digits      int
	SkewSteps   int
	MaxFailures int
	// KeyGrace keeps a rotated-away secret usable for in-flight verifications.
	KeyGrace time.Duration
	// EncryptionKey protects secrets at rest.
	EncryptionKey []byte
}

// KeyRecord is one TOTP secret.
type KeyRecord struct {
	TenantID  string
	KeyID     string
	Secret    []byte
	Algorithm string
	Version   uint16
}

// VerifyResult is the outcome of a verification.
type VerifyResult struct {
	OK         bool
	Reason     string
	DriftSteps int
	Locked     bool
}

// TotpGuard owns TOTP secrets, replay state and rate limiting.
type TotpGuard struct {
	cfg Config
}

// NewTotpGuard returns a guard configured by cfg.
func NewTotpGuard(cfg Config) *TotpGuard {
	return &TotpGuard{cfg: cfg}
}

// NewTotpGuardFromSnapshot restores keys, used codes and failure counters from
// a snapshot.
func NewTotpGuardFromSnapshot(cfg Config, data []byte) *TotpGuard {
	return &TotpGuard{cfg: cfg}
}

// AddKey stores a TOTP secret. Key ids are scoped per tenant.
func (g *TotpGuard) AddKey(k KeyRecord) error {
	return errors.New("not implemented")
}

// RotateKey replaces the secret of tenantID/keyID, keeping the previous secret
// usable for cfg.KeyGrace.
func (g *TotpGuard) RotateKey(tenantID, keyID string, newSecret []byte, algorithm string, now time.Time) error {
	return errors.New("not implemented")
}

// Code returns the TOTP code for tenantID/keyID at the time step containing at.
func (g *TotpGuard) Code(tenantID, keyID string, at time.Time) (string, error) {
	return "", errors.New("not implemented")
}

// Verify checks code against the current step plus the configured tolerance
// window and reports the observed drift. A code may be used once per user and
// step.
func (g *TotpGuard) Verify(tenantID, keyID, userID, code string, now time.Time) (VerifyResult, error) {
	return VerifyResult{}, errors.New("not implemented")
}

// AddBackupCodes stores single-use backup codes for a user.
func (g *TotpGuard) AddBackupCodes(tenantID, userID string, codes []string) error {
	return errors.New("not implemented")
}

// VerifyBackupCode consumes a backup code.
func (g *TotpGuard) VerifyBackupCode(tenantID, userID, code string) (bool, error) {
	return false, errors.New("not implemented")
}

// Failures returns the consecutive failure count of a user.
func (g *TotpGuard) Failures(tenantID, userID string) (int, error) {
	return 0, errors.New("not implemented")
}

// Unlock clears the failure counter of a user.
func (g *TotpGuard) Unlock(tenantID, userID string) error {
	return errors.New("not implemented")
}

// SecretAtRest returns the stored representation of a secret; it must not be
// the plaintext secret.
func (g *TotpGuard) SecretAtRest(tenantID, keyID string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

// Snapshot serialises secrets, used codes and failure counters.
func (g *TotpGuard) Snapshot() ([]byte, error) {
	return nil, errors.New("not implemented")
}

// ConstantTimeEqual compares two codes without leaking their content through
// timing.
func ConstantTimeEqual(a, b string) bool {
	return false
}
