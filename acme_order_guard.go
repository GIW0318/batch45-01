package task

import (
	"errors"
	"time"
)

// Sentinel errors of the ACME order guard.
var (
	ErrAccountMismatch  = errors.New("challenge token not bound to this account")
	ErrChallengeReplay  = errors.New("challenge token already validated")
	ErrNonceReplay      = errors.New("nonce already used")
	ErrOrderState       = errors.New("order state transition not allowed")
	ErrHostMismatch     = errors.New("http-01 probe host does not match")
	ErrTXTOwnerMismatch = errors.New("dns-01 txt record owner does not match")
	ErrRetryExhausted   = errors.New("retry budget exhausted")
	ErrOrderStale       = errors.New("order belongs to a rotated account key")
)

// Account is one ACME account.
type Account struct {
	ID         string
	KeyID      string
	Thumbprint string
}

// OrderStatus is the ACME order state machine.
type OrderStatus int

const (
	OrderPending OrderStatus = iota
	OrderReady
	OrderProcessing
	OrderValid
	OrderInvalid
)

// Challenge is one authorization challenge.
type Challenge struct {
	Type      string
	Token     string
	Domain    string
	Validated bool
}

// Order is one ACME order.
type Order struct {
	ID                string
	AccountID         string
	AccountKeyID      string
	Domains           []string
	Status            OrderStatus
	AuthorizationDone bool
	Challenges        []Challenge
}

// ChallengeProbe is what the validation server observed.
type ChallengeProbe struct {
	HostHeader string
	TXTName    string
	TXTValue   string
}

// KeyAuthorization is the key authorization for one challenge.
type KeyAuthorization struct {
	Token      string
	Thumbprint string
	Value      string
}

// CertRef is a reference to an issued certificate.
type CertRef struct {
	ID       string
	Domain   string
	IssuedAt time.Time
	NotAfter time.Time
}

// RenewResult is the outcome of a renewal attempt.
type RenewResult struct {
	Cert         CertRef
	FallbackUsed bool
	Alerted      bool
}

// SealedKey keeps key material sealed in memory.
type SealedKey struct {
	ID string
}

// String renders the sealed key without exposing key material.
func (k SealedKey) String() string {
	return ""
}

// AcmeOrderGuard guards ACME orders, challenges and renewals.
type AcmeOrderGuard struct {
}

// NewAcmeOrderGuard creates an empty guard.
func NewAcmeOrderGuard() *AcmeOrderGuard {
	return &AcmeOrderGuard{}
}

// CreateOrder creates a pending order for the account.
func (g *AcmeOrderGuard) CreateOrder(acct Account, domains []string) (Order, error) {
	return Order{}, errors.New("not implemented")
}

// IssueChallenge mints a challenge token bound to the account.
func (g *AcmeOrderGuard) IssueChallenge(orderID, accountID, typ string) (KeyAuthorization, error) {
	return KeyAuthorization{}, errors.New("not implemented")
}

// ValidateChallenge validates one challenge attempt.
func (g *AcmeOrderGuard) ValidateChallenge(orderID, token string, probe ChallengeProbe) error {
	return errors.New("not implemented")
}

// UseNonce consumes a single use nonce.
func (g *AcmeOrderGuard) UseNonce(nonce string) error {
	return errors.New("not implemented")
}

// Finalize requests the certificate for an authorized order.
func (g *AcmeOrderGuard) Finalize(orderID string) (CertRef, error) {
	return CertRef{}, errors.New("not implemented")
}

// RetryDelay returns the backoff delay of one attempt.
func (g *AcmeOrderGuard) RetryDelay(attempt int) (time.Duration, error) {
	return 0, errors.New("not implemented")
}

// RotateAccountKey rotates the account key and stales old orders.
func (g *AcmeOrderGuard) RotateAccountKey(accountID, newKeyID string) error {
	return errors.New("not implemented")
}

// Renew renews a certificate once across all replicas.
func (g *AcmeOrderGuard) Renew(domain string, replicas int) (RenewResult, error) {
	return RenewResult{}, errors.New("not implemented")
}

// Revoke revokes a certificate and clears its cache entry.
func (g *AcmeOrderGuard) Revoke(certID string) error {
	return errors.New("not implemented")
}

// Cached returns the cached certificate of a domain.
func (g *AcmeOrderGuard) Cached(domain string) (CertRef, bool) {
	return CertRef{}, false
}

// StoreCert seeds the certificate cache.
func (g *AcmeOrderGuard) StoreCert(c CertRef) error {
	return errors.New("not implemented")
}

// SetIssuerAvailable toggles the upstream CA.
func (g *AcmeOrderGuard) SetIssuerAvailable(ok bool) error {
	return errors.New("not implemented")
}

// SealKey loads private key material without keeping it in plaintext.
func (g *AcmeOrderGuard) SealKey(raw []byte) (SealedKey, error) {
	return SealedKey{}, errors.New("not implemented")
}

// OrderStatus reports the current state of an order.
func (g *AcmeOrderGuard) OrderStatus(orderID string) (OrderStatus, error) {
	return OrderPending, errors.New("not implemented")
}
