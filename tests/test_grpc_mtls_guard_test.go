package task

import (
	"errors"
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-05"
)

func testConfig() taskpkg.Config {
	return taskpkg.Config{
		TenantDomainSuffix: ".example.com",
		MaxClockSkew:       5 * time.Second,
	}
}

func validCert(now time.Time) taskpkg.CertInfo {
	return taskpkg.CertInfo{
		Serial:               "SN-1001",
		Subject:              "CN=alice",
		SANs:                 []string{"alice@tenant-a.example.com"},
		NotBefore:            now.Add(-time.Hour),
		NotAfter:             now.Add(time.Hour),
		IntermediatesPresent: true,
		ChainComplete:        true,
		Fingerprint:          "fp-1",
	}
}

func freshGuard(t *testing.T) *taskpkg.MtlsGuard {
	t.Helper()
	g := taskpkg.NewMtlsGuard(testConfig())
	if err := g.LoadRevocationList(nil); err != nil {
		t.Fatalf("LoadRevocationList: %v", err)
	}
	return g
}

// A revoked client certificate must not reach the management interface.
func TestRevokedCertRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	if err := g.Revoke("SN-1001"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	res, err := g.Authorize(validCert(now), nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Allowed {
		t.Fatalf("revoked certificate was authorized")
	}
}

// A valid, unrevoked certificate must still be accepted.
func TestValidCertAccepted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	res, err := g.Authorize(validCert(now), nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("valid certificate rejected: %s", res.Reason)
	}
	if res.TenantID != "tenant-a" {
		t.Fatalf("tenant mapping wrong: %q", res.TenantID)
	}
}

// An OCSP timeout must never be treated as a good status.
func TestOCSPTimeoutFailsClosed(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	status, err := g.CheckOCSP("SN-1001", "", true, now)
	if err != nil {
		t.Fatalf("CheckOCSP: %v", err)
	}
	if status == taskpkg.RevocationGood {
		t.Fatalf("OCSP timeout was interpreted as a good certificate")
	}
	if status != taskpkg.RevocationUnknown {
		t.Fatalf("OCSP timeout must yield an unknown status, got %d", status)
	}
}

