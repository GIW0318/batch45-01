package task

import (
	"errors"
	"time"
)

var errNotImplemented = errors.New("not implemented")

// Key is one kernel key owned by a tenant.
type Key struct {
	ID          int
	Tenant      string
	Description string
	Revoked     bool
	Negative    bool
	ExpiresAt   time.Time
}

// Keyring is one keyring of a tenant.
type Keyring struct {
	Name   string
	Tenant string
	Parent string
	Depth  int
}

// QuotaOptions configures the quota guard.
type QuotaOptions struct {
	MaxPerTenant   int
	MaxRoot        int
	MaxDescription int
	MaxDepth       int
	RootTenant     string
}

// Usage is a consistent quota usage snapshot.
type Usage struct {
	Tenant  string
	Keys    int
	Version uint64
}

// KeyringQuotaGuard enforces per tenant key quotas.
type KeyringQuotaGuard struct{}

// NewKeyringQuotaGuard returns a guard configured by opts.
func NewKeyringQuotaGuard(opts QuotaOptions) *KeyringQuotaGuard {
	return &KeyringQuotaGuard{}
}

// CreateRing creates a keyring below parent.
func (g *KeyringQuotaGuard) CreateRing(tenant, name, parent string) (*Keyring, error) {
	return nil, errNotImplemented
}

// AddKey adds a key to the tenant session keyring.
func (g *KeyringQuotaGuard) AddKey(tenant, description string, expiresAt time.Time) (*Key, error) {
	return nil, errNotImplemented
}

// AddNegativeKey adds a negative key that is not usable.
func (g *KeyringQuotaGuard) AddNegativeKey(tenant, description string) (*Key, error) {
	return nil, errNotImplemented
}

// Link links a key into a keyring.
func (g *KeyringQuotaGuard) Link(tenant string, key *Key, ring *Keyring) error {
	return errNotImplemented
}

// Delete removes a key and releases its quota immediately.
func (g *KeyringQuotaGuard) Delete(key *Key) error {
	return errNotImplemented
}

// Revoke revokes a key so it no longer counts against the quota.
func (g *KeyringQuotaGuard) Revoke(key *Key) error {
	return errNotImplemented
}

// Usage returns the quota usage of a tenant.
func (g *KeyringQuotaGuard) Usage(tenant string) (Usage, error) {
	return Usage{}, errNotImplemented
}

// Reclaim removes expired keys and reports how many were reclaimed.
func (g *KeyringQuotaGuard) Reclaim(now time.Time) (int, error) {
	return 0, errNotImplemented
}

// Capacity reports used and allowed keys of a tenant.
func (g *KeyringQuotaGuard) Capacity(tenant string) (int, int) {
	return 0, 0
}

// Snapshot serialises the quota state.
func (g *KeyringQuotaGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds quota state from a snapshot.
func (g *KeyringQuotaGuard) Restore(data []byte) error {
	return errNotImplemented
}

// TotalKeys reports how many keys are currently accounted.
func (g *KeyringQuotaGuard) TotalKeys() int {
	return 0
}
