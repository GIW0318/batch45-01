package task

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-08"
)

func testConfig() taskpkg.Config {
	return taskpkg.Config{
		RPID:         "login.example.com",
		Origin:       "https://login.example.com",
		RequireUV:    true,
		TrustedRoots: []string{"root-a"},
		ChallengeTTL: 5 * time.Minute,
	}
}

func rpIDHash() string {
	sum := sha256.Sum256([]byte("login.example.com"))
	return hex.EncodeToString(sum[:])
}

func baseReg(userID, credID, handle string) taskpkg.Registration {
	return taskpkg.Registration{
		TenantID:     "tenant-a",
		UserID:       userID,
		Username:     userID,
		CredentialID: credID,
		UserHandle:   handle,
		PublicKey:    []byte("public-key-" + credID),
		Origin:       "https://login.example.com",
		RPIDHash:     rpIDHash(),
		UV:           true,
		Attestation: taskpkg.Attestation{
			Format: "packed",
			Chain:  []string{"root-a"},
			Root:   "root-a",
		},
	}
}

func attemptRegister(g *taskpkg.CredentialStore, r taskpkg.Registration) (taskpkg.Credential, error) {
	challenge, err := g.BeginChallenge(r.UserID)
	if err != nil {
		return taskpkg.Credential{}, err
	}
	r.Challenge = challenge
	return g.FinishRegistration(r)
}

func mustRegister(t *testing.T, g *taskpkg.CredentialStore, r taskpkg.Registration) taskpkg.Credential {
	t.Helper()
	cred, err := attemptRegister(g, r)
	if err != nil {
		t.Fatalf("FinishRegistration(%s): %v", r.CredentialID, err)
	}
	if cred.CredentialID != r.CredentialID {
		t.Fatalf("stored credential mismatch: %q", cred.CredentialID)
	}
	return cred
}

// A credential id must be unique across tenants, not just inside one tenant.
func TestCredentialIDGloballyUniqueAcrossTenants(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	otherTenant := baseReg("user-2", "cred-2", "handle-2")
	otherTenant.TenantID = "tenant-b"
	mustRegister(t, g, otherTenant)
	duplicate := baseReg("user-3", "cred-1", "handle-3")
	duplicate.TenantID = "tenant-b"
	if _, err := attemptRegister(g, duplicate); err == nil {
		t.Fatalf("credential id collided across tenants")
	}
}

// The same credential id must not be registered twice inside one tenant.
func TestDuplicateCredentialWithinTenantRejected(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	duplicate := baseReg("user-2", "cred-1", "handle-2")
	if _, err := attemptRegister(g, duplicate); err == nil {
		t.Fatalf("duplicate credential id was registered twice")
	}
	if _, err := attemptRegister(g, baseReg("user-3", "cred-3", "handle-3")); err != nil {
		t.Fatalf("store stopped accepting valid registrations: %v", err)
	}
}

// A resident key user handle belongs to one account only.
func TestUserHandleBoundToAccount(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	second := baseReg("user-1", "cred-2", "handle-1")
	if _, err := attemptRegister(g, second); err != nil {
		t.Fatalf("second credential of the same account was rejected: %v", err)
	}
	foreign := baseReg("user-2", "cred-3", "handle-1")
	if _, err := attemptRegister(g, foreign); err == nil {
		t.Fatalf("user handle was rebound to a different account")
	}
}

// Self attestation must not be accepted when the policy forbids it.
func TestSelfSignedAttestationRejected(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	bad := baseReg("user-1", "cred-1", "handle-1")
	bad.Attestation.SelfSigned = true
	if _, err := attemptRegister(g, bad); err == nil {
		t.Fatalf("self-signed attestation was accepted")
	}
	if _, err := attemptRegister(g, baseReg("user-1", "cred-2", "handle-1")); err != nil {
		t.Fatalf("valid attestation rejected: %v", err)
	}
}

// The attestation chain must terminate in a trusted root.
func TestAttestationChainValidatedAgainstRoots(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	bad := baseReg("user-1", "cred-1", "handle-1")
	bad.Attestation.Root = "rogue-root"
	bad.Attestation.Chain = []string{"rogue-root"}
	if _, err := attemptRegister(g, bad); err == nil {
		t.Fatalf("attestation with an untrusted root was accepted")
	}
	if _, err := attemptRegister(g, baseReg("user-2", "cred-2", "handle-2")); err != nil {
		t.Fatalf("attestation with a trusted root rejected: %v", err)
	}
}

// User verification must be required when configured.
func TestUVFlagRequired(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	noUV := baseReg("user-1", "cred-1", "handle-1")
	noUV.UV = false
	if _, err := attemptRegister(g, noUV); err == nil {
		t.Fatalf("registration without UV was accepted")
	}
	if _, err := attemptRegister(g, baseReg("user-2", "cred-2", "handle-2")); err != nil {
		t.Fatalf("registration with UV rejected: %v", err)
	}
}

// Backup eligibility flags must be recorded with the credential.
func TestBackupEligibleFlagTracked(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	reg := baseReg("user-1", "cred-1", "handle-1")
	reg.BE = true
	reg.BS = true
	mustRegister(t, g, reg)
	creds, err := g.Discover("tenant-a", "user-1")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("want one credential, got %d", len(creds))
	}
	if !creds[0].BE || !creds[0].BS {
		t.Fatalf("backup flags were dropped: BE=%v BS=%v", creds[0].BE, creds[0].BS)
	}
}

