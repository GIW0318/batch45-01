package task

import (
	"errors"
	"reflect"
	"testing"
	"time"

	task "gsb/batch45-safety-c-05"
)

var dnsNow = time.Unix(1700000000, 0)

func newValidator() *task.DnssecValidator {
	return task.NewDnssecValidator(task.ValidatorConfig{
		MaxNSEC3Iterations: 100,
		AllowedDigests:     []uint8{2, 4},
		ClockSkew:          5 * time.Minute,
	})
}

func signedRRset() task.RRset {
	return task.RRset{Name: "www.example.com", Type: task.TypeA, TTL: 300, Records: []string{"192.0.2.1"}}
}

func validSig() task.RRSIG {
	return task.RRSIG{
		TypeCovered: task.TypeA,
		Algorithm:   13,
		Labels:      3,
		Inception:   uint32(dnsNow.Add(-time.Hour).Unix()),
		Expiration:  uint32(dnsNow.Add(time.Hour).Unix()),
		KeyTag:      1234,
		SignerName:  "example.com",
		Signature:   "c2ln",
	}
}

func TestExpiredSignatureRejected(t *testing.T) {
	v := newValidator()
	sig := validSig()
	sig.Expiration = uint32(dnsNow.Add(-time.Hour).Unix())
	_, err := v.ValidateSignature(signedRRset(), sig, dnsNow)
	if !errors.Is(err, task.ErrSignatureExpired) {
		t.Fatalf("an expired RRSIG must be rejected, got %v", err)
	}
}

func TestNotYetValidSignatureRejected(t *testing.T) {
	v := newValidator()
	sig := validSig()
	sig.Inception = uint32(dnsNow.Add(time.Hour).Unix())
	_, err := v.ValidateSignature(signedRRset(), sig, dnsNow)
	if !errors.Is(err, task.ErrSignatureNotYetValid) {
		t.Fatalf("an RRSIG before its inception must be rejected, got %v", err)
	}
}

func TestSignerNameMustMatchZone(t *testing.T) {
	v := newValidator()
	sig := validSig()
	sig.SignerName = "other.example.net"
	if err := v.CheckSignerZone(sig, "example.com"); !errors.Is(err, task.ErrSignerMismatch) {
		t.Fatalf("the signer name must belong to the zone, got %v", err)
	}
	if err := v.CheckSignerZone(validSig(), "example.com"); err != nil {
		t.Fatalf("a signer inside the zone must be accepted, got %v", err)
	}
}

