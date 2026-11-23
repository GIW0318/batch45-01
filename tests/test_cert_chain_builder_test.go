package task

import (
	"errors"
	"testing"
	"time"

	task "gsb/batch45-safety-c-03"
)

var chainNow = time.Unix(1700000000, 0)

func validFrom() time.Time { return chainNow.Add(-48 * time.Hour) }
func validTo() time.Time   { return chainNow.Add(48 * time.Hour) }

func leafCert() task.Cert {
	return task.Cert{
		Subject:        "CN=api.example.com",
		Issuer:         "CN=Corp Intermediate",
		Serial:         "leaf-1",
		Fingerprint:    "fp-leaf",
		AuthorityKeyID: "ski-int",
		IsCA:           false,
		PathLen:        -1,
		NotBefore:      validFrom(),
		NotAfter:       validTo(),
		SANs:           []string{"api.example.com"},
		Policies:       []string{"2.23.140.1.2.1"},
		SignatureValid: true,
	}
}

func intermediateCert() task.Cert {
	return task.Cert{
		Subject:        "CN=Corp Intermediate",
		Issuer:         "CN=Corp Root",
		Serial:         "int-1",
		Fingerprint:    "fp-int",
		SubjectKeyID:   "ski-int",
		AuthorityKeyID: "ski-root",
		IsCA:           true,
		PathLen:        -1,
		NotBefore:      validFrom(),
		NotAfter:       validTo(),
		Policies:       []string{"2.23.140.1.2.1"},
		SignatureValid: true,
	}
}

func anchorCert() task.Cert {
	return task.Cert{
		Subject:        "CN=Corp Root",
		Issuer:         "CN=Corp Root",
		Serial:         "root-1",
		Fingerprint:    "fp-root",
		SubjectKeyID:   "ski-root",
		IsCA:           true,
		SelfSigned:     true,
		PathLen:        -1,
		NotBefore:      validFrom(),
		NotAfter:       validTo(),
		Policies:       []string{"2.23.140.1.2.1"},
		SignatureValid: true,
	}
}

func builder() *task.ChainBuilder {
	return task.NewChainBuilder(task.BuilderOptions{
		MaxDepth:      5,
		CurrentTime:   chainNow,
		PinnedAnchors: []string{"fp-root"},
	})
}

func TestSubjectMatchWithoutSignatureRejected(t *testing.T) {
	broken := intermediateCert()
	broken.SignatureValid = false
	_, err := builder().Build(leafCert(), []task.Cert{broken}, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrNoTrustAnchor) {
		t.Fatalf("a subject match with an invalid signature must not build a path, got %v", err)
	}
}

func TestMultipleIntermediatesSameSubjectBacktracks(t *testing.T) {
	broken := intermediateCert()
	broken.Serial = "int-broken"
	broken.SignatureValid = false
	good := intermediateCert()
	good.Serial = "int-good"
	chain, err := builder().Build(leafCert(), []task.Cert{broken, good}, []task.Cert{anchorCert()})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if len(chain) != 3 || chain[1].Serial != "int-good" {
		t.Fatalf("the builder must try the other intermediate with the same subject, got %+v", chain)
	}
}

func TestPathLenConstraintEnforced(t *testing.T) {
	root := anchorCert()
	root.PathLen = 0
	_, err := builder().Build(leafCert(), []task.Cert{intermediateCert()}, []task.Cert{root})
	if !errors.Is(err, task.ErrPathLenViolation) {
		t.Fatalf("an intermediate below a pathLen 0 anchor must be rejected, got %v", err)
	}
}

func TestNameConstraintAppliesToSAN(t *testing.T) {
	root := anchorCert()
	root.PermittedDNS = []string{"example.com"}
	leaf := leafCert()
	leaf.SANs = []string{"evil.com"}
	_, err := builder().Build(leaf, []task.Cert{intermediateCert()}, []task.Cert{root})
	if !errors.Is(err, task.ErrNameConstraintViolation) {
		t.Fatalf("name constraints must be checked against SAN entries, got %v", err)
	}
}

func TestPolicyConstraintEnforced(t *testing.T) {
	root := anchorCert()
	root.RequiredPolicies = []string{"2.23.140.1.2.2"}
	_, err := builder().Build(leafCert(), []task.Cert{intermediateCert()}, []task.Cert{root})
	if !errors.Is(err, task.ErrPolicyViolation) {
		t.Fatalf("a required policy missing from the chain must be rejected, got %v", err)
	}
}

