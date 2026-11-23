package task

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-08"
)

var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func newQuotaGuard() *KeyringQuotaGuard {
	return NewKeyringQuotaGuard(QuotaOptions{
		MaxPerTenant:   5,
		MaxRoot:        10,
		MaxDescription: 64,
		MaxDepth:       2,
		RootTenant:     "root",
	})
}

func mustAdd(t *testing.T, g *KeyringQuotaGuard, tenant, desc string) *Key {
	t.Helper()
	k, err := g.AddKey(tenant, desc, fixedNow.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("AddKey(%s, %s) returned error: %v", tenant, desc, err)
	}
	return k
}

func mustUsage(t *testing.T, g *KeyringQuotaGuard, tenant string) Usage {
	t.Helper()
	u, err := g.Usage(tenant)
	if err != nil {
		t.Fatalf("Usage returned error: %v", err)
	}
	return u
}

func TestQuotaIsPerTenantNotPerProcess(t *testing.T) {
	g := newQuotaGuard()
	for i := 0; i < 5; i++ {
		mustAdd(t, g, "tenant-a", "key-"+strconv.Itoa(i))
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 5 {
		t.Fatalf("Usage = %d, want 5", u.Keys)
	}
	if _, err := g.AddKey("tenant-a", "overflow", fixedNow.Add(time.Hour)); err == nil {
		t.Fatalf("sixth key for tenant-a must be rejected")
	}
	mustAdd(t, g, "tenant-b", "other-tenant")
	if u := mustUsage(t, g, "tenant-b"); u.Keys != 1 {
		t.Fatalf("tenant-b usage = %d, want 1 (quota must be per tenant)", u.Keys)
	}
}

func TestSessionKeyringKeysAreCounted(t *testing.T) {
	g := newQuotaGuard()
	ring, err := g.CreateRing("tenant-a", "session", "")
	if err != nil {
		t.Fatalf("CreateRing returned error: %v", err)
	}
	key := mustAdd(t, g, "tenant-a", "login")
	if err := g.Link("tenant-a", key, ring); err != nil {
		t.Fatalf("Link returned error: %v", err)
	}
	u := mustUsage(t, g, "tenant-a")
	if u.Keys != 1 {
		t.Fatalf("key linked into the session keyring counted as %d, want 1", u.Keys)
	}
	used, _ := g.Capacity("tenant-a")
	if used != 1 {
		t.Fatalf("Capacity used = %d, want 1", used)
	}
}

func TestRevokedKeysFreeQuota(t *testing.T) {
	g := newQuotaGuard()
	var last *Key
	for i := 0; i < 5; i++ {
		last = mustAdd(t, g, "tenant-a", "key-"+strconv.Itoa(i))
	}
	if err := g.Revoke(last); err != nil {
		t.Fatalf("Revoke returned error: %v", err)
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 4 {
		t.Fatalf("usage after revoke = %d, want 4", u.Keys)
	}
	mustAdd(t, g, "tenant-a", "replacement")
}

func TestNegativeKeysDoNotCount(t *testing.T) {
	g := newQuotaGuard()
	key, err := g.AddNegativeKey("tenant-a", "miss")
	if err != nil {
		t.Fatalf("AddNegativeKey returned error: %v", err)
	}
	if key == nil {
		t.Fatalf("AddNegativeKey returned a nil key")
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 0 {
		t.Fatalf("negative key counted against quota: %d", u.Keys)
	}
	if n := g.TotalKeys(); n != 0 {
		t.Fatalf("TotalKeys = %d after a single negative key, want 0", n)
	}
}

func TestQuotaReleasedImmediatelyAfterDelete(t *testing.T) {
	g := newQuotaGuard()
	var victim *Key
	for i := 0; i < 5; i++ {
		k := mustAdd(t, g, "tenant-a", "key-"+strconv.Itoa(i))
		if i == 0 {
			victim = k
		}
	}
	if err := g.Delete(victim); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := g.AddKey("tenant-a", "fresh", fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("quota was not released immediately after delete: %v", err)
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 5 {
		t.Fatalf("usage = %d after delete and add, want 5", u.Keys)
	}
}

func TestDoubleReferenceNotDoubleCounted(t *testing.T) {
	g := newQuotaGuard()
	r1, err := g.CreateRing("tenant-a", "ring1", "")
	if err != nil {
		t.Fatalf("CreateRing returned error: %v", err)
	}
	r2, err := g.CreateRing("tenant-a", "ring2", "")
	if err != nil {
		t.Fatalf("CreateRing returned error: %v", err)
	}
	key := mustAdd(t, g, "tenant-a", "shared")
	if err := g.Link("tenant-a", key, r1); err != nil {
		t.Fatalf("Link returned error: %v", err)
	}
	if err := g.Link("tenant-a", key, r2); err != nil {
		t.Fatalf("Link returned error: %v", err)
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 1 {
		t.Fatalf("key referenced by two keyrings counted %d times, want 1", u.Keys)
	}
	if n := g.TotalKeys(); n != 1 {
		t.Fatalf("TotalKeys = %d, want 1", n)
	}
}

func TestRootTenantHasSeparateLimit(t *testing.T) {
	g := newQuotaGuard()
	for i := 0; i < 7; i++ {
		mustAdd(t, g, "root", "root-key-"+strconv.Itoa(i))
	}
	if u := mustUsage(t, g, "root"); u.Keys != 7 {
		t.Fatalf("root usage = %d, want 7", u.Keys)
	}
	if _, err := g.AddKey("root", "root-overflow", fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("root must use its own quota of 10: %v", err)
	}
}

func TestDescriptionLengthLimited(t *testing.T) {
	g := newQuotaGuard()
	if _, err := g.AddKey("tenant-a", strings.Repeat("x", 65), fixedNow.Add(time.Hour)); err == nil {
		t.Fatalf("description longer than the limit was accepted")
	}
	if _, err := g.AddKey("tenant-a", "short", fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("short description rejected: %v", err)
	}
}

func TestConcurrentCreateRespectsQuota(t *testing.T) {
	g := newQuotaGuard()
	var mu sync.Mutex
	successes := 0
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := g.AddKey("tenant-a", "c-"+strconv.Itoa(i), fixedNow.Add(time.Hour)); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 5 {
		t.Fatalf("%d concurrent creations succeeded, want exactly 5", successes)
	}
	if n := g.TotalKeys(); n != 5 {
		t.Fatalf("TotalKeys = %d after concurrent creation, want 5", n)
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 5 {
		t.Fatalf("Usage = %d, want 5", u.Keys)
	}
}

func TestNestingDepthLimited(t *testing.T) {
	g := newQuotaGuard()
	if _, err := g.CreateRing("tenant-a", "level1", ""); err != nil {
		t.Fatalf("first level rejected: %v", err)
	}
	if _, err := g.CreateRing("tenant-a", "level2", "level1"); err != nil {
		t.Fatalf("second level rejected: %v", err)
	}
	if _, err := g.CreateRing("tenant-a", "level3", "level2"); err == nil {
		t.Fatalf("nesting beyond MaxDepth must be rejected")
	}
}

func TestExpiredKeysReclaimed(t *testing.T) {
	g := newQuotaGuard()
	if _, err := g.AddKey("tenant-a", "short-lived", fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("AddKey returned error: %v", err)
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 1 {
		t.Fatalf("usage = %d, want 1", u.Keys)
	}
	n, err := g.Reclaim(fixedNow.Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("Reclaim returned error: %v", err)
	}
	if n != 1 {
		t.Fatalf("Reclaim returned %d, want 1", n)
	}
	if u := mustUsage(t, g, "tenant-a"); u.Keys != 0 {
		t.Fatalf("usage after reclaim = %d, want 0", u.Keys)
	}
}

func TestUsageVersionAdvances(t *testing.T) {
	g := newQuotaGuard()
	mustAdd(t, g, "tenant-a", "first")
	u1 := mustUsage(t, g, "tenant-a")
	mustAdd(t, g, "tenant-a", "second")
	u2 := mustUsage(t, g, "tenant-a")
	if u1.Keys != 1 || u2.Keys != 2 {
		t.Fatalf("usage keys %d then %d, want 1 then 2", u1.Keys, u2.Keys)
	}
	if u2.Version <= u1.Version {
		t.Fatalf("usage version did not advance (%d -> %d)", u1.Version, u2.Version)
	}
}

func TestSnapshotRestoreKeepsCounters(t *testing.T) {
	g := newQuotaGuard()
	for i := 0; i < 3; i++ {
		mustAdd(t, g, "tenant-a", "key-"+strconv.Itoa(i))
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	restored := newQuotaGuard()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if u := mustUsage(t, restored, "tenant-a"); u.Keys != 3 {
		t.Fatalf("restored usage = %d, want 3", u.Keys)
	}
	for i := 0; i < 2; i++ {
		mustAdd(t, restored, "tenant-a", "post-restore-"+strconv.Itoa(i))
	}
	if _, err := restored.AddKey("tenant-a", "overflow", fixedNow.Add(time.Hour)); err == nil {
		t.Fatalf("quota not enforced after restore")
	}
}

func TestLinkRejectsForeignTenant(t *testing.T) {
	g := newQuotaGuard()
	ring, err := g.CreateRing("tenant-b", "session", "")
	if err != nil {
		t.Fatalf("CreateRing returned error: %v", err)
	}
	key := mustAdd(t, g, "tenant-a", "own")
	if err := g.Link("tenant-a", key, ring); err == nil {
		t.Fatalf("linking a key into another tenant's keyring was allowed")
	}
	own, err := g.CreateRing("tenant-a", "session", "")
	if err != nil {
		t.Fatalf("CreateRing returned error: %v", err)
	}
	if err := g.Link("tenant-a", key, own); err != nil {
		t.Fatalf("linking into the own keyring rejected: %v", err)
	}
}

func TestRevokedKeyCannotBeLinked(t *testing.T) {
	g := newQuotaGuard()
	ring, err := g.CreateRing("tenant-a", "session", "")
	if err != nil {
		t.Fatalf("CreateRing returned error: %v", err)
	}
	key := mustAdd(t, g, "tenant-a", "temp")
	if err := g.Revoke(key); err != nil {
		t.Fatalf("Revoke returned error: %v", err)
	}
	if err := g.Link("tenant-a", key, ring); err == nil {
		t.Fatalf("revoked key was linked into a keyring")
	}
}