func TestSEPFlagRequired(t *testing.T) {
	v := newValidator()
	zoneKey := task.DNSKEY{Flags: 256, Protocol: 3, Algorithm: 13, KeyTag: 1234}
	ds := task.DS{KeyTag: 1234, Algorithm: 13, DigestType: 2, Digest: "abcd"}
	ok, err := v.CheckKey(zoneKey, ds)
	if err != nil {
		t.Fatalf("CheckKey returned unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("a DNSKEY without the SEP flag must not validate a DS")
	}
	sepKey := zoneKey
	sepKey.Flags = 257
	ok, err = v.CheckKey(sepKey, ds)
	if err != nil {
		t.Fatalf("CheckKey returned unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("a SEP DNSKEY matching the DS must validate")
	}
}

func TestDeprecatedDSDigestRejected(t *testing.T) {
	v := newValidator()
	if err := v.CheckDSDigest(task.DS{DigestType: 1}); !errors.Is(err, task.ErrDeprecatedDigest) {
		t.Fatalf("a deprecated DS digest algorithm must be rejected, got %v", err)
	}
	if err := v.CheckDSDigest(task.DS{DigestType: 2}); err != nil {
		t.Fatalf("an allowed DS digest algorithm must pass, got %v", err)
	}
}

func TestNSEC3IterationsCapped(t *testing.T) {
	v := newValidator()
	_, err := v.ValidateNSEC3(task.NSEC3Proof{Iterations: 500, ClosestEncloser: "example.com", Matched: true})
	if !errors.Is(err, task.ErrNSEC3Iterations) {
		t.Fatalf("an NSEC3 iteration count above the cap must be rejected, got %v", err)
	}
}

func TestWildcardClosestEncloserRequired(t *testing.T) {
	v := newValidator()
	_, err := v.ValidateWildcard(task.WildcardProof{Wildcard: "*.example.com", NextCloserName: "a.example.com", Valid: true})
	if !errors.Is(err, task.ErrWildcardProof) {
		t.Fatalf("a wildcard proof without a closest encloser must be rejected, got %v", err)
	}
	ok, err := v.ValidateWildcard(task.WildcardProof{ClosestEncloser: "example.com", Wildcard: "*.example.com", NextCloserName: "a.example.com", Valid: true})
	if err != nil {
		t.Fatalf("ValidateWildcard returned unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("a complete wildcard proof must validate")
	}
}

func TestCNAMEChainUnsignedLinkRejected(t *testing.T) {
	v := newValidator()
	chain := []task.SignedRRset{
		{RRset: task.RRset{Name: "a.example.com", Type: task.TypeCNAME, Records: []string{"b.example.com"}}, Sig: validSig(), Signed: true},
		{RRset: task.RRset{Name: "b.example.com", Type: task.TypeA, Records: []string{"192.0.2.9"}}, Signed: false},
	}
	_, err := v.ValidateCNAMEChain(chain)
	if !errors.Is(err, task.ErrUnsignedLink) {
		t.Fatalf("every CNAME chain link must be signed, got %v", err)
	}
}

func TestCanonicalizeSortsRRset(t *testing.T) {
	v := newValidator()
	rr := task.RRset{Name: "example.com", Type: task.TypeTXT, Records: []string{"c", "a", "b"}}
	got, err := v.Canonicalize(rr)
	if err != nil {
		t.Fatalf("Canonicalize returned unexpected error: %v", err)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RRset order must not affect the signature, got %v want %v", got, want)
	}
}

func TestKeyRolloverRetainsOldKey(t *testing.T) {
	v := newValidator()
	newKeys := []task.DNSKEY{{Flags: 257, Protocol: 3, Algorithm: 13, KeyTag: 2001, PublicKey: "new"}}
	oldKeys := []task.DNSKEY{{Flags: 257, Protocol: 3, Algorithm: 13, KeyTag: 1001, PublicKey: "old"}}
	set, err := v.Rollover(newKeys, oldKeys, dnsNow)
	if err != nil {
		t.Fatalf("Rollover returned unexpected error: %v", err)
	}
	if len(set.Active) != 1 || set.Active[0].KeyTag != 2001 {
		t.Fatalf("the new key must be active, got %+v", set.Active)
	}
	if len(set.Retired) != 1 || set.Retired[0].KeyTag != 1001 {
		t.Fatalf("the old key must stay usable during the rollover, got %+v", set.Retired)
	}
}

func TestResolveRejectsBogusWithoutFallback(t *testing.T) {
	v := newValidator()
	resp := task.Response{Results: []task.SignedRRset{{
		RRset:  signedRRset(),
		Signed: false,
	}}}
	_, err := v.Resolve(resp)
	if !errors.Is(err, task.ErrBogus) {
		t.Fatalf("a bogus answer must fail instead of falling back to plain DNS, got %v", err)
	}
}

func TestTruncatedResponseRetriesTCP(t *testing.T) {
	v := newValidator()
	res, err := v.Resolve(task.Response{Truncated: true})
	if err != nil {
		t.Fatalf("Resolve returned unexpected error: %v", err)
	}
	if !res.UsedTCP {
		t.Fatalf("a truncated answer must be retried over TCP, got %+v", res)
	}
}

func TestNegativeResultNotCached(t *testing.T) {
	v := newValidator()
	if err := v.CachePut("bad.example", task.TypeA, task.Result{Secure: false, Reason: "bogus signature"}); err != nil {
		t.Fatalf("CachePut returned unexpected error: %v", err)
	}
	if _, ok := v.CacheGet("bad.example", task.TypeA); ok {
		t.Fatalf("a failed validation must not be cached")
	}
	if err := v.CachePut("good.example", task.TypeA, task.Result{Secure: true}); err != nil {
		t.Fatalf("CachePut returned unexpected error: %v", err)
	}
	res, ok := v.CacheGet("good.example", task.TypeA)
	if !ok || !res.Secure {
		t.Fatalf("a validated result must be cached, ok=%v res=%+v", ok, res)
	}
}

func TestValidSignatureAccepted(t *testing.T) {
	v := newValidator()
	res, err := v.ValidateSignature(signedRRset(), validSig(), dnsNow)
	if err != nil {
		t.Fatalf("ValidateSignature returned unexpected error: %v", err)
	}
	if !res.Secure {
		t.Fatalf("a valid signature must yield a secure result, got %+v", res)
	}
}

func TestDSKeyTagMismatchRejected(t *testing.T) {
	v := newValidator()
	key := task.DNSKEY{Flags: 257, Protocol: 3, Algorithm: 13, KeyTag: 1234}
	ds := task.DS{KeyTag: 9999, Algorithm: 13, DigestType: 2, Digest: "abcd"}
	ok, err := v.CheckKey(key, ds)
	if err != nil {
		t.Fatalf("CheckKey returned unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("a DS with a different key tag must not match the DNSKEY")
	}
}
