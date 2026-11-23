package task

import (
	"sync"
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-06"
)

func testConfig() taskpkg.Config {
	return taskpkg.Config{
		TrustDomain:           "example.org",
		RenewBefore:           10 * time.Second,
		BundleRefreshInterval: 10 * time.Second,
	}
}

func newSVID(serial, trustDomain string, notAfter time.Time) taskpkg.SVID {
	return taskpkg.SVID{
		ID:           "spiffe://" + trustDomain + "/ns/default/sa/api",
		Serial:       serial,
		TrustDomain:  trustDomain,
		NotAfter:     notAfter,
		Chain:        []string{"intermediate-" + serial},
		PrivateKeyID: "key-" + serial,
	}
}

// While a rotation is in progress the old SVID must remain usable; it may not
// be deleted before the new one is mounted.
func TestOldSVIDValidUntilNewLoaded(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	old := newSVID("serial-old", "example.org", now.Add(time.Hour))
	if err := g.LoadSVID(old); err != nil {
		t.Fatalf("LoadSVID old: %v", err)
	}
	if _, err := g.BeginRotation(newSVID("serial-new", "example.org", now.Add(2*time.Hour))); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	active, err := g.ActiveSVID()
	if err != nil {
		t.Fatalf("ActiveSVID during rotation: %v", err)
	}
	if active.Serial != "serial-old" {
		t.Fatalf("old SVID was dropped before the new one was committed: %q", active.Serial)
	}
}

// After the commit the new SVID must be the active one.
func TestNewSVIDActiveAfterCommit(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	old := newSVID("serial-old", "example.org", now.Add(time.Hour))
	if err := g.LoadSVID(old); err != nil {
		t.Fatalf("LoadSVID old: %v", err)
	}
	id, err := g.BeginRotation(newSVID("serial-new", "example.org", now.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if err := g.CommitRotation(id); err != nil {
		t.Fatalf("CommitRotation: %v", err)
	}
	active, err := g.ActiveSVID()
	if err != nil {
		t.Fatalf("ActiveSVID: %v", err)
	}
	if active.Serial != "serial-new" {
		t.Fatalf("rotation did not activate the new SVID: %q", active.Serial)
	}
}

// Bundle updates must be atomic: a concurrent reader may see the old bundle or
// the new one, never a mixture.
func TestBundleUpdateAtomic(t *testing.T) {
	g := taskpkg.NewSvidRotator(testConfig())
	if err := g.UpdateBundle(taskpkg.Bundle{
		TrustDomain: "example.org",
		Roots:       []string{"root-1", "root-2"},
		Version:     1,
	}); err != nil {
		t.Fatalf("UpdateBundle initial: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			snap, err := g.BundleSnapshot()
			if err != nil {
				t.Errorf("BundleSnapshot: %v", err)
				return
			}
			if len(snap.Roots) != 2 {
				t.Errorf("reader observed a torn bundle: %v", snap.Roots)
				return
			}
		}
	}()
	if err := g.UpdateBundle(taskpkg.Bundle{
		TrustDomain: "example.org",
		Roots:       []string{"root-3", "root-4"},
		Version:     2,
	}); err != nil {
		t.Fatalf("UpdateBundle replacement: %v", err)
	}
	wg.Wait()
}

// A bundle endpoint that returns only part of the roots must not wipe the good
// cached roots.
func TestPartialBundleMergedNotReplaced(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	if err := g.UpdateBundle(taskpkg.Bundle{
		TrustDomain: "example.org",
		Roots:       []string{"root-1", "root-2"},
		Version:     1,
	}); err != nil {
		t.Fatalf("UpdateBundle: %v", err)
	}
	if err := g.RefreshBundle(taskpkg.Bundle{
		TrustDomain: "example.org",
		Roots:       []string{"root-1"},
		Version:     2,
	}, now); err != nil {
		t.Fatalf("RefreshBundle: %v", err)
	}
	snap, err := g.BundleSnapshot()
	if err != nil {
		t.Fatalf("BundleSnapshot: %v", err)
	}
	has := map[string]bool{}
	for _, r := range snap.Roots {
		has[r] = true
	}
	if !has["root-1"] || !has["root-2"] {
		t.Fatalf("partial bundle refresh replaced cached roots: %v", snap.Roots)
	}
}

// Two rotations racing for the same workload must not produce two private keys.
func TestConcurrentRotationSingleKey(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	if err := g.LoadSVID(newSVID("serial-old", "example.org", now.Add(time.Hour))); err != nil {
		t.Fatalf("LoadSVID: %v", err)
	}
	var wg sync.WaitGroup
	ids := make([]uint32, 2)
	errs := make([]error, 2)
	candidates := []taskpkg.SVID{
		newSVID("serial-new-a", "example.org", now.Add(2*time.Hour)),
		newSVID("serial-new-b", "example.org", now.Add(2*time.Hour)),
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = g.BeginRotation(candidates[i])
		}(i)
	}
	wg.Wait()
	winners := 0
	winnerID := uint32(0)
	for i := 0; i < 2; i++ {
		if errs[i] == nil {
			winners++
			winnerID = ids[i]
		}
	}
	if winners != 1 {
		t.Fatalf("want exactly one winning rotation, got %d", winners)
	}
	if err := g.CommitRotation(winnerID); err != nil {
		t.Fatalf("CommitRotation: %v", err)
	}
	svids, err := g.SVIDs()
	if err != nil {
		t.Fatalf("SVIDs: %v", err)
	}
	keys := map[string]bool{}
	for _, s := range svids {
		keys[s.PrivateKeyID] = true
	}
	if len(keys) != len(svids) {
		t.Fatalf("concurrent rotation produced duplicate private keys: %v", svids)
	}
	if len(svids) != 2 {
		t.Fatalf("want old and new SVID, got %d", len(svids))
	}
}

// A second rotation while one is in progress must be rejected, and the losing
// candidate must leave no trace.
func TestParallelRotationRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	if err := g.LoadSVID(newSVID("serial-old", "example.org", now.Add(time.Hour))); err != nil {
		t.Fatalf("LoadSVID: %v", err)
	}
	id, err := g.BeginRotation(newSVID("serial-new-a", "example.org", now.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("BeginRotation first: %v", err)
	}
	if _, err := g.BeginRotation(newSVID("serial-new-b", "example.org", now.Add(2*time.Hour))); err == nil {
		t.Fatalf("second rotation started while one was already in progress")
	}
	if err := g.CommitRotation(id); err != nil {
		t.Fatalf("CommitRotation: %v", err)
	}
	svids, err := g.SVIDs()
	if err != nil {
		t.Fatalf("SVIDs: %v", err)
	}
	for _, s := range svids {
		if s.Serial == "serial-new-b" {
			t.Fatalf("rejected rotation candidate was installed anyway")
		}
	}
}

// Renewal must start before the SVID expires, not after.
func TestRenewBeforeExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	if err := g.LoadSVID(newSVID("serial-soon", "example.org", now.Add(5*time.Second))); err != nil {
		t.Fatalf("LoadSVID: %v", err)
	}
	need, err := g.NeedsRenewal(now)
	if err != nil {
		t.Fatalf("NeedsRenewal: %v", err)
	}
	if !need {
		t.Fatalf("SVID expiring inside RenewBefore was not flagged for renewal")
	}
}

