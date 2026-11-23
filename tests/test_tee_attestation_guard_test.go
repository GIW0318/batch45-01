package task

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-17"
)

var testNow = time.Unix(1700000000, 0)

func teePolicy() Policy {
	return Policy{
		AllowedMeasurements: []string{"measurement-a", "measurement-b"},
		MinTCBVersion:       "1.5.0",
		MaxAge:              time.Hour,
		AllowedTypes:        []string{"sgx"},
		RootCert:            "root-cert",
	}
}

func newTee(t *testing.T) *TeeAttestationGuard {
	t.Helper()
	g, err := NewTeeAttestationGuard(teePolicy(), testNow)
	if err != nil {
		t.Fatalf("NewTeeAttestationGuard: %v", err)
	}
	return g
}

func teeReport(nonce string) Report {
	return Report{
		Type:        "sgx",
		Measurement: "measurement-a",
		TCBVersion:  "1.5.0",
		Nonce:       nonce,
		PublicKey:   []byte("report-pk"),
		IssuedAt:    testNow.Add(-time.Minute),
		Chain:       []string{"leaf-cert", "intermediate-cert", "root-cert"},
	}
}

func TestChainVerifiedToRoot(t *testing.T) {
	g := newTee(t)
	if err := g.VerifyCertChain([]string{"leaf-cert", "intermediate-cert", "root-cert"}, "root-cert"); err != nil {
		t.Fatalf("valid chain rejected: %v", err)
	}
	if err := g.VerifyCertChain([]string{"leaf-cert", "intermediate-cert"}, "root-cert"); !errors.Is(err, ErrChainInvalid) {
		t.Fatalf("chain without root error = %v, want ErrChainInvalid", err)
	}
	r := teeReport("chain-nonce")
	r.Chain = []string{"leaf-cert", "intermediate-cert"}
	if _, err := g.Verify(r, "chain-nonce", nil); err == nil {
		t.Fatalf("report with an incomplete chain was accepted")
	}
}

func TestNonceBoundToRequest(t *testing.T) {
	g := newTee(t)
	if _, err := g.Verify(teeReport("nonce-a"), "nonce-b", nil); !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("mismatched nonce error = %v, want ErrNonceMismatch", err)
	}
	if g.NonceUsed("nonce-a") {
		t.Fatalf("mismatched nonce was consumed")
	}
	if _, err := g.Verify(teeReport("nonce-c"), "nonce-c", nil); err != nil {
		t.Fatalf("matching nonce rejected: %v", err)
	}
}

func TestMeasurementWhitelistEnforced(t *testing.T) {
	g := newTee(t)
	bad := teeReport("measurement-nonce-1")
	bad.Measurement = "rogue-measurement"
	if _, err := g.Verify(bad, "measurement-nonce-1", nil); !errors.Is(err, ErrMeasurement) {
		t.Fatalf("unknown measurement error = %v, want ErrMeasurement", err)
	}
	if _, err := g.Verify(teeReport("measurement-nonce-2"), "measurement-nonce-2", nil); err != nil {
		t.Fatalf("allowed measurement rejected: %v", err)
	}
}

func TestTCBVersionBelowFloorRejected(t *testing.T) {
	g := newTee(t)
	old := teeReport("tcb-nonce-1")
	old.TCBVersion = "1.4.9"
	if _, err := g.Verify(old, "tcb-nonce-1", nil); !errors.Is(err, ErrTCBTooOld) {
		t.Fatalf("old TCB error = %v, want ErrTCBTooOld", err)
	}
	atFloor := teeReport("tcb-nonce-2")
	atFloor.TCBVersion = "1.5.0"
	if _, err := g.Verify(atFloor, "tcb-nonce-2", nil); err != nil {
		t.Fatalf("TCB at floor rejected: %v", err)
	}
	newer := teeReport("tcb-nonce-3")
	newer.TCBVersion = "1.6.2"
	if _, err := g.Verify(newer, "tcb-nonce-3", nil); err != nil {
		t.Fatalf("newer TCB rejected: %v", err)
	}
}

func TestExpiredReportRejected(t *testing.T) {
	g := newTee(t)
	stale := teeReport("expired-nonce")
	stale.IssuedAt = testNow.Add(-2 * time.Hour)
	if _, err := g.Verify(stale, "expired-nonce", nil); !errors.Is(err, ErrReportExpired) {
		t.Fatalf("stale report error = %v, want ErrReportExpired", err)
	}
	if _, err := g.Verify(teeReport("fresh-nonce"), "fresh-nonce", nil); err != nil {
		t.Fatalf("fresh report rejected: %v", err)
	}
}

func TestNonceReplayRejected(t *testing.T) {
	g := newTee(t)
	r := teeReport("replay-nonce")
	if _, err := g.Verify(r, "replay-nonce", nil); err != nil {
		t.Fatalf("first verification: %v", err)
	}
	if !g.NonceUsed("replay-nonce") {
		t.Fatalf("consumed nonce not recorded")
	}
	if _, err := g.Verify(r, "replay-nonce", nil); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("replayed nonce error = %v, want ErrNonceReplay", err)
	}
	if _, err := g.Verify(teeReport("replay-nonce-2"), "replay-nonce-2", nil); err != nil {
		t.Fatalf("fresh nonce after replay: %v", err)
	}
}