func TestRevokedIntermediateRejected(t *testing.T) {
	inter := intermediateCert()
	inter.Revoked = true
	_, err := builder().Build(leafCert(), []task.Cert{inter}, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrRevoked) {
		t.Fatalf("revocation must be checked for every chain element, got %v", err)
	}
}

func TestExpiredIntermediateRejected(t *testing.T) {
	inter := intermediateCert()
	inter.NotAfter = chainNow.Add(-time.Hour)
	_, err := builder().Build(leafCert(), []task.Cert{inter}, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrExpired) {
		t.Fatalf("an expired intermediate must not be used, got %v", err)
	}
}

func TestSelfSignedLeafNotTrustedAnchor(t *testing.T) {
	leaf := leafCert()
	leaf.SelfSigned = true
	leaf.Issuer = leaf.Subject
	leaf.Fingerprint = "fp-self"
	_, err := builder().Build(leaf, nil, nil)
	if !errors.Is(err, task.ErrNoTrustAnchor) {
		t.Fatalf("a self signed leaf outside the anchor set must not validate, got %v", err)
	}
}

func TestDuplicateCertsDoNotLoopForever(t *testing.T) {
	leaf := leafCert()
	leaf.Issuer = "CN=A"
	a := task.Cert{Subject: "CN=A", Issuer: "CN=B", Serial: "a", IsCA: true, PathLen: -1, NotBefore: validFrom(), NotAfter: validTo(), SignatureValid: true}
	b := task.Cert{Subject: "CN=B", Issuer: "CN=A", Serial: "b", IsCA: true, PathLen: -1, NotBefore: validFrom(), NotAfter: validTo(), SignatureValid: true}
	_, err := builder().Build(leaf, []task.Cert{a, b, a}, nil)
	if !errors.Is(err, task.ErrNoTrustAnchor) {
		t.Fatalf("a cyclic pool must end without a path instead of looping, got %v", err)
	}
}

func TestMaxDepthEnforced(t *testing.T) {
	b := task.NewChainBuilder(task.BuilderOptions{
		MaxDepth:      1,
		CurrentTime:   chainNow,
		PinnedAnchors: []string{"fp-root"},
	})
	_, err := b.Build(leafCert(), []task.Cert{intermediateCert()}, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrChainTooDeep) {
		t.Fatalf("paths above max depth must be rejected, got %v", err)
	}
}

func TestMissingIntermediateReported(t *testing.T) {
	leaf := leafCert()
	leaf.Issuer = "CN=Missing Intermediate"
	_, err := builder().Build(leaf, nil, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrMissingIntermediate) {
		t.Fatalf("a missing intermediate must be reported instead of skipped, got %v", err)
	}
}

func TestAnchorFingerprintMismatchRejected(t *testing.T) {
	b := task.NewChainBuilder(task.BuilderOptions{
		MaxDepth:      5,
		CurrentTime:   chainNow,
		PinnedAnchors: []string{"fp-other"},
	})
	_, err := b.Build(leafCert(), []task.Cert{intermediateCert()}, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrAnchorFingerprintMismatch) {
		t.Fatalf("a trust anchor must be pinned by fingerprint, got %v", err)
	}
}

func TestValidChainBuiltLeafToAnchor(t *testing.T) {
	chain, err := builder().Build(leafCert(), []task.Cert{intermediateCert()}, []task.Cert{anchorCert()})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("expected a three certificate chain, got %d", len(chain))
	}
	if chain[0].Serial != "leaf-1" || chain[1].Serial != "int-1" || chain[2].Fingerprint != "fp-root" {
		t.Fatalf("chain must be ordered leaf to anchor, got %+v", chain)
	}
}

func TestRevokedLeafRejected(t *testing.T) {
	leaf := leafCert()
	leaf.Revoked = true
	_, err := builder().Build(leaf, []task.Cert{intermediateCert()}, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrRevoked) {
		t.Fatalf("a revoked leaf must be rejected, got %v", err)
	}
}

func TestExpiredLeafRejected(t *testing.T) {
	leaf := leafCert()
	leaf.NotAfter = chainNow.Add(-time.Minute)
	_, err := builder().Build(leaf, []task.Cert{intermediateCert()}, []task.Cert{anchorCert()})
	if !errors.Is(err, task.ErrExpired) {
		t.Fatalf("an expired leaf must be rejected, got %v", err)
	}
}
