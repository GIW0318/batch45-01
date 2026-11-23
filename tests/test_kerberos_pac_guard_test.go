package task

import (
	"sync"
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-10"
)

func testConfig() taskpkg.Config {
	return taskpkg.Config{
		MaxSkew:   5 * time.Minute,
		PACMaxAge: time.Hour,
		AllowedEnctypes: []string{
			"aes256-cts-hmac-sha1-96",
			"aes128-cts-hmac-sha256-128",
		},
	}
}

func basePAC(now time.Time) taskpkg.PAC {
	return taskpkg.PAC{
		LogonServer:     "dc01.domain-a.example",
		LogonDomain:     "DOMAIN-A",
		UserSID:         "domain-a/S-1-5-21-1-2-3-1001",
		GroupSIDs:       []string{"domain-a/S-1-5-21-1-2-3-512"},
		KDCChecksum:     "kdc-checksum-value",
		ServiceChecksum: "service-checksum-value",
		IssuedAt:        now.Add(-time.Minute),
		ExpiresAt:       now.Add(time.Hour),
	}
}

func baseTicket(now time.Time, id string) taskpkg.Ticket {
	return taskpkg.Ticket{
		ID:      id,
		SPN:     "HTTP/API.EXAMPLE.COM",
		Enctype: "aes256-cts-hmac-sha1-96",
		PAC:     basePAC(now),
	}
}

func baseAuth(nonce string, now time.Time) taskpkg.Authenticator {
	return taskpkg.Authenticator{
		ClientPrincipal: "alice@DOMAIN-A",
		Nonce:           nonce,
		Timestamp:       now,
	}
}

func baseSvc() taskpkg.ServicePrincipal {
	return taskpkg.ServicePrincipal{Name: "http/api.example.com"}
}

func newGuard(t *testing.T) *taskpkg.PacGuard {
	t.Helper()
	g := taskpkg.NewPacGuard(testConfig())
	if err := g.TrustDomain("domain-a"); err != nil {
		t.Fatalf("TrustDomain: %v", err)
	}
	return g
}

func mustValidate(t *testing.T, g *taskpkg.PacGuard, tk taskpkg.Ticket, a taskpkg.Authenticator, svc taskpkg.ServicePrincipal, now time.Time) taskpkg.ValidationResult {
	t.Helper()
	res, err := g.Validate(tk, a, svc, now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.OK {
		t.Fatalf("valid ticket rejected: %s", res.Reason)
	}
	return res
}

// The KDC checksum is mandatory.
func TestKDCChecksumRequired(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-ok"), baseAuth("nonce-1", now), baseSvc(), now)
	bad := baseTicket(now, "ticket-no-kdc")
	bad.PAC.KDCChecksum = ""
	res, err := g.Validate(bad, baseAuth("nonce-2", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("PAC without KDC checksum was accepted")
	}
}

// The service checksum must be verified too.
func TestServiceChecksumRequired(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-ok"), baseAuth("nonce-1", now), baseSvc(), now)
	bad := baseTicket(now, "ticket-no-svc")
	bad.PAC.ServiceChecksum = ""
	res, err := g.Validate(bad, baseAuth("nonce-2", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("PAC without service checksum was accepted")
	}
}

// Group SIDs of a trusted domain are accepted.
func TestTrustedDomainSIDAccepted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	ticket := baseTicket(now, "ticket-trusted")
	ticket.PAC.GroupSIDs = append(ticket.PAC.GroupSIDs, "domain-a/S-1-5-21-1-2-3-1105")
	res := mustValidate(t, g, ticket, baseAuth("nonce-1", now), baseSvc(), now)
	if !res.OK {
		t.Fatalf("trusted group SID rejected")
	}
}

// Group SIDs from an untrusted domain must reject the PAC.
func TestUntrustedDomainSIDRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-ok"), baseAuth("nonce-1", now), baseSvc(), now)
	bad := baseTicket(now, "ticket-untrusted-sid")
	bad.PAC.GroupSIDs = append(bad.PAC.GroupSIDs, "domain-evil/S-1-5-21-9-9-9-512")
	res, err := g.Validate(bad, baseAuth("nonce-2", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("group SID of an untrusted domain was accepted")
	}
}

// A referral path may not walk through an untrusted domain.
func TestReferralPathLimited(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	good := baseTicket(now, "ticket-referral-ok")
	good.ReferralPath = []string{"domain-a"}
	mustValidate(t, g, good, baseAuth("nonce-1", now), baseSvc(), now)
	bad := baseTicket(now, "ticket-referral-bad")
	bad.ReferralPath = []string{"domain-a", "domain-evil"}
	res, err := g.Validate(bad, baseAuth("nonce-2", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("referral through an untrusted domain was accepted")
	}
}

// An expired PAC must be rejected.
func TestExpiredPACRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-ok"), baseAuth("nonce-1", now), baseSvc(), now)
	expired := baseTicket(now, "ticket-expired")
	expired.PAC.ExpiresAt = now.Add(-time.Second)
	res, err := g.Validate(expired, baseAuth("nonce-2", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("expired PAC was accepted")
	}
}

// Concurrent validations must not overwrite each other's cache entries.
func TestConcurrentValidationConsistentCache(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	t1 := baseTicket(now, "ticket-a")
	t2 := baseTicket(now, "ticket-b")
	var wg sync.WaitGroup
	results := make([]taskpkg.ValidationResult, 2)
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], errs[0] = g.Validate(t1, baseAuth("nonce-a", now), baseSvc(), now)
	}()
	go func() {
		defer wg.Done()
		results[1], errs[1] = g.Validate(t2, baseAuth("nonce-b", now), baseSvc(), now)
	}()
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("Validate[%d]: %v", i, errs[i])
		}
		if !results[i].OK {
			t.Fatalf("concurrent validation[%d] failed: %s", i, results[i].Reason)
		}
	}
	for _, id := range []string{"ticket-a", "ticket-b"} {
		cached, hit, err := g.CachedResult(id)
		if err != nil {
			t.Fatalf("CachedResult(%s): %v", id, err)
		}
		if !hit || !cached.OK {
			t.Fatalf("cache entry for %s was lost: hit=%v result=%+v", id, hit, cached)
		}
	}
}

