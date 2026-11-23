package task

import (
	"errors"
	"time"
)

// Key purposes and version states.
const (
	PurposeEncrypt = "encrypt"
	PurposeSign    = "sign"

	StateActive    = "active"
	StateRetired   = "retired"
	StateDestroyed = "destroyed"
)

var (
	ErrNoKey          = errors.New("kms: unknown key")
	ErrPurpose        = errors.New("kms: key purpose mismatch")
	ErrImportLength   = errors.New("kms: imported material has invalid length")
	ErrConfirmNeeded  = errors.New("kms: destroy requires confirmation")
	ErrActiveDestroy  = errors.New("kms: active version cannot be destroyed")
	ErrHSMDown        = errors.New("kms: HSM session is down")
	ErrRotationFailed = errors.New("kms: rotation failed")
	ErrUnknownVersion = errors.New("kms: unknown key version")
)

// KeyVersion is one version of a key.
type KeyVersion struct {
	Tenant    string
	KeyID     string
	Version   int
	Purpose   string
	State     string
	CreatedAt time.Time
	HSMHandle string
}

// Ciphertext binds the key version that produced it.
type Ciphertext struct {
	Tenant  string
	KeyID   string
	Version int
	Blob    []byte
}

// RotationGuard manages key versions and rotations.
type RotationGuard struct{}

// NewRotationGuard creates an empty guard with a fixed clock origin.
func NewRotationGuard(now time.Time) (*RotationGuard, error) {
	return nil, errors.New("not implemented")
}

// CreateKey creates version 1 of a key for one tenant. Key ids are scoped per
// tenant.
func (g *RotationGuard) CreateKey(tenant, keyID, purpose string, material []byte) (KeyVersion, error) {
	return KeyVersion{}, errors.New("not implemented")
}

// ImportKey imports material into a new key, validating its length.
func (g *RotationGuard) ImportKey(tenant, keyID string, material []byte) (KeyVersion, error) {
	return KeyVersion{}, errors.New("not implemented")
}

// Encrypt seals plaintext with the active encryption key version and records
// that version in the ciphertext.
func (g *RotationGuard) Encrypt(tenant, keyID string, plaintext []byte) (Ciphertext, error) {
	return Ciphertext{}, errors.New("not implemented")
}

// Decrypt opens a ciphertext with exactly the version it was written with,
// which stays valid while the version exists.
func (g *RotationGuard) Decrypt(ct Ciphertext) ([]byte, error) {
	return nil, errors.New("not implemented")
}

// Sign signs msg with a signing key. Encryption keys must be refused.
func (g *RotationGuard) Sign(tenant, keyID string, msg []byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

// Rotate creates a new active version and retires the previous one. Two calls
// made at the same guard time must produce a single new version.
func (g *RotationGuard) Rotate(tenant, keyID string) (KeyVersion, error) {
	return KeyVersion{}, errors.New("not implemented")
}

// Destroy removes a retired version. The caller must pass the confirmation
// string "CONFIRM" and the active version may never be destroyed.
func (g *RotationGuard) Destroy(tenant, keyID string, version int, confirm string) error {
	return errors.New("not implemented")
}

// Versions lists the versions of one tenant key ordered by version.
func (g *RotationGuard) Versions(tenant, keyID string) []KeyVersion {
	return nil
}

// Version returns one version of one tenant key.
func (g *RotationGuard) Version(tenant, keyID string, v int) (KeyVersion, bool) {
	return KeyVersion{}, false
}

// DropHSM simulates losing the HSM session.
func (g *RotationGuard) DropHSM() error {
	return errors.New("not implemented")
}

// Reconnect re-establishes the HSM session and revalidates existing handles.
func (g *RotationGuard) Reconnect() error {
	return errors.New("not implemented")
}

// HSMUp reports whether the HSM session is usable.
func (g *RotationGuard) HSMUp() bool {
	return false
}

// Backup exports the key state outside the primary fault domain. Raw key
// material must not appear verbatim in the result.
func (g *RotationGuard) Backup() ([]byte, error) {
	return nil, errors.New("not implemented")
}

// Restore loads the state produced by Backup.
func (g *RotationGuard) Restore(state []byte) error {
	return errors.New("not implemented")
}

// CacheGet returns the cached active version for a key.
func (g *RotationGuard) CacheGet(tenant, keyID string) (KeyVersion, bool) {
	return KeyVersion{}, false
}

// SetCacheTTL configures how long a cached version stays valid.
func (g *RotationGuard) SetCacheTTL(d time.Duration) {
}

// Advance moves the guard clock forward.
func (g *RotationGuard) Advance(d time.Duration) {
}

// FailNextRotate makes the next rotation of one key fail once.
func (g *RotationGuard) FailNextRotate(tenant, keyID string) error {
	return errors.New("not implemented")
}
