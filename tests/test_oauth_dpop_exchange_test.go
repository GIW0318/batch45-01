package task

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-07"
)

func testConfig() taskpkg.Config {
	return taskpkg.Config{
		MaxSkew:  30 * time.Second,
		JTITTL:   time.Hour,
		NonceTTL: time.Minute,
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newProof(jti, nonce string, iat time.Time) taskpkg.Proof {
	return taskpkg.Proof{
		JTI:            jti,
		HTM:            "POST",
		HTU:            "https://as.example.com/token",
		ATH:            sha256Hex("access-1"),
		Alg:            "ES256",
		Thumbprint:     "thumb-1",
		IAT:            iat,
		Nonce:          nonce,
		TenantID:       "tenant-a",
		SignatureValid: true,
	}
}

func setupExchange(t *testing.T, now time.Time) (*taskpkg.DpopExchange, taskpkg.ExchangeRequest) {
	t.Helper()
	g := taskpkg.NewDpopExchange(testConfig())
	if err := g.RegisterAccessToken(taskpkg.Token{
		Value:      "access-1",
		TenantID:   "tenant-a",
		Thumbprint: "thumb-1",
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RegisterAccessToken: %v", err)
	}
	if err := g.RegisterRefreshToken("refresh-1", "thumb-1", "tenant-a"); err != nil {
		t.Fatalf("RegisterRefreshToken 1: %v", err)
	}
	if err := g.RegisterRefreshToken("refresh-2", "thumb-1", "tenant-a"); err != nil {
		t.Fatalf("RegisterRefreshToken 2: %v", err)
	}
	return g, taskpkg.ExchangeRequest{
		Method:       "POST",
		URL:          "https://as.example.com/token",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TenantID:     "tenant-a",
	}
}

func freshNonce(t *testing.T, g *taskpkg.DpopExchange) string {
	t.Helper()
	nonce, err := g.IssueNonce()
	if err != nil {
		t.Fatalf("IssueNonce: %v", err)
	}
	return nonce
}

// setupWithBaseline first proves that a correct proof is accepted, then hands
// back a fresh exchange for the negative case.
func setupWithBaseline(t *testing.T, now time.Time) (*taskpkg.DpopExchange, taskpkg.ExchangeRequest) {
	t.Helper()
	guard, req := setupExchange(t, now)
	if _, err := guard.Exchange(newProof("jti-baseline", freshNonce(t, guard), now), req, now); err != nil {
		t.Fatalf("baseline valid exchange failed: %v", err)
	}
	return setupExchange(t, now)
}

// A proof id must not be usable twice inside its window.
func TestJTIReplayRejectedWithinWindow(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupExchange(t, now)
	first := newProof("jti-replay", freshNonce(t, g), now)
	if _, err := g.Exchange(first, req, now); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	replay := first
	replay.Nonce = freshNonce(t, g)
	req.RefreshToken = "refresh-2"
	if _, err := g.Exchange(replay, req, now); !errors.Is(err, taskpkg.ErrReplay) {
		t.Fatalf("replayed jti must yield ErrReplay, got %v", err)
	}
}

// htm must match the request method exactly.
func TestHTMExactMatch(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-htm", freshNonce(t, g), now)
	proof.HTM = "GET"
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrProofInvalid) {
		t.Fatalf("proof with a mismatched htm must be invalid, got %v", err)
	}
}

// htu must match the full URL, not just a prefix.
func TestHTUExactMatch(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-htu", freshNonce(t, g), now)
	proof.HTU = "https://as.example.com/token/other"
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrProofInvalid) {
		t.Fatalf("proof for another path must be invalid, got %v", err)
	}
}

// ath must be bound to the presented access token.
func TestATHMustMatchAccessToken(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-ath", freshNonce(t, g), now)
	proof.ATH = sha256Hex("access-2")
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrProofInvalid) {
		t.Fatalf("proof with a foreign ath must be invalid, got %v", err)
	}
}

// The "none" algorithm must never be accepted.
func TestAlgNoneRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-none", freshNonce(t, g), now)
	proof.Alg = "none"
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrProofInvalid) {
		t.Fatalf("alg none must be rejected as invalid, got %v", err)
	}
}

// The proof key thumbprint must match the thumbprint the token is bound to.
func TestThumbprintMismatchRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-thumb", freshNonce(t, g), now)
	proof.Thumbprint = "thumb-other"
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrProofInvalid) {
		t.Fatalf("foreign thumbprint must be invalid, got %v", err)
	}
}

// A proof older than the allowed skew must be rejected.
func TestClockSkewBeyondLimitRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-skew", freshNonce(t, g), now.Add(-5*time.Minute))
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrProofInvalid) {
		t.Fatalf("proof outside the skew must be invalid, got %v", err)
	}
}

