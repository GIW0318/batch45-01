package task

import (
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-07"
)

var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

const subjectDigest = "sha256:img"

func validSBOM() SBOM {
	return SBOM{
		Format:        "spdx-2.3",
		SubjectDigest: subjectDigest,
		Components:    []Component{{Name: "app", Version: "1.2.3"}},
		CreatedAt:     fixedNow.Add(-time.Hour),
	}
}

func validAttestation(g *SbomGuard, id string) Attestation {
	sbom := validSBOM()
	return Attestation{
		ID:            id,
		Tenant:        "tenant-a",
		SubjectDigest: sbom.SubjectDigest,
		PayloadDigest: g.SBOMDigest(sbom),
		SBOM:          sbom,
		Materials:     []string{"base-image:alpine:3.19"},
		BuildParams:   map[string]string{"target": "prod"},
		SignedAt:      fixedNow.Add(-time.Minute),
		ExpiresAt:     fixedNow.Add(24 * time.Hour),
		Signature:     []byte("sig"),
	}
}

func mustSubmit(t *testing.T, g *SbomGuard, att Attestation) {
	t.Helper()
	if err := g.Submit(att, fixedNow); err != nil {
		t.Fatalf("Submit returned error for a valid attestation: %v", err)
	}
}

func TestStaleSBOMRejected(t *testing.T) {
	g := NewSbomGuard()
	newer := validAttestation(g, "att-new")
	mustSubmit(t, g, newer)
	older := validAttestation(g, "att-old")
	older.SBOM.CreatedAt = fixedNow.Add(-3 * time.Hour)
	older.PayloadDigest = g.SBOMDigest(older.SBOM)
	if err := g.Submit(older, fixedNow); err == nil {
		t.Fatalf("older SBOM for the same digest must not overwrite the newer one")
	}
	got, err := g.Get("tenant-a", subjectDigest)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != "att-new" {
		t.Fatalf("stored attestation = %s, want att-new", got.ID)
	}
}

func TestComponentVersionsMustBePinned(t *testing.T) {
	g := NewSbomGuard()
	floating := validSBOM()
	floating.Components = []Component{{Name: "openssl", Version: ">=1.1.1"}}
	if err := g.ValidateSBOM(floating); err == nil {
		t.Fatalf("range version must be rejected")
	}
	caret := validSBOM()
	caret.Components = []Component{{Name: "openssl", Version: "^3.0.0"}}
	if err := g.ValidateSBOM(caret); err == nil {
		t.Fatalf("caret version must be rejected")
	}
	if err := g.ValidateSBOM(validSBOM()); err != nil {
		t.Fatalf("pinned version rejected: %v", err)
	}
}

func TestSignatureCoversSBOMContent(t *testing.T) {
	g := NewSbomGuard()
	att := validAttestation(g, "att-1")
	mustSubmit(t, g, att)
	tampered := validAttestation(g, "att-2")
	tampered.PayloadDigest = "sha256:other"
	if err := g.Submit(tampered, fixedNow); err == nil {
		t.Fatalf("attestation whose payload digest does not cover the SBOM content was accepted")
	}
}

func TestMergeAttestationsSameDigest(t *testing.T) {
	g := NewSbomGuard()
	a := validAttestation(g, "att-a")
	b := validAttestation(g, "att-b")
	b.Materials = []string{"layer:sha256:base"}
	b.SBOM.Components = []Component{{Name: "libc", Version: "2.39"}}
	merged, err := g.Merge(a, b)
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if len(merged.Materials) != 2 {
		t.Fatalf("merged materials = %v, want base image plus layer", merged.Materials)
	}
	if len(merged.SBOM.Components) != 2 {
		t.Fatalf("merged components = %v, want both components", merged.SBOM.Components)
	}
	if merged.ID != "att-a" {
		t.Fatalf("merged ID = %q, want the original attestation id", merged.ID)
	}
}

func TestBaseImageMaterialRequired(t *testing.T) {
	g := NewSbomGuard()
	att := validAttestation(g, "att-1")
	att.Materials = []string{"layer:sha256:abc"}
	att.PayloadDigest = g.SBOMDigest(att.SBOM)
	if err := g.Submit(att, fixedNow); err == nil {
		t.Fatalf("attestation without the base image material was accepted")
	}
	mustSubmit(t, g, validAttestation(g, "att-2"))
}

func TestExpiredAttestationRejected(t *testing.T) {
	g := NewSbomGuard()
	att := validAttestation(g, "att-1")
	att.ExpiresAt = fixedNow.Add(-time.Minute)
	att.PayloadDigest = g.SBOMDigest(att.SBOM)
	if err := g.Submit(att, fixedNow); err == nil {
		t.Fatalf("expired attestation was accepted")
	}
	mustSubmit(t, g, validAttestation(g, "att-2"))
}

