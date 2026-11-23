package task

import (
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-06"
)

var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

const trustedIssuer = "trusted@example.com"

func trustedRoot(issuer, keyID string) TrustRoot {
	return TrustRoot{
		Issuer:    issuer,
		KeyID:     keyID,
		PublicKey: "ed25519:" + keyID,
		NotBefore: fixedNow.Add(-24 * time.Hour),
		NotAfter:  fixedNow.Add(24 * time.Hour),
	}
}

func goodManifest() Manifest {
	return Manifest{Digest: "sha256:aaa", Arch: "amd64", AttestationDigest: "sha256:att"}
}

func goodRef() ImageRef {
	return ImageRef{Registry: "registry.example.com", Repo: "app", Tag: "v1", Digest: "sha256:aaa", Arch: "amd64"}
}

func goodSignature(issuer, keyID, digest string) Signature {
	return Signature{
		Issuer:         issuer,
		KeyID:          keyID,
		ManifestDigest: digest,
		SignedAt:       fixedNow,
		Bytes:          []byte("signature-bytes"),
	}
}

func newGuardWithRoot(t *testing.T) *ImageSignatureGuard {
	t.Helper()
	g := NewImageSignatureGuard(GuardPolicy{AllowedIssuers: []string{trustedIssuer}})
	if err := g.AddTrustRoot(trustedRoot(trustedIssuer, "k1")); err != nil {
		t.Fatalf("AddTrustRoot returned error: %v", err)
	}
	return g
}

func TestVerifyRequiresAllowedIssuer(t *testing.T) {
	g := newGuardWithRoot(t)
	if err := g.AddTrustRoot(trustedRoot("evil@example.com", "k9")); err != nil {
		t.Fatalf("AddTrustRoot returned error: %v", err)
	}
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("signature from the allowed issuer rejected: %v", err)
	}
	if err := g.Verify(goodRef(), goodSignature("evil@example.com", "k9", "sha256:aaa"), goodManifest(), fixedNow); err == nil {
		t.Fatalf("signature from an issuer outside the allow list was accepted")
	}
}

func TestExpiredTrustRootRejected(t *testing.T) {
	g := newGuardWithRoot(t)
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("valid root rejected: %v", err)
	}
	expired := NewImageSignatureGuard(GuardPolicy{AllowedIssuers: []string{trustedIssuer}})
	root := trustedRoot(trustedIssuer, "k1")
	root.NotAfter = fixedNow.Add(-time.Hour)
	if err := expired.AddTrustRoot(root); err != nil {
		t.Fatalf("AddTrustRoot returned error: %v", err)
	}
	if err := expired.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err == nil {
		t.Fatalf("expired trust root was still used")
	}
}

func TestManifestDigestMustMatchPulledObject(t *testing.T) {
	g := newGuardWithRoot(t)
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("matching digest rejected: %v", err)
	}
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:bbb"), goodManifest(), fixedNow); err == nil {
		t.Fatalf("signature over a different manifest digest was accepted")
	}
}

func TestMultiArchVerifiesEveryArch(t *testing.T) {
	g := newGuardWithRoot(t)
	mans := []Manifest{
		{Digest: "sha256:amd", Arch: "amd64"},
		{Digest: "sha256:arm", Arch: "arm64"},
	}
	partial := []Signature{goodSignature(trustedIssuer, "k1", "sha256:amd")}
	if err := g.VerifyMultiArch(goodRef(), mans, partial, fixedNow); err == nil {
		t.Fatalf("multi arch image without a signature for arm64 was accepted")
	}
	complete := append(partial, goodSignature(trustedIssuer, "k1", "sha256:arm"))
	if err := g.VerifyMultiArch(goodRef(), mans, complete, fixedNow); err != nil {
		t.Fatalf("fully signed multi arch image rejected: %v", err)
	}
}

func TestAttestationBoundToImage(t *testing.T) {
	g := newGuardWithRoot(t)
	att := Attestation{SubjectDigest: "sha256:aaa", PayloadDigest: "sha256:att"}
	if err := g.VerifyAttestation("sha256:aaa", att); err != nil {
		t.Fatalf("attestation bound to the image rejected: %v", err)
	}
	if err := g.VerifyAttestation("sha256:bbb", att); err == nil {
		t.Fatalf("attestation for another digest was accepted")
	}
}

