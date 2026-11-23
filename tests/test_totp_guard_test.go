package task

import (
	"bytes"
	"sync"
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-09"
)

func testConfig() taskpkg.Config {
	return taskpkg.Config{
		Period:        30 * time.Second,
		Digits:        6,
		SkewSteps:     1,
		MaxFailures:   3,
		KeyGrace:      time.Hour,
		EncryptionKey: []byte("totp-at-rest-key"),
	}
}

func addKey(t *testing.T, g *taskpkg.TotpGuard, tenantID, keyID string, secret []byte, alg string, version uint16) {
	t.Helper()
	if err := g.AddKey(taskpkg.KeyRecord{
		TenantID:  tenantID,
		KeyID:     keyID,
		Secret:    secret,
		Algorithm: alg,
		Version:   version,
	}); err != nil {
		t.Fatalf("AddKey(%s/%s): %v", tenantID, keyID, err)
	}
}

// A code from the previous step must be accepted inside the tolerance window.
func TestAdjacentStepAcceptedWithinWindow(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	prev, err := g.Code("tenant-a", "key-1", now.Add(-30*time.Second))
	if err != nil {
		t.Fatalf("Code previous step: %v", err)
	}
	res, err := g.Verify("tenant-a", "key-1", "user-1", prev, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.OK {
		t.Fatalf("previous-step code rejected inside the window: %s", res.Reason)
	}
	if res.DriftSteps != -1 {
		t.Fatalf("drift not reported: %d", res.DriftSteps)
	}
}

// A code from far outside the window must be rejected.
func TestOutsideWindowRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	current, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code current: %v", err)
	}
	if res, err := g.Verify("tenant-a", "key-1", "user-1", current, now); err != nil || !res.OK {
		t.Fatalf("current code must verify: ok=%v err=%v", res.OK, err)
	}
	far, err := g.Code("tenant-a", "key-1", now.Add(90*time.Second))
	if err != nil {
		t.Fatalf("Code far step: %v", err)
	}
	res, err := g.Verify("tenant-a", "key-1", "user-1", far, now)
	if err != nil {
		t.Fatalf("Verify far: %v", err)
	}
	if res.OK {
		t.Fatalf("code three steps away was accepted")
	}
}

// The same code must not be usable twice inside its window.
func TestSameCodeReplayRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	code, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if res, err := g.Verify("tenant-a", "key-1", "user-1", code, now); err != nil || !res.OK {
		t.Fatalf("first verification failed: ok=%v err=%v", res.OK, err)
	}
	replay, err := g.Verify("tenant-a", "key-1", "user-1", code, now)
	if err != nil {
		t.Fatalf("Verify replay: %v", err)
	}
	if replay.OK {
		t.Fatalf("the same code was accepted twice")
	}
	if replay.Reason == "" {
		t.Fatalf("rejected replay did not carry a reason")
	}
}

// A code for the next step must be a fresh, acceptable code.
func TestNextStepCodeAccepted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	current, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code current: %v", err)
	}
	if res, err := g.Verify("tenant-a", "key-1", "user-1", current, now); err != nil || !res.OK {
		t.Fatalf("current code rejected: ok=%v err=%v", res.OK, err)
	}
	next, err := g.Code("tenant-a", "key-1", now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Code next: %v", err)
	}
	res, err := g.Verify("tenant-a", "key-1", "user-1", next, now)
	if err != nil {
		t.Fatalf("Verify next: %v", err)
	}
	if !res.OK {
		t.Fatalf("next-step code rejected after the current one was used: %s", res.Reason)
	}
}

// Code comparison must not be a plain string comparison.
func TestConstantTimeComparison(t *testing.T) {
	if !taskpkg.ConstantTimeEqual("123456", "123456") {
		t.Fatalf("equal codes compared unequal")
	}
	if taskpkg.ConstantTimeEqual("123456", "123457") {
		t.Fatalf("different codes compared equal")
	}
	if taskpkg.ConstantTimeEqual("12345", "123456") {
		t.Fatalf("codes of different length compared equal")
	}
}

