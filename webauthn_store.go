package task

import (
	"errors"
	"time"
)

// Config holds WebAuthn relying party policy.
type Config struct {
	RPID                 string
	Origin               string
	RequireUV            bool
	AllowSelfAttestation bool
	TrustedRoots         []string
	ChallengeTTL         time.Duration
}

// Attestation describes the attestation statement of a registration.
type Attestation struct {
	Format     string
	Chain      []string
	SelfSigned bool
	// Root is the trust anchor of the chain.
	Root string
}

// Registration is one authenticator registration request.
type Registration struct {
	TenantID     string
	UserID       string
	Username     string
	CredentialID string
	UserHandle   string
	PublicKey    []byte
	Origin       string
	// RPIDHash is the lower-case hex SHA-256 of the relying party id.
	RPIDHash    string
	UV          bool
	BE          bool
	BS          bool
	Attestation Attestation
	Challenge   string
	Counter     uint32
}

// Credential is a stored credential.
type Credential struct {
	TenantID     string
	UserID       string
	Username     string
	CredentialID string
	UserHandle   string
	Origin       string
	BE           bool
	BS           bool
	Frozen       bool
	Counter      uint32
}

// AuthResult is the outcome of an authentication.
type AuthResult struct {
	OK      bool
	Reason  string
	Counter uint32
}

// CredentialStore holds WebAuthn credentials.
type CredentialStore struct {
	cfg Config
}

// NewCredentialStore returns a store configured by cfg.
func NewCredentialStore(cfg Config) *CredentialStore {
	return &CredentialStore{cfg: cfg}
}

// NewCredentialStoreFromSnapshot restores a store from a snapshot.
func NewCredentialStoreFromSnapshot(cfg Config, data []byte) *CredentialStore {
	return &CredentialStore{cfg: cfg}
}

// BeginChallenge mints a registration challenge for userID.
func (g *CredentialStore) BeginChallenge(userID string) (string, error) {
	return "", errors.New("not implemented")
}

// FinishRegistration validates and stores a registration.
//
// The challenge must be unexpired and bound to the user, the origin must match
// exactly, the RP ID hash must match, UV must be present when required, the
// attestation chain must be evaluated against the trusted roots, the credential
// id must be globally unique, and a user handle may belong to one account only.
func (g *CredentialStore) FinishRegistration(r Registration) (Credential, error) {
	return Credential{}, errors.New("not implemented")
}

// Authenticate verifies an assertion against a stored credential.
func (g *CredentialStore) Authenticate(tenantID, credentialID, origin, rpIDHash string, uv bool, counter uint32) (AuthResult, error) {
	return AuthResult{}, errors.New("not implemented")
}

// Freeze marks a credential as unusable.
func (g *CredentialStore) Freeze(tenantID, credentialID string) error {
	return errors.New("not implemented")
}

// Discover lists the discoverable credentials of one user in one tenant.
func (g *CredentialStore) Discover(tenantID, userID string) ([]Credential, error) {
	return nil, errors.New("not implemented")
}

// Snapshot serialises the stored public keys and credential records.
func (g *CredentialStore) Snapshot() ([]byte, error) {
	return nil, errors.New("not implemented")
}