// SVIDs of a different trust domain must not be accepted.
func TestTrustDomainIsolation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	if err := g.LoadSVID(newSVID("serial-own", "example.org", now.Add(time.Hour))); err != nil {
		t.Fatalf("LoadSVID own domain: %v", err)
	}
	foreign := newSVID("serial-foreign", "other.org", now.Add(time.Hour))
	if err := g.LoadSVID(foreign); err == nil {
		t.Fatalf("SVID from a foreign trust domain was loaded")
	}
}

// Tokens issued under an SVID that has been rotated away must be revocable.
func TestRotatedTokenRevocable(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	old := newSVID("serial-old", "example.org", now.Add(time.Hour))
	if err := g.LoadSVID(old); err != nil {
		t.Fatalf("LoadSVID: %v", err)
	}
	token, err := g.IssueToken(old.ID, now)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	id, err := g.BeginRotation(newSVID("serial-new", "example.org", now.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if err := g.CommitRotation(id); err != nil {
		t.Fatalf("CommitRotation: %v", err)
	}
	if err := g.RevokeTokensForSVID(old.ID); err != nil {
		t.Fatalf("RevokeTokensForSVID: %v", err)
	}
	valid, err := g.TokenValid(token, now)
	if err != nil {
		t.Fatalf("TokenValid: %v", err)
	}
	if valid {
		t.Fatalf("token of the rotated-away SVID is still accepted after revocation")
	}
}

// A chain must only be accepted for a SPIFFE ID inside the configured trust
// domain.
func TestSPIFFEPrefixValidated(t *testing.T) {
	g := taskpkg.NewSvidRotator(testConfig())
	ok, err := g.VerifyPeerChain("spiffe://example.org/ns/default/sa/api", []string{"intermediate-serial"})
	if err != nil {
		t.Fatalf("VerifyPeerChain own domain: %v", err)
	}
	if !ok {
		t.Fatalf("valid SPIFFE ID of the own trust domain was rejected")
	}
	bad, err := g.VerifyPeerChain("spiffe://evil.org/ns/default/sa/api", []string{"intermediate-serial"})
	if err != nil {
		t.Fatalf("VerifyPeerChain foreign domain: %v", err)
	}
	if bad {
		t.Fatalf("SPIFFE ID from a foreign trust domain was accepted")
	}
}

// After a restart the sidecar must only become ready once the SVIDs are
// loaded, not before.
func TestRestoreLoadsCertsBeforeReady(t *testing.T) {
	now := time.Unix(1700000000, 0)
	source := taskpkg.NewSvidRotator(testConfig())
	if err := source.LoadSVID(newSVID("serial-old", "example.org", now.Add(time.Hour))); err != nil {
		t.Fatalf("LoadSVID: %v", err)
	}
	state, err := source.ExportState()
	if err != nil {
		t.Fatalf("ExportState: %v", err)
	}
	restored := taskpkg.NewSvidRotator(testConfig())
	ready, err := restored.Ready()
	if err != nil {
		t.Fatalf("Ready before restore: %v", err)
	}
	if ready {
		t.Fatalf("sidecar reported ready before loading certificates")
	}
	if err := restored.Restore(state); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	ready, err = restored.Ready()
	if err != nil {
		t.Fatalf("Ready after restore: %v", err)
	}
	if !ready {
		t.Fatalf("sidecar did not become ready after restoring certificates")
	}
	active, err := restored.ActiveSVID()
	if err != nil {
		t.Fatalf("ActiveSVID: %v", err)
	}
	if active.Serial != "serial-old" {
		t.Fatalf("restored SVID mismatch: %q", active.Serial)
	}
}

// A failed rotation must roll back to the old SVID instead of leaving the
// workload on a broken certificate.
func TestRotationFailureRollsBack(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	old := newSVID("serial-old", "example.org", now.Add(time.Hour))
	if err := g.LoadSVID(old); err != nil {
		t.Fatalf("LoadSVID: %v", err)
	}
	id, err := g.BeginRotation(newSVID("serial-new", "example.org", now.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if err := g.RollbackRotation(id); err != nil {
		t.Fatalf("RollbackRotation: %v", err)
	}
	active, err := g.ActiveSVID()
	if err != nil {
		t.Fatalf("ActiveSVID: %v", err)
	}
	if active.Serial != "serial-old" {
		t.Fatalf("rollback did not restore the old SVID: %q", active.Serial)
	}
	svids, err := g.SVIDs()
	if err != nil {
		t.Fatalf("SVIDs: %v", err)
	}
	if len(svids) != 1 {
		t.Fatalf("rolled-back rotation left extra SVIDs: %v", svids)
	}
}

// Bundle refreshes must be rate limited so the upstream endpoint is not
// hammered.
func TestBundleRefreshRateLimited(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	bundleA := taskpkg.Bundle{TrustDomain: "example.org", Roots: []string{"root-1"}, Version: 1}
	if err := g.RefreshBundle(bundleA, now); err != nil {
		t.Fatalf("first RefreshBundle: %v", err)
	}
	if err := g.RefreshBundle(bundleA, now.Add(time.Second)); err == nil {
		t.Fatalf("bundle refresh inside the interval was not rate limited")
	}
}

// Replicas that loaded the same identity must present the same fingerprint.
func TestReplicasShareIdentity(t *testing.T) {
	now := time.Unix(1700000000, 0)
	svid := newSVID("serial-shared", "example.org", now.Add(time.Hour))
	a := taskpkg.NewSvidRotator(testConfig())
	b := taskpkg.NewSvidRotator(testConfig())
	if err := a.LoadSVID(svid); err != nil {
		t.Fatalf("LoadSVID replica a: %v", err)
	}
	if err := b.LoadSVID(svid); err != nil {
		t.Fatalf("LoadSVID replica b: %v", err)
	}
	fa, err := a.ReplicaFingerprint()
	if err != nil {
		t.Fatalf("ReplicaFingerprint a: %v", err)
	}
	fb, err := b.ReplicaFingerprint()
	if err != nil {
		t.Fatalf("ReplicaFingerprint b: %v", err)
	}
	if fa == "" || fa != fb {
		t.Fatalf("replicas present different identities: %q vs %q", fa, fb)
	}
}

// An expired SVID must never be installed.
func TestExpiredSVIDRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := taskpkg.NewSvidRotator(testConfig())
	if err := g.LoadSVID(newSVID("serial-valid", "example.org", now.Add(time.Hour))); err != nil {
		t.Fatalf("LoadSVID valid: %v", err)
	}
	expired := newSVID("serial-expired", "example.org", now.Add(-time.Second))
	if err := g.LoadSVID(expired); err == nil {
		t.Fatalf("expired SVID was installed")
	}
}
