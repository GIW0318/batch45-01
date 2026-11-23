package task

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// VerifyTimeout bounds a single Verify call. The caller always gets an answer
// within this window, even when the verifier is blocked on internal state.
const VerifyTimeout = 200 * time.Millisecond

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
	ErrVerifyTimeout  = errors.New("attestation: verification exceeded the 200ms deadline")
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
type TeeAttestationGuard struct {
	mu        sync.Mutex
	policy    Policy
	now       time.Time
	usedNonce map[string]struct{}
	revoked   map[string]struct{}
	fallback  bool
}

// NewTeeAttestationGuard creates a verifier with a fixed clock origin.
func NewTeeAttestationGuard(p Policy, now time.Time) (*TeeAttestationGuard, error) {
	if p.RootCert == "" {
		return nil, errors.New("attestation: policy requires a root certificate")
	}
	if len(p.AllowedTypes) == 0 {
		return nil, errors.New("attestation: policy requires at least one allowed TEE type")
	}
	if len(p.AllowedMeasurements) == 0 {
		return nil, errors.New("attestation: policy requires at least one allowed measurement")
	}
	if p.MaxAge <= 0 {
		return nil, errors.New("attestation: policy requires a positive report max age")
	}
	revoked := make(map[string]struct{}, len(p.Revoked))
	for _, measurement := range p.Revoked {
		revoked[measurement] = struct{}{}
	}
	return &TeeAttestationGuard{
		policy:    p,
		now:       now,
		usedNonce: make(map[string]struct{}),
		revoked:   revoked,
	}, nil
}

// Verify performs the full check: TEE type, chain to root, nonce binding and
// freshness, measurement allowlist and revocation, TCB floor, report age and
// optional TLS public key binding. A nonce may be accepted only once.
func (g *TeeAttestationGuard) Verify(r Report, expectedNonce string, tlsPubKey []byte) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), VerifyTimeout)
	defer cancel()

	type verifyOutcome struct {
		result Result
		err    error
	}
	done := make(chan verifyOutcome, 1)
	go func() {
		result, err := g.verify(ctx, r, expectedNonce, tlsPubKey)
		done <- verifyOutcome{result: result, err: err}
	}()

	select {
	case outcome := <-done:
		return outcome.result, outcome.err
	case <-ctx.Done():
		return Result{}, ErrVerifyTimeout
	}
}

func (g *TeeAttestationGuard) verify(ctx context.Context, r Report, expectedNonce string, tlsPubKey []byte) (Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Result{}, ErrVerifyTimeout
	}

	if !contains(g.policy.AllowedTypes, r.Type) {
		return Result{}, ErrTEEType
	}

	if err := g.verifyCertChainLocked(r.Chain, g.policy.RootCert); err != nil {
		return Result{}, err
	}

	if r.Nonce == "" || r.Nonce != expectedNonce {
		return Result{}, ErrNonceMismatch
	}

	if _, used := g.usedNonce[r.Nonce]; used {
		return Result{}, ErrNonceReplay
	}

	if !contains(g.policy.AllowedMeasurements, r.Measurement) {
		return Result{}, ErrMeasurement
	}
	if _, revoked := g.revoked[r.Measurement]; revoked {
		return Result{}, ErrMeasurement
	}

	if compareVersions(r.TCBVersion, g.policy.MinTCBVersion) < 0 {
		return Result{}, ErrTCBTooOld
	}

	if g.now.Sub(r.IssuedAt) > g.policy.MaxAge {
		return Result{}, ErrReportExpired
	}

	if len(tlsPubKey) > 0 && !bytes.Equal(r.PublicKey, tlsPubKey) {
		return Result{}, ErrPublicKeyBind
	}

	if err := ctx.Err(); err != nil {
		return Result{}, ErrVerifyTimeout
	}

	g.usedNonce[r.Nonce] = struct{}{}
	return Result{
		Measurement: r.Measurement,
		Type:        r.Type,
		Verified:    true,
		CacheKey:    g.cacheKeyLocked(r),
	}, nil
}

// VerifyCertChain accepts the chain in any order as long as it reaches root.
func (g *TeeAttestationGuard) VerifyCertChain(chain []string, root string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.verifyCertChainLocked(chain, root)
}

func (g *TeeAttestationGuard) verifyCertChainLocked(chain []string, root string) error {
	if root == "" || len(chain) == 0 {
		return ErrChainInvalid
	}
	for _, cert := range chain {
		if cert == root {
			return nil
		}
	}
	return ErrChainInvalid
}

// BindPublicKey checks the report key against the TLS certificate key.
func (g *TeeAttestationGuard) BindPublicKey(r Report, tlsPubKey []byte) error {
	if len(tlsPubKey) == 0 || !bytes.Equal(r.PublicKey, tlsPubKey) {
		return ErrPublicKeyBind
	}
	return nil
}

// CacheKey returns the cache partition key: the measurement must be part of
// it, the nonce must not be the only component.
func (g *TeeAttestationGuard) CacheKey(r Report) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cacheKeyLocked(r)
}

func (g *TeeAttestationGuard) cacheKeyLocked(r Report) string {
	return fmt.Sprintf("tee:%s:%s", r.Type, r.Measurement)
}

// Restart simulates a process restart. The nonce cache must survive it.
func (g *TeeAttestationGuard) Restart() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return nil
}

// SetSoftwareFallback toggles the fallback. Even when enabled, a failed
// hardware report must never be reported as verified.
func (g *TeeAttestationGuard) SetSoftwareFallback(enabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fallback = enabled
}

// NonceUsed reports whether a nonce was already consumed.
func (g *TeeAttestationGuard) NonceUsed(nonce string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, used := g.usedNonce[nonce]
	return used
}

// Revoke adds a measurement to the revocation list.
func (g *TeeAttestationGuard) Revoke(measurement string) error {
	if measurement == "" {
		return fmt.Errorf("%w: empty measurement", ErrMeasurement)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.revoked[measurement] = struct{}{}
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// compareVersions compares dotted numeric versions component by component.
// Missing components count as zero; non-numeric components parse by their
// numeric prefix so malformed input stays deterministic.
func compareVersions(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	length := len(partsA)
	if len(partsB) > length {
		length = len(partsB)
	}
	for i := 0; i < length; i++ {
		var left, right int
		if i < len(partsA) {
			left = versionComponent(partsA[i])
		}
		if i < len(partsB) {
			right = versionComponent(partsB[i])
		}
		switch {
		case left < right:
			return -1
		case left > right:
			return 1
		}
	}
	return 0
}

func versionComponent(part string) int {
	value := 0
	for _, char := range part {
		if char < '0' || char > '9' {
			break
		}
		value = value*10 + int(char-'0')
	}
	return value
}
