package task

import (
	"errors"
	"time"
)

// Sentinels returned by Exchange. Callers must be able to tell a replayed proof
// from a proof with a broken signature.
var (
	ErrProofInvalid = errors.New("dpop: invalid proof")
	ErrSignature    = errors.New("dpop: invalid signature")
	ErrReplay       = errors.New("dpop: replayed proof")
	ErrNonce        = errors.New("dpop: invalid nonce")
)

// Config holds DPoP policy.
type Config struct {
	// MaxSkew is the tolerated clock difference for the proof iat claim.
	MaxSkew time.Duration
	// JTITTL is how long a proof id is remembered.
	JTITTL time.Duration
	// NonceTTL is how long a server-issued nonce stays usable.
	NonceTTL time.Duration
}

// Proof is a DPoP proof as presented by the client.
type Proof struct {
	JTI string
	// HTM is the HTTP method the proof was made for.
	HTM string
	// HTU is the full HTTP URI the proof was made for.
	HTU string
	// ATH is the lower-case hex SHA-256 of the access token.
	ATH string
	// Alg is the signature algorithm; "none" must never be accepted.
	Alg        string
	Thumbprint string
	IAT        time.Time
	Nonce      string
	TenantID   string
	// SignatureValid is the result of signature verification.
	SignatureValid bool
}

// Token is an access or refresh token bound to a key thumbprint.
type Token struct {
	Value      string
	TenantID   string
	Thumbprint string
	ExpiresAt  time.Time
}

// ExchangeRequest is the token request a proof is presented with.
type ExchangeRequest struct {
	Method       string
	URL          string
	AccessToken  string
	RefreshToken string
	TenantID     string
}

// DpopExchange validates DPoP proofs and performs token exchange.
type DpopExchange struct {
	cfg Config
}

// NewDpopExchange returns an exchange configured by cfg.
func NewDpopExchange(cfg Config) *DpopExchange {
	return &DpopExchange{cfg: cfg}
}

// NewDpopExchangeFromSnapshot restores an exchange, including the used proof
// ids, from a snapshot.
func NewDpopExchangeFromSnapshot(cfg Config, data []byte) (*DpopExchange, error) {
	return nil, errors.New("not implemented")
}

// RegisterAccessToken records a token that proofs may be bound to via ath.
func (g *DpopExchange) RegisterAccessToken(t Token) error {
	return errors.New("not implemented")
}

// RegisterRefreshToken records a refresh token bound to a key thumbprint.
func (g *DpopExchange) RegisterRefreshToken(value, thumbprint, tenantID string) error {
	return errors.New("not implemented")
}

// IssueNonce mints a server nonce.
func (g *DpopExchange) IssueNonce() (string, error) {
	return "", errors.New("not implemented")
}

// Exchange validates proof against req and returns a new access token.
//
// The proof must match the method and full URL exactly, carry the ath of the
// presented access token, be bound to the registered thumbprint, use a real
// signature algorithm, stay inside the clock skew, come from the same tenant,
// present a fresh server nonce, and use an unseen jti. The refresh token is
// rotated on success.
func (g *DpopExchange) Exchange(proof Proof, req ExchangeRequest, now time.Time) (Token, error) {
	return Token{}, errors.New("not implemented")
}

// JTIUsed reports whether a proof id has already been consumed and is still
// inside its TTL.
func (g *DpopExchange) JTIUsed(jti string) (bool, error) {
	return false, errors.New("not implemented")
}

// Snapshot serialises the used proof ids so that a restarted server keeps
// rejecting replays.
func (g *DpopExchange) Snapshot() ([]byte, error) {
	return nil, errors.New("not implemented")
}