func TestConcurrentSubmitSingleWinner(t *testing.T) {
	g := NewSbomGuard()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			att := validAttestation(g, "att-concurrent")
			att.SignedAt = fixedNow.Add(time.Duration(i) * time.Second)
			if err := g.Submit(att, fixedNow); err != nil {
				t.Errorf("concurrent Submit returned error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	list, err := g.Audit(AuditQuery{Tenant: "tenant-a", SubjectDigest: subjectDigest})
	if err != nil {
		t.Fatalf("Audit returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("audit returned %d attestations for one digest, concurrent builds must not duplicate", len(list))
	}
}

func TestBuildParamsRedacted(t *testing.T) {
	g := NewSbomGuard()
	params := map[string]string{
		"AWS_SECRET_ACCESS_KEY": "AKIA-secret",
		"GITHUB_TOKEN":          "ghp-secret",
		"db_password":           "hunter2",
		"target":                "prod",
	}
	redacted := g.RedactParams(params)
	for _, key := range []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "db_password"} {
		if redacted[key] != "[REDACTED]" {
			t.Fatalf("%s = %q, want [REDACTED]", key, redacted[key])
		}
	}
	if redacted["target"] != "prod" {
		t.Fatalf("benign parameter was altered: %q", redacted["target"])
	}
	att := validAttestation(g, "att-secret")
	att.BuildParams = map[string]string{"AWS_SECRET_ACCESS_KEY": "AKIA-secret"}
	att.PayloadDigest = g.SBOMDigest(att.SBOM)
	if err := g.Submit(att, fixedNow); err == nil {
		t.Fatalf("attestation carrying secrets in build parameters was accepted")
	}
}

func TestMixedFormatVersionsRejected(t *testing.T) {
	g := NewSbomGuard()
	mustSubmit(t, g, validAttestation(g, "att-spdx"))
	mixed := validAttestation(g, "att-cdx")
	mixed.SBOM.Format = "cyclonedx-1.5"
	mixed.PayloadDigest = g.SBOMDigest(mixed.SBOM)
	if err := g.Submit(mixed, fixedNow); err == nil {
		t.Fatalf("attestation mixing SBOM formats for one digest was accepted")
	}
}

func TestCyclicDependencyDetected(t *testing.T) {
	g := NewSbomGuard()
	sbom := validSBOM()
	sbom.Components = []Component{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	sbom.Deps = []Dependency{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "a"}}
	if _, err := g.DependencyOrder(sbom); err == nil {
		t.Fatalf("dependency cycle was not detected")
	}
	sbom.Deps = []Dependency{{From: "a", To: "b"}, {From: "b", To: "c"}}
	order, err := g.DependencyOrder(sbom)
	if err != nil {
		t.Fatalf("acyclic graph rejected: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("order = %v, want all three components", order)
	}
}

func TestTenantIsolation(t *testing.T) {
	g := NewSbomGuard()
	mustSubmit(t, g, validAttestation(g, "att-a"))
	if _, err := g.Get("tenant-b", subjectDigest); err == nil {
		t.Fatalf("tenant-b read tenant-a attestation")
	}
	list, err := g.Audit(AuditQuery{Tenant: "tenant-b"})
	if err != nil {
		t.Fatalf("Audit returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("audit for tenant-b returned %d attestations of other tenants", len(list))
	}
}

func TestAuditExcludesRevoked(t *testing.T) {
	g := NewSbomGuard()
	mustSubmit(t, g, validAttestation(g, "att-1"))
	if err := g.Revoke("att-1"); err != nil {
		t.Fatalf("Revoke returned error: %v", err)
	}
	list, err := g.Audit(AuditQuery{Tenant: "tenant-a", SubjectDigest: subjectDigest})
	if err != nil {
		t.Fatalf("Audit returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("audit returned a revoked attestation")
	}
	all, err := g.Audit(AuditQuery{Tenant: "tenant-a", SubjectDigest: subjectDigest, IncludeRevoked: true})
	if err != nil {
		t.Fatalf("Audit returned error: %v", err)
	}
	if len(all) != 1 || !all[0].Revoked {
		t.Fatalf("audit with revoked entries = %+v, want the revoked attestation", all)
	}
}

func TestDependencyOrderTopological(t *testing.T) {
	g := NewSbomGuard()
	sbom := validSBOM()
	sbom.Components = []Component{{Name: "app"}, {Name: "libc"}, {Name: "kernel"}}
	sbom.Deps = []Dependency{{From: "app", To: "libc"}, {From: "libc", To: "kernel"}}
	order, err := g.DependencyOrder(sbom)
	if err != nil {
		t.Fatalf("DependencyOrder returned error: %v", err)
	}
	pos := map[string]int{}
	for i, name := range order {
		pos[name] = i
	}
	if pos["kernel"] > pos["libc"] || pos["libc"] > pos["app"] {
		t.Fatalf("order %v does not place dependencies before dependents", order)
	}
}

func TestSubmitRequiresSubjectDigest(t *testing.T) {
	g := NewSbomGuard()
	att := validAttestation(g, "att-1")
	att.SubjectDigest = ""
	if err := g.Submit(att, fixedNow); err == nil {
		t.Fatalf("attestation without subject digest was accepted")
	}
	mustSubmit(t, g, validAttestation(g, "att-2"))
}

func TestDuplicateSubmitIdempotent(t *testing.T) {
	g := NewSbomGuard()
	att := validAttestation(g, "att-1")
	mustSubmit(t, g, att)
	if err := g.Submit(att, fixedNow); err != nil {
		t.Fatalf("re-submitting the same attestation returned error: %v", err)
	}
	list, err := g.Audit(AuditQuery{Tenant: "tenant-a", SubjectDigest: subjectDigest})
	if err != nil {
		t.Fatalf("Audit returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("idempotent submit produced %d attestations", len(list))
	}
}