func TestCacheEntryExpires(t *testing.T) {
	g := newGuardWithRoot(t)
	entry := CacheEntry{Ref: "app:v1", Digest: "sha256:aaa", Verified: true, ExpiresAt: fixedNow.Add(time.Hour)}
	if err := g.CachePut(entry); err != nil {
		t.Fatalf("CachePut returned error: %v", err)
	}
	if got, ok := g.CacheGet("app:v1", "sha256:aaa", fixedNow); !ok || !got.Verified {
		t.Fatalf("valid cache entry not returned: %+v ok=%v", got, ok)
	}
	if _, ok := g.CacheGet("app:v1", "sha256:aaa", fixedNow.Add(2*time.Hour)); ok {
		t.Fatalf("expired cache entry was still accepted")
	}
}

func TestFailedVerifyNeverAdmits(t *testing.T) {
	g := newGuardWithRoot(t)
	att := Attestation{SubjectDigest: "sha256:aaa", PayloadDigest: "sha256:att"}
	if !g.AdmitRef(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), att, fixedNow) {
		t.Fatalf("valid image was not admitted")
	}
	if g.AdmitRef(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:zzz"), goodManifest(), att, fixedNow) {
		t.Fatalf("image with a mismatched signature digest was admitted")
	}
}

func TestConcurrentCachePutKeepsCompleteEntry(t *testing.T) {
	g := newGuardWithRoot(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := CacheEntry{
				Ref:       "app:v1",
				Digest:    "sha256:aaa",
				Verified:  true,
				ExpiresAt: fixedNow.Add(time.Duration(i+1) * time.Hour),
			}
			if err := g.CachePut(e); err != nil {
				t.Errorf("CachePut returned error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	got, ok := g.CacheGet("app:v1", "sha256:aaa", fixedNow)
	if !ok {
		t.Fatalf("cache entry lost after concurrent puts")
	}
	if !got.Verified {
		t.Fatalf("concurrent puts left a half written entry: %+v", got)
	}
	if !got.ExpiresAt.After(fixedNow) {
		t.Fatalf("concurrent puts produced an entry without a valid expiry: %+v", got)
	}
}

func TestCriticalFieldsEnforced(t *testing.T) {
	g := newGuardWithRoot(t)
	sig := goodSignature(trustedIssuer, "k1", "sha256:aaa")
	sig.Critical = map[string]string{"expires": fixedNow.Add(time.Hour).Format(time.RFC3339)}
	if err := g.Verify(goodRef(), sig, goodManifest(), fixedNow); err != nil {
		t.Fatalf("valid critical field rejected: %v", err)
	}
	expired := goodSignature(trustedIssuer, "k1", "sha256:aaa")
	expired.Critical = map[string]string{"expires": fixedNow.Add(-time.Hour).Format(time.RFC3339)}
	if err := g.Verify(goodRef(), expired, goodManifest(), fixedNow); err == nil {
		t.Fatalf("signature whose critical expires field lapsed was accepted")
	}
	unknown := goodSignature(trustedIssuer, "k1", "sha256:aaa")
	unknown.Critical = map[string]string{"rekor": "offline"}
	if err := g.Verify(goodRef(), unknown, goodManifest(), fixedNow); err == nil {
		t.Fatalf("unknown critical field must be rejected")
	}
}

func TestRotatedIssuerKeyRejectsOldSignature(t *testing.T) {
	g := newGuardWithRoot(t)
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("current key rejected: %v", err)
	}
	if err := g.AddTrustRoot(trustedRoot(trustedIssuer, "k2")); err != nil {
		t.Fatalf("AddTrustRoot returned error: %v", err)
	}
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err == nil {
		t.Fatalf("signature from the retired key k1 was accepted after rotation")
	}
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k2", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("signature from the rotated key k2 rejected: %v", err)
	}
}

func TestRootsSurviveExportImport(t *testing.T) {
	g := newGuardWithRoot(t)
	data, err := g.ExportRoots()
	if err != nil {
		t.Fatalf("ExportRoots returned error: %v", err)
	}
	restarted := NewImageSignatureGuard(GuardPolicy{AllowedIssuers: []string{trustedIssuer}})
	if err := restarted.ImportRoots(data); err != nil {
		t.Fatalf("ImportRoots returned error: %v", err)
	}
	if len(restarted.TrustRoots()) == 0 {
		t.Fatalf("trust roots lost after restart")
	}
	if err := restarted.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("verification after import failed: %v", err)
	}
}