// Delegation must only be granted to services trusted for delegation.
func TestDelegationOnlyForTrustedService(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	delegating := baseTicket(now, "ticket-deleg")
	delegating.PAC.DelegationAllowed = true
	plain := baseSvc()
	plain.TrustedForDelegation = false
	res, err := g.Validate(delegating, baseAuth("nonce-1", now), plain, now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("untrusted service received delegation")
	}
	delegating2 := baseTicket(now, "ticket-deleg-ok")
	delegating2.PAC.DelegationAllowed = true
	trusted := baseSvc()
	trusted.TrustedForDelegation = true
	ok := mustValidate(t, g, delegating2, baseAuth("nonce-2", now), trusted, now)
	if !ok.Delegated {
		t.Fatalf("trusted service did not receive delegation")
	}
}

// Logon information in the PAC must be complete.
func TestLogonInfoMandatory(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-ok"), baseAuth("nonce-1", now), baseSvc(), now)
	noServer := baseTicket(now, "ticket-no-server")
	noServer.PAC.LogonServer = ""
	if res, err := g.Validate(noServer, baseAuth("nonce-2", now), baseSvc(), now); err != nil {
		t.Fatalf("Validate: %v", err)
	} else if res.OK {
		t.Fatalf("PAC without logon server was accepted")
	}
	noDomain := baseTicket(now, "ticket-no-domain")
	noDomain.PAC.LogonDomain = ""
	if res, err := g.Validate(noDomain, baseAuth("nonce-3", now), baseSvc(), now); err != nil {
		t.Fatalf("Validate: %v", err)
	} else if res.OK {
		t.Fatalf("PAC without logon domain was accepted")
	}
}

// RC4 tickets must be rejected.
func TestRC4Rejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-ok"), baseAuth("nonce-1", now), baseSvc(), now)
	rc4 := baseTicket(now, "ticket-rc4")
	rc4.Enctype = "rc4-hmac"
	res, err := g.Validate(rc4, baseAuth("nonce-2", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("RC4 ticket was accepted")
	}
}

// AES128 and AES256 tickets are both acceptable.
func TestAESAccepted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	aes256 := baseTicket(now, "ticket-aes256")
	mustValidate(t, g, aes256, baseAuth("nonce-1", now), baseSvc(), now)
	aes128 := baseTicket(now, "ticket-aes128")
	aes128.Enctype = "aes128-cts-hmac-sha256-128"
	mustValidate(t, g, aes128, baseAuth("nonce-2", now), baseSvc(), now)
}

// SPNs compare case-insensitively after normalization.
func TestSPNNormalizedCaseInsensitive(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	normalized, err := g.NormalizeSPN("HTTP/API.EXAMPLE.COM")
	if err != nil {
		t.Fatalf("NormalizeSPN: %v", err)
	}
	if normalized != "http/api.example.com" {
		t.Fatalf("SPN normalization wrong: %q", normalized)
	}
	mustValidate(t, g, baseTicket(now, "ticket-spn-ok"), baseAuth("nonce-1", now), baseSvc(), now)
	mismatch := baseTicket(now, "ticket-spn-bad")
	mismatch.SPN = "HTTP/OTHER.EXAMPLE.COM"
	otherSvc := baseSvc()
	otherSvc.Name = "http/other.example.com"
	if res, err := g.Validate(mismatch, baseAuth("nonce-2", now), baseSvc(), now); err != nil {
		t.Fatalf("Validate: %v", err)
	} else if res.OK {
		t.Fatalf("ticket for another SPN was accepted")
	}
}

// Replay protection is keyed by the authenticator nonce, not the ticket.
func TestReplayKeyedByAuthenticator(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	ticket := baseTicket(now, "ticket-reuse")
	mustValidate(t, g, ticket, baseAuth("nonce-1", now), baseSvc(), now)
	mustValidate(t, g, ticket, baseAuth("nonce-2", now), baseSvc(), now)
	other := baseTicket(now, "ticket-other")
	res, err := g.Validate(other, baseAuth("nonce-1", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("authenticator nonce was replayed with another ticket")
	}
}

// The authenticator timestamp must stay inside the clock skew.
func TestClockSkewLimited(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-ok"), baseAuth("nonce-1", now.Add(-time.Minute)), baseSvc(), now)
	stale := baseTicket(now, "ticket-stale")
	res, err := g.Validate(stale, baseAuth("nonce-2", now.Add(-10*time.Minute)), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("authenticator outside the clock skew was accepted")
	}
}

// The replay cache must survive a restart.
func TestReplayCachePersists(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newGuard(t)
	mustValidate(t, g, baseTicket(now, "ticket-1"), baseAuth("nonce-shared", now), baseSvc(), now)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored := taskpkg.NewPacGuardFromSnapshot(testConfig(), snap)
	if err := restored.TrustDomain("domain-a"); err != nil {
		t.Fatalf("TrustDomain after restore: %v", err)
	}
	res, err := restored.Validate(baseTicket(now, "ticket-2"), baseAuth("nonce-shared", now), baseSvc(), now)
	if err != nil {
		t.Fatalf("Validate after restart: %v", err)
	}
	if res.OK {
		t.Fatalf("authenticator nonce replay succeeded after restart")
	}
}