// A proof inside the allowed skew must be accepted.
func TestClockSkewWithinLimitAccepted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupExchange(t, now)
	proof := newProof("jti-skew-ok", freshNonce(t, g), now.Add(-10*time.Second))
	token, err := g.Exchange(proof, req, now)
	if err != nil {
		t.Fatalf("proof inside the skew was rejected: %v", err)
	}
	if token.Value == "" {
		t.Fatalf("exchange did not return a token")
	}
	if token.TenantID != "tenant-a" {
		t.Fatalf("token tenant mismatch: %q", token.TenantID)
	}
}

// Two concurrent submissions of the same jti must not both obtain a token.
func TestConcurrentJTISingleUse(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupExchange(t, now)
	first := newProof("jti-race", freshNonce(t, g), now)
	second := first
	second.Nonce = freshNonce(t, g)
	req2 := req
	req2.RefreshToken = "refresh-2"
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = g.Exchange(first, req, now)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = g.Exchange(second, req2, now)
	}()
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("want exactly one concurrent exchange to succeed, got %d", successes)
	}
}

// A refresh token rotation must invalidate proofs made for the old refresh
// token.
func TestProofBoundToRefreshTokenRotation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupExchange(t, now)
	if _, err := g.Exchange(newProof("jti-rot-1", freshNonce(t, g), now), req, now); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	reuse := newProof("jti-rot-2", freshNonce(t, g), now)
	if _, err := g.Exchange(reuse, req, now); err == nil {
		t.Fatalf("old refresh token was accepted after rotation")
	}
}

// A proof without a server nonce must be rejected.
func TestNonceRequired(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-no-nonce", "", now)
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrNonce) {
		t.Fatalf("missing nonce must yield ErrNonce, got %v", err)
	}
}

// A server nonce may be used only once.
func TestNonceSingleUse(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupExchange(t, now)
	nonce := freshNonce(t, g)
	if _, err := g.Exchange(newProof("jti-n-1", nonce, now), req, now); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	req.RefreshToken = "refresh-2"
	reuse := newProof("jti-n-2", nonce, now)
	if _, err := g.Exchange(reuse, req, now); !errors.Is(err, taskpkg.ErrNonce) {
		t.Fatalf("reused nonce must yield ErrNonce, got %v", err)
	}
}

// A proof of one tenant must not be exchangeable for another tenant's token.
func TestCrossTenantProofRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	proof := newProof("jti-tenant", freshNonce(t, g), now)
	proof.TenantID = "tenant-b"
	if _, err := g.Exchange(proof, req, now); !errors.Is(err, taskpkg.ErrProofInvalid) {
		t.Fatalf("cross tenant proof must be invalid, got %v", err)
	}
}

// Replay and signature failures must be distinguishable.
func TestReplayErrorDistinctFromSignatureError(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupWithBaseline(t, now)
	badSig := newProof("jti-sig", freshNonce(t, g), now)
	badSig.SignatureValid = false
	if _, err := g.Exchange(badSig, req, now); !errors.Is(err, taskpkg.ErrSignature) {
		t.Fatalf("bad signature must yield ErrSignature, got %v", err)
	} else if errors.Is(err, taskpkg.ErrReplay) {
		t.Fatalf("signature failure reported as a replay")
	}
	g2, req2 := setupExchange(t, now)
	first := newProof("jti-replay-2", freshNonce(t, g2), now)
	if _, err := g2.Exchange(first, req2, now); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	req2.RefreshToken = "refresh-2"
	replay := first
	replay.Nonce = freshNonce(t, g2)
	if _, err := g2.Exchange(replay, req2, now); !errors.Is(err, taskpkg.ErrReplay) {
		t.Fatalf("replay must yield ErrReplay, got %v", err)
	} else if errors.Is(err, taskpkg.ErrSignature) {
		t.Fatalf("replay reported as a signature failure")
	}
}

// After a restart the used proof ids must still be known.
func TestJTIStoreSurvivesRestart(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g, req := setupExchange(t, now)
	const jti = "jti-persist"
	if _, err := g.Exchange(newProof(jti, freshNonce(t, g), now), req, now); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored, err := taskpkg.NewDpopExchangeFromSnapshot(testConfig(), snap)
	if err != nil {
		t.Fatalf("NewDpopExchangeFromSnapshot: %v", err)
	}
	used, err := restored.JTIUsed(jti)
	if err != nil {
		t.Fatalf("JTIUsed: %v", err)
	}
	if !used {
		t.Fatalf("used proof id was forgotten across restart")
	}
}