func TestMissingSignatureRejected(t *testing.T) {
	g := newGuardWithRoot(t)
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("signed image rejected: %v", err)
	}
	empty := goodSignature(trustedIssuer, "k1", "sha256:aaa")
	empty.Bytes = nil
	if err := g.Verify(goodRef(), empty, goodManifest(), fixedNow); err == nil {
		t.Fatalf("image without signature bytes was treated as passing")
	}
	att := Attestation{SubjectDigest: "sha256:aaa", PayloadDigest: "sha256:att"}
	if g.AdmitRef(goodRef(), empty, goodManifest(), att, fixedNow) {
		t.Fatalf("unsigned image was admitted")
	}
}

func TestCacheKeyIncludesDigest(t *testing.T) {
	g := newGuardWithRoot(t)
	entry := CacheEntry{Ref: "app:v1", Digest: "sha256:aaa", Verified: true, ExpiresAt: fixedNow.Add(time.Hour)}
	if err := g.CachePut(entry); err != nil {
		t.Fatalf("CachePut returned error: %v", err)
	}
	if _, ok := g.CacheGet("app:v1", "sha256:aaa", fixedNow); !ok {
		t.Fatalf("cache entry for the same digest missing")
	}
	if _, ok := g.CacheGet("app:v1", "sha256:bbb", fixedNow); ok {
		t.Fatalf("cache entry was served for a different digest")
	}
}

func TestPolicyRequiresAttestation(t *testing.T) {
	g := NewImageSignatureGuard(GuardPolicy{AllowedIssuers: []string{trustedIssuer}, RequireAttestation: true})
	if err := g.AddTrustRoot(trustedRoot(trustedIssuer, "k1")); err != nil {
		t.Fatalf("AddTrustRoot returned error: %v", err)
	}
	sig := goodSignature(trustedIssuer, "k1", "sha256:aaa")
	man := goodManifest()
	att := Attestation{SubjectDigest: "sha256:aaa", PayloadDigest: "sha256:att"}
	if !g.AdmitRef(goodRef(), sig, man, att, fixedNow) {
		t.Fatalf("signed and attested image was not admitted")
	}
	if g.AdmitRef(goodRef(), sig, man, Attestation{}, fixedNow) {
		t.Fatalf("image without attestation was admitted although the policy requires one")
	}
}

func TestTrustRootIssuerAndKeyMustMatch(t *testing.T) {
	g := newGuardWithRoot(t)
	if err := g.Verify(goodRef(), goodSignature(trustedIssuer, "k1", "sha256:aaa"), goodManifest(), fixedNow); err != nil {
		t.Fatalf("matching issuer and key rejected: %v", err)
	}
	otherIssuer := NewImageSignatureGuard(GuardPolicy{AllowedIssuers: []string{trustedIssuer, "other@example.com"}})
	if err := otherIssuer.AddTrustRoot(trustedRoot(trustedIssuer, "k1")); err != nil {
		t.Fatalf("AddTrustRoot returned error: %v", err)
	}
	if err := otherIssuer.Verify(goodRef(), goodSignature("other@example.com", "k1", "sha256:aaa"), goodManifest(), fixedNow); err == nil {
		t.Fatalf("signature claiming an issuer without a trust root was accepted")
	}
	if err := otherIssuer.Verify(goodRef(), goodSignature(trustedIssuer, "k2", "sha256:aaa"), goodManifest(), fixedNow); err == nil {
		t.Fatalf("signature claiming an unknown key of a known issuer was accepted")
	}
}
