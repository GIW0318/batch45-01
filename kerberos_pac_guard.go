package task

import (
	"errors"
	"time"
)

// Config holds Kerberos PAC validation policy.
type Config struct {
	MaxSkew   time.Duration
	PACMaxAge time.Duration
	// AllowedEnctypes lists the ticket encryption types that may be accepted.
	AllowedEnctypes []string
}

// PAC is the privilege attribute certificate carried in a ticket.
type PAC struct {
	LogonServer string
	LogonDomain string
	// UserSID and GroupSIDs are domain qualified, e.g.
	// "domain-a/S-1-5-21-1-2-3-1001".
	UserSID         string
	GroupSIDs       []string
	KDCChecksum     string
	ServiceChecksum string
	IssuedAt        time.Time
	ExpiresAt       time.Time
	// DelegationAllowed is the PAC delegates flag.
	DelegationAllowed bool
}

// Ticket is a service ticket.
type Ticket struct {
	ID      string
	SPN     string
	Enctype string
	PAC     PAC
	// ReferralPath is the list of domains the referral ticket walked through.
	ReferralPath []string
}

// Authenticator is the client authenticator that accompanies a ticket.
type Authenticator struct {
	ClientPrincipal string
	Nonce           string
	Timestamp       time.Time
}

// ServicePrincipal is the service the ticket is presented to.
type ServicePrincipal struct {
	Name                 string
	TrustedForDelegation bool
}

// ValidationResult is the outcome of PAC validation.
type ValidationResult struct {
	OK        bool
	Reason    string
	Delegated bool
}

// PacGuard validates Kerberos PACs at the service gateway.
type PacGuard struct {
	cfg Config
}

// NewPacGuard returns a guard configured by cfg.
func NewPacGuard(cfg Config) *PacGuard {
	return &PacGuard{cfg: cfg}
}

// NewPacGuardFromSnapshot restores the replay cache from a snapshot.
func NewPacGuardFromSnapshot(cfg Config, data []byte) *PacGuard {
	return &PacGuard{cfg: cfg}
}

// TrustDomain declares a domain that group SIDs and referral paths may
// reference.
func (g *PacGuard) TrustDomain(domain string) error {
	return errors.New("not implemented")
}

// Validate checks a ticket and returns the authorization decision.
//
// Both the KDC and the service checksum must be present, the enctype must be
// strong, the PAC must be fresh, every SID domain and referral hop must be
// trusted, the logon information must be complete, the SPN must match the
// service case-insensitively, delegation must only be granted to trusted
// services, the authenticator must be fresh and unused, and the ticket must
// match the authenticator nonce.
func (g *PacGuard) Validate(t Ticket, a Authenticator, svc ServicePrincipal, now time.Time) (ValidationResult, error) {
	return ValidationResult{}, errors.New("not implemented")
}

// NormalizeSPN returns the canonical form of a service principal name.
func (g *PacGuard) NormalizeSPN(spn string) (string, error) {
	return "", errors.New("not implemented")
}

// CachedResult returns the cached validation result for a ticket.
func (g *PacGuard) CachedResult(ticketID string) (ValidationResult, bool, error) {
	return ValidationResult{}, false, errors.New("not implemented")
}

// Snapshot serialises the replay cache.
func (g *PacGuard) Snapshot() ([]byte, error) {
	return nil, errors.New("not implemented")
}