// A challenge is burned when the RP ID hash does not match; it may not be
// retried with the correct hash.
func TestChallengeInvalidatedAfterRPIDMismatch(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-0", "cred-0", "handle-0"))

	reg := baseReg("user-1", "cred-1", "handle-1")
	challenge, err := g.BeginChallenge(reg.UserID)
	if err != nil {
		t.Fatalf("BeginChallenge: %v", err)
	}
	reg.Challenge = challenge
	reg.RPIDHash = "deadbeef"
	if _, err := g.FinishRegistration(reg); err == nil {
		t.Fatalf("registration with a wrong RP ID hash was accepted")
	}
	reg.RPIDHash = rpIDHash()
	if _, err := g.FinishRegistration(reg); err == nil {
		t.Fatalf("challenge was retried after an RP ID mismatch")
	}
}

// Concurrent registrations of one credential id must yield a single record.
func TestConcurrentRegistrationSingleRecord(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = attemptRegister(g, baseReg("user-1", "cred-race", "handle-1"))
	}()
	go func() {
		defer wg.Done()
		_, results[1] = attemptRegister(g, baseReg("user-2", "cred-race", "handle-2"))
	}()
	wg.Wait()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("want exactly one successful registration, got %d", successes)
	}
	first, err := g.Discover("tenant-a", "user-1")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	second, err := g.Discover("tenant-a", "user-2")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(first)+len(second) != 1 {
		t.Fatalf("credential was stored for more than one account: %d + %d", len(first), len(second))
	}
}

// A non-increasing signature counter must be rejected.
func TestSignatureCounterEqualRejected(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	first, err := g.Authenticate("tenant-a", "cred-1", "https://login.example.com", rpIDHash(), true, 5)
	if err != nil {
		t.Fatalf("Authenticate first: %v", err)
	}
	if !first.OK {
		t.Fatalf("valid assertion rejected: %s", first.Reason)
	}
	replay, err := g.Authenticate("tenant-a", "cred-1", "https://login.example.com", rpIDHash(), true, 5)
	if err != nil {
		t.Fatalf("Authenticate equal counter: %v", err)
	}
	if replay.OK {
		t.Fatalf("assertion with a repeated signature counter was accepted")
	}
}

// An increasing signature counter is fine.
func TestSignatureCounterIncreaseAccepted(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	if res, err := g.Authenticate("tenant-a", "cred-1", "https://login.example.com", rpIDHash(), true, 5); err != nil || !res.OK {
		t.Fatalf("first assertion: ok=%v err=%v", res.OK, err)
	}
	res, err := g.Authenticate("tenant-a", "cred-1", "https://login.example.com", rpIDHash(), true, 6)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !res.OK {
		t.Fatalf("increasing counter rejected: %s", res.Reason)
	}
}

// The origin must match exactly, not by domain suffix.
func TestOriginExactMatch(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	good, err := g.Authenticate("tenant-a", "cred-1", "https://login.example.com", rpIDHash(), true, 1)
	if err != nil || !good.OK {
		t.Fatalf("valid origin rejected: ok=%v err=%v", good.OK, err)
	}
	bad, err := g.Authenticate("tenant-a", "cred-1", "https://login.example.com.evil.com", rpIDHash(), true, 2)
	if err != nil {
		t.Fatalf("Authenticate suffix origin: %v", err)
	}
	if bad.OK {
		t.Fatalf("origin matched by domain suffix")
	}
}

// A frozen credential must not authenticate.
func TestFrozenCredentialRejected(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	if err := g.Freeze("tenant-a", "cred-1"); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	res, err := g.Authenticate("tenant-a", "cred-1", "https://login.example.com", rpIDHash(), true, 1)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if res.OK {
		t.Fatalf("frozen credential authenticated")
	}
}

// Public keys must survive a restart.
func TestStorePersistsAcrossRestart(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored := taskpkg.NewCredentialStoreFromSnapshot(testConfig(), snap)
	creds, err := restored.Discover("tenant-a", "user-1")
	if err != nil {
		t.Fatalf("Discover after restart: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("credential lost across restart: %d", len(creds))
	}
	res, err := restored.Authenticate("tenant-a", "cred-1", "https://login.example.com", rpIDHash(), true, 1)
	if err != nil {
		t.Fatalf("Authenticate after restart: %v", err)
	}
	if !res.OK {
		t.Fatalf("restored credential cannot authenticate: %s", res.Reason)
	}
}

// Discoverable credential listing must be scoped to one user of one tenant.
func TestDiscoverableCredentialsScopedToUser(t *testing.T) {
	g := taskpkg.NewCredentialStore(testConfig())
	mustRegister(t, g, baseReg("user-1", "cred-1", "handle-1"))
	mustRegister(t, g, baseReg("user-2", "cred-2", "handle-2"))
	otherTenant := baseReg("user-1", "cred-3", "handle-3")
	otherTenant.TenantID = "tenant-b"
	mustRegister(t, g, otherTenant)
	creds, err := g.Discover("tenant-a", "user-1")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("discover returned %d credentials for one user, want 1", len(creds))
	}
	if creds[0].CredentialID != "cred-1" {
		t.Fatalf("discover returned another account's credential: %q", creds[0].CredentialID)
	}
}