// A code produced just before a rotation must still verify while the previous
// key is inside its grace period.
func TestPreviousKeyAllowedDuringGrace(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-old"), "SHA1", 1)
	oldCode, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if err := g.RotateKey("tenant-a", "key-1", []byte("secret-new"), "SHA1", now.Add(time.Second)); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	res, err := g.Verify("tenant-a", "key-1", "user-1", oldCode, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Verify after rotation: %v", err)
	}
	if !res.OK {
		t.Fatalf("in-flight code failed right after key rotation: %s", res.Reason)
	}
}

// Once the grace period is over the previous key must be rejected.
func TestOldKeyRejectedAfterGrace(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := testConfig()
	cfg.SkewSteps = 10
	cfg.KeyGrace = 5 * time.Second
	g := taskpkg.NewTotpGuard(cfg)
	addKey(t, g, "tenant-a", "key-1", []byte("secret-old"), "SHA1", 1)
	oldCode, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if err := g.RotateKey("tenant-a", "key-1", []byte("secret-new"), "SHA1", now.Add(time.Second)); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	res, err := g.Verify("tenant-a", "key-1", "user-1", oldCode, now.Add(60*time.Second))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.OK {
		t.Fatalf("code of a rotated-away key was accepted after the grace period")
	}
}

// Two concurrent submissions of one code must yield exactly one success.
func TestConcurrentVerifySingleSuccess(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	code, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	var wg sync.WaitGroup
	results := make([]taskpkg.VerifyResult, 2)
	errs := make([]error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = g.Verify("tenant-a", "key-1", "user-1", code, now)
		}(i)
	}
	wg.Wait()
	successes := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("Verify[%d]: %v", i, errs[i])
		}
		if results[i].OK {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("want exactly one concurrent success, got %d", successes)
	}
}