// When the SANs of one certificate point at different tenants the call must be
// rejected instead of silently using the first SAN.
func TestMultipleSANsMustMapToSingleTenant(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	cert := validCert(now)
	cert.SANs = []string{"alice@tenant-a.example.com", "bob@tenant-b.example.com"}
	res, err := g.Authorize(cert, nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Allowed {
		t.Fatalf("multi-tenant SAN list was authorized as tenant %q", res.TenantID)
	}
}

// Several SANs of the same tenant are fine.
func TestMultipleSANsSameTenantAllowed(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	cert := validCert(now)
	cert.SANs = []string{"alice@tenant-a.example.com", "svc@tenant-a.example.com"}
	res, err := g.Authorize(cert, nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("single-tenant SAN list rejected: %s", res.Reason)
	}
	if res.TenantID != "tenant-a" {
		t.Fatalf("tenant mapping wrong: %q", res.TenantID)
	}
}

// A resumed session must re-derive the identity instead of reusing the tenant
// bound to the old certificate.
func TestResumeRevalidatesCertificate(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	cert := validCert(now)
	if _, err := g.BindTenant(cert, "tenant-a"); err != nil {
		t.Fatalf("BindTenant: %v", err)
	}
	cert.Resumed = true
	cert.SANs = []string{"alice@tenant-b.example.com"}
	res, err := g.Authorize(cert, nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Allowed {
		t.Fatalf("resumed session kept the old tenant identity")
	}
}

// One certificate may not be bound to two tenants; the later binding must not
// overwrite the earlier one.
func TestConcurrentTenantBindingConflictRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	cert := validCert(now)
	if _, err := g.BindTenant(cert, "tenant-a"); err != nil {
		t.Fatalf("first BindTenant: %v", err)
	}
	if _, err := g.BindTenant(cert, "tenant-b"); err == nil {
		t.Fatalf("second binding of the same certificate was accepted")
	}
	tenant, err := g.TenantFor("SN-1001")
	if err != nil {
		t.Fatalf("TenantFor: %v", err)
	}
	if tenant != "tenant-a" {
		t.Fatalf("later binding overwrote the first: %q", tenant)
	}
}

// A certificate without its intermediates must not be considered valid.
func TestMissingIntermediateRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	cert := validCert(now)
	cert.IntermediatesPresent = false
	res, err := g.Authorize(cert, nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Allowed {
		t.Fatalf("certificate accepted without intermediates")
	}
}

// There must be no implicit grace period after expiry.
func TestExpiredCertRejectedWithoutGrace(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	cert := validCert(now)
	cert.NotAfter = now.Add(-time.Second)
	res, err := g.Authorize(cert, nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Allowed {
		t.Fatalf("expired certificate was accepted")
	}
}

// Reusing a gRPC connection must re-validate the certificate.
func TestConnectionReuseRevalidates(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	cert := validCert(now)
	ok, err := g.ReuseConnection("conn-1", cert, now)
	if err != nil {
		t.Fatalf("ReuseConnection initial: %v", err)
	}
	if !ok {
		t.Fatalf("valid connection reuse was rejected")
	}
	cert.NotAfter = now.Add(-time.Second)
	ok, err = g.ReuseConnection("conn-1", cert, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ReuseConnection expired: %v", err)
	}
	if ok {
		t.Fatalf("connection reused without re-validating the certificate")
	}
}

// Identity headers injected by the gateway must not override the certificate
// derived tenant.
func TestInjectedHeaderNotTrusted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	headers := map[string]string{
		"x-client-tenant":  "tenant-admin",
		"x-forwarded-user": "root",
	}
	res, err := g.Authorize(validCert(now), headers, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("valid certificate rejected: %s", res.Reason)
	}
	if res.TenantID != "tenant-a" {
		t.Fatalf("injected header overrode the certificate tenant: %q", res.TenantID)
	}
}

// When the revocation list cannot be refreshed the guard must fail closed.
func TestRevocationRefreshFailureFailsClosed(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := testConfig()
	cfg.RevocationSource = func() ([]string, error) {
		return nil, errors.New("ocsp responder unreachable")
	}
	g := taskpkg.NewMtlsGuard(cfg)
	if err := g.RefreshRevocations(); err == nil {
		t.Fatalf("RefreshRevocations reported success although the source failed")
	}
	res, err := g.Authorize(validCert(now), nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Allowed {
		t.Fatalf("certificate authorized while the revocation state was stale")
	}
}

// Duplicate serial numbers must be detected.
func TestDuplicateSerialDetected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	first := validCert(now)
	second := validCert(now)
	second.Fingerprint = "fp-2"
	second.SANs = []string{"bob@tenant-a.example.com"}
	if err := g.ObserveCert(first); err != nil {
		t.Fatalf("ObserveCert first: %v", err)
	}
	if err := g.ObserveCert(second); err != nil {
		t.Fatalf("ObserveCert second: %v", err)
	}
	dups, err := g.DuplicateSerials()
	if err != nil {
		t.Fatalf("DuplicateSerials: %v", err)
	}
	found := false
	for _, s := range dups {
		if s == "SN-1001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("duplicate serial was not reported: %v", dups)
	}
}

// Revocation state must be consistent between cluster nodes.
func TestClusterRevocationCacheConsistent(t *testing.T) {
	now := time.Unix(1700000000, 0)
	nodeA := freshGuard(t)
	if err := nodeA.Revoke("SN-1001"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	cache, err := nodeA.ExportRevocationCache()
	if err != nil {
		t.Fatalf("ExportRevocationCache: %v", err)
	}
	nodeB := taskpkg.NewMtlsGuardFromCache(testConfig(), cache)
	revoked, err := nodeB.IsRevoked("SN-1001")
	if err != nil {
		t.Fatalf("IsRevoked on node B: %v", err)
	}
	if !revoked {
		t.Fatalf("node B does not see the revocation issued on node A")
	}
	res, err := nodeB.Authorize(validCert(now), nil, now)
	if err != nil {
		t.Fatalf("Authorize on node B: %v", err)
	}
	if res.Allowed {
		t.Fatalf("node B authorized a certificate revoked on node A")
	}
}

// A refreshed revocation list must take effect immediately.
func TestRevocationListUpdateAppliesImmediately(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := freshGuard(t)
	revoked, err := g.IsRevoked("SN-1001")
	if err != nil {
		t.Fatalf("IsRevoked before: %v", err)
	}
	if revoked {
		t.Fatalf("serial reported revoked before the list update")
	}
	if err := g.LoadRevocationList([]string{"SN-1001"}); err != nil {
		t.Fatalf("LoadRevocationList: %v", err)
	}
	revoked, err = g.IsRevoked("SN-1001")
	if err != nil {
		t.Fatalf("IsRevoked after: %v", err)
	}
	if !revoked {
		t.Fatalf("revocation list update did not take effect")
	}
	res, err := g.Authorize(validCert(now), nil, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Allowed {
		t.Fatalf("certificate authorized after being added to the revocation list")
	}
}