func TestCertChainOrderInsensitive(t *testing.T) {
	g := newTee(t)
	shuffled := []string{"root-cert", "leaf-cert", "intermediate-cert"}
	if err := g.VerifyCertChain(shuffled, "root-cert"); err != nil {
		t.Fatalf("shuffled chain rejected: %v", err)
	}
	r := teeReport("order-nonce")
	r.Chain = shuffled
	if _, err := g.Verify(r, "order-nonce", nil); err != nil {
		t.Fatalf("report with shuffled chain rejected: %v", err)
	}
}

func TestMixedTEETypeRejected(t *testing.T) {
	g := newTee(t)
	tdx := teeReport("type-nonce-1")
	tdx.Type = "tdx"
	if _, err := g.Verify(tdx, "type-nonce-1", nil); !errors.Is(err, ErrTEEType) {
		t.Fatalf("tdx report error = %v, want ErrTEEType", err)
	}
	if _, err := g.Verify(teeReport("type-nonce-2"), "type-nonce-2", nil); err != nil {
		t.Fatalf("sgx report rejected: %v", err)
	}
}

func TestPublicKeyBoundToTLSCert(t *testing.T) {
	g := newTee(t)
	r := teeReport("pk-nonce-1")
	if _, err := g.Verify(r, "pk-nonce-1", []byte("other-pk")); !errors.Is(err, ErrPublicKeyBind) {
		t.Fatalf("mismatched TLS key error = %v, want ErrPublicKeyBind", err)
	}
	if g.NonceUsed("pk-nonce-1") {
		t.Fatalf("nonce consumed by a rejected report")
	}
	if _, err := g.Verify(teeReport("pk-nonce-2"), "pk-nonce-2", []byte("report-pk")); err != nil {
		t.Fatalf("matching TLS key rejected: %v", err)
	}
}

func TestCacheKeyPartitionedByMeasurement(t *testing.T) {
	g := newTee(t)
	first := teeReport("cache-nonce")
	second := first
	second.Measurement = "measurement-b"
	keyA := g.CacheKey(first)
	keyB := g.CacheKey(second)
	if keyA == "" {
		t.Fatalf("empty cache key")
	}
	if !strings.Contains(keyA, "measurement-a") {
		t.Fatalf("cache key %q does not include the measurement", keyA)
	}
	if keyA == keyB {
		t.Fatalf("cache key not partitioned by measurement: %q", keyA)
	}
	if again := g.CacheKey(first); again != keyA {
		t.Fatalf("cache key not stable: %q vs %q", again, keyA)
	}
}

func TestNonceCacheSurvivesRestart(t *testing.T) {
	g := newTee(t)
	if _, err := g.Verify(teeReport("persist-nonce"), "persist-nonce", nil); err != nil {
		t.Fatalf("initial verification: %v", err)
	}
	if err := g.Restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if _, err := g.Verify(teeReport("persist-nonce"), "persist-nonce", nil); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("nonce reuse after restart error = %v, want ErrNonceReplay", err)
	}
	if _, err := g.Verify(teeReport("after-restart"), "after-restart", nil); err != nil {
		t.Fatalf("verification after restart: %v", err)
	}
}

func TestNoSilentSoftwareFallback(t *testing.T) {
	g := newTee(t)
	g.SetSoftwareFallback(true)
	bad := teeReport("fallback-nonce-1")
	bad.Measurement = "rogue-measurement"
	res, err := g.Verify(bad, "fallback-nonce-1", nil)
	if err == nil {
		t.Fatalf("fallback accepted a failed hardware report")
	}
	if res.Verified {
		t.Fatalf("failed report marked verified through software fallback")
	}
	if _, err := g.Verify(teeReport("fallback-nonce-2"), "fallback-nonce-2", nil); err != nil {
		t.Fatalf("valid report rejected with fallback enabled: %v", err)
	}
}

func TestConcurrentSameNonceSingleSuccess(t *testing.T) {
	g := newTee(t)
	r := teeReport("concurrent-nonce")
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	replays := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := g.Verify(r, "concurrent-nonce", nil)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrNonceReplay):
				replays++
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	if replays != 9 {
		t.Fatalf("replays = %d, want 9", replays)
	}
	if !g.NonceUsed("concurrent-nonce") {
		t.Fatalf("nonce not marked used")
	}
}

func TestRevokedMeasurementRejected(t *testing.T) {
	g := newTee(t)
	if err := g.Revoke("measurement-a"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := g.Verify(teeReport("revoked-nonce"), "revoked-nonce", nil); !errors.Is(err, ErrMeasurement) {
		t.Fatalf("revoked measurement error = %v, want ErrMeasurement", err)
	}
	ok := teeReport("revoked-nonce-2")
	ok.Measurement = "measurement-b"
	if _, err := g.Verify(ok, "revoked-nonce-2", nil); err != nil {
		t.Fatalf("non-revoked measurement rejected: %v", err)
	}
}

func TestResultCarriesMeasurement(t *testing.T) {
	g := newTee(t)
	res, err := g.Verify(teeReport("result-nonce"), "result-nonce", nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Verified {
		t.Fatalf("result not marked verified")
	}
	if res.Measurement != "measurement-a" {
		t.Fatalf("result measurement = %q, want measurement-a", res.Measurement)
	}
	if res.Type != "sgx" {
		t.Fatalf("result type = %q, want sgx", res.Type)
	}
	if res.CacheKey == "" {
		t.Fatalf("result carries no cache key")
	}
}