// Drift beyond one step must be reported so the service can warn.
func TestDriftAlarmRaised(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := testConfig()
	cfg.SkewSteps = 3
	g := taskpkg.NewTotpGuard(cfg)
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	shifted, err := g.Code("tenant-a", "key-1", now.Add(60*time.Second))
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	res, err := g.Verify("tenant-a", "key-1", "user-1", shifted, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.OK {
		t.Fatalf("code inside the wider window rejected: %s", res.Reason)
	}
	if res.DriftSteps != 2 {
		t.Fatalf("drift of two steps was not reported: %d", res.DriftSteps)
	}
}

// Backup codes are single use.
func TestBackupCodeSingleUse(t *testing.T) {
	g := taskpkg.NewTotpGuard(testConfig())
	if err := g.AddBackupCodes("tenant-a", "user-1", []string{"backup-a", "backup-b"}); err != nil {
		t.Fatalf("AddBackupCodes: %v", err)
	}
	ok, err := g.VerifyBackupCode("tenant-a", "user-1", "backup-a")
	if err != nil {
		t.Fatalf("VerifyBackupCode first: %v", err)
	}
	if !ok {
		t.Fatalf("valid backup code rejected")
	}
	again, err := g.VerifyBackupCode("tenant-a", "user-1", "backup-a")
	if err != nil {
		t.Fatalf("VerifyBackupCode second: %v", err)
	}
	if again {
		t.Fatalf("backup code was accepted twice")
	}
	other, err := g.VerifyBackupCode("tenant-a", "user-1", "backup-b")
	if err != nil {
		t.Fatalf("VerifyBackupCode other: %v", err)
	}
	if !other {
		t.Fatalf("second backup code rejected")
	}
}

// Secrets must not be stored in plaintext.
func TestSecretEncryptedAtRest(t *testing.T) {
	g := taskpkg.NewTotpGuard(testConfig())
	secret := []byte("plaintext-totp-secret-value")
	addKey(t, g, "tenant-a", "key-1", secret, "SHA1", 1)
	atRest, err := g.SecretAtRest("tenant-a", "key-1")
	if err != nil {
		t.Fatalf("SecretAtRest: %v", err)
	}
	if len(atRest) == 0 {
		t.Fatalf("stored secret is empty")
	}
	if bytes.Contains(atRest, secret) {
		t.Fatalf("secret is stored in plaintext")
	}
}

// The hash algorithm must be versioned so SHA1 and SHA256 keys coexist.
func TestAlgorithmVersioned(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-sha1", []byte("shared-secret"), "SHA1", 1)
	addKey(t, g, "tenant-a", "key-sha256", []byte("shared-secret"), "SHA256", 2)
	codeSHA1, err := g.Code("tenant-a", "key-sha1", now)
	if err != nil {
		t.Fatalf("Code SHA1: %v", err)
	}
	codeSHA256, err := g.Code("tenant-a", "key-sha256", now)
	if err != nil {
		t.Fatalf("Code SHA256: %v", err)
	}
	if codeSHA1 == codeSHA256 {
		t.Fatalf("SHA1 and SHA256 produced the same code")
	}
	if res, err := g.Verify("tenant-a", "key-sha1", "user-1", codeSHA1, now); err != nil || !res.OK {
		t.Fatalf("SHA1 code rejected: ok=%v err=%v", res.OK, err)
	}
	if res, err := g.Verify("tenant-a", "key-sha256", "user-2", codeSHA256, now); err != nil || !res.OK {
		t.Fatalf("SHA256 code rejected: ok=%v err=%v", res.OK, err)
	}
}

// Repeated failures must lock the account until it is unlocked.
func TestFailureRateLimitLockout(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	for i := 0; i < 3; i++ {
		if _, err := g.Verify("tenant-a", "key-1", "user-1", "NOTACODE", now); err != nil {
			t.Fatalf("Verify wrong code %d: %v", i, err)
		}
	}
	count, err := g.Failures("tenant-a", "user-1")
	if err != nil {
		t.Fatalf("Failures: %v", err)
	}
	if count != 3 {
		t.Fatalf("failure counter mismatch: %d", count)
	}
	code, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	locked, err := g.Verify("tenant-a", "key-1", "user-1", code, now)
	if err != nil {
		t.Fatalf("Verify while locked: %v", err)
	}
	if locked.OK || !locked.Locked {
		t.Fatalf("account was not locked after repeated failures: %+v", locked)
	}
	if err := g.Unlock("tenant-a", "user-1"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	res, err := g.Verify("tenant-a", "key-1", "user-1", code, now)
	if err != nil {
		t.Fatalf("Verify after unlock: %v", err)
	}
	if !res.OK {
		t.Fatalf("correct code rejected after unlock: %s", res.Reason)
	}
}

// Key ids are scoped per tenant; the same id in another tenant is a different
// secret.
func TestTenantScopedKeyIDs(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	addKey(t, g, "tenant-b", "key-1", []byte("secret-b"), "SHA1", 1)
	codeA, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code tenant a: %v", err)
	}
	codeB, err := g.Code("tenant-b", "key-1", now)
	if err != nil {
		t.Fatalf("Code tenant b: %v", err)
	}
	if codeA == codeB {
		t.Fatalf("same key id in two tenants produced the same secret")
	}
	cross, err := g.Verify("tenant-b", "key-1", "user-1", codeA, now)
	if err != nil {
		t.Fatalf("Verify cross tenant: %v", err)
	}
	if cross.OK {
		t.Fatalf("code of tenant-a verified against tenant-b's key")
	}
}

// Used codes must be remembered across a restart.
func TestUsedCodeCachePersists(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewTotpGuard(testConfig())
	addKey(t, g, "tenant-a", "key-1", []byte("secret-a"), "SHA1", 1)
	code, err := g.Code("tenant-a", "key-1", now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if res, err := g.Verify("tenant-a", "key-1", "user-1", code, now); err != nil || !res.OK {
		t.Fatalf("first verification failed: ok=%v err=%v", res.OK, err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored := taskpkg.NewTotpGuardFromSnapshot(testConfig(), snap)
	res, err := restored.Verify("tenant-a", "key-1", "user-1", code, now)
	if err != nil {
		t.Fatalf("Verify after restart: %v", err)
	}
	if res.OK {
		t.Fatalf("used code accepted again after restart")
	}
}
