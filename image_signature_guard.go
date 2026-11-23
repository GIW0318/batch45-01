package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// Sentinel errors returned by the guard. Every failed check produces a
// non-nil error so that callers can fail closed.
var (
	errUnsigned          = errors.New("image signature guard: signature is missing or empty")
	errIssuerNotAllowed  = errors.New("image signature guard: issuer is not in the allow list")
	errNoTrustRoot       = errors.New("image signature guard: no matching trust root for issuer and key")
	errRootNotActive     = errors.New("image signature guard: trust root is expired or not yet valid")
	errDigestMismatch    = errors.New("image signature guard: signature digest does not match the pulled object")
	errMissingObject     = errors.New("image signature guard: manifest or object is missing")
	errAttestation       = errors.New("image signature guard: attestation subject is not bound to the image digest")
	errCriticalExpired   = errors.New("image signature guard: signature critical expiry has lapsed")
	errCriticalUnknown   = errors.New("image signature guard: signature contains an unknown critical field")
	errCriticalMalformed = errors.New("image signature guard: signature critical field is malformed")
	errInvalidRoot       = errors.New("image signature guard: trust root must have an issuer and key id")
	errInvalidCacheKey   = errors.New("image signature guard: cache entry requires both ref and digest")
	errInvalidRootsData  = errors.New("image signature guard: malformed serialised trust roots")
)

// maxCacheEntries bounds the admission cache. Entries beyond the cap evict
// the oldest key so the cache always converges to a fixed memory footprint.
const maxCacheEntries = 4096

// criticalExpiresField is the only critical directive accepted. Any other
// key is an unknown critical field and must reject the signature.
const criticalExpiresField = "expires"

// TrustRoot is one trusted signing key for an issuer.
type TrustRoot struct {
	Issuer    string
	KeyID     string
	PublicKey string
	NotBefore time.Time
	NotAfter  time.Time
}

// Signature is a signature attached to a manifest.
type Signature struct {
	Issuer         string
	KeyID          string
	ManifestDigest string
	Critical       map[string]string
	SignedAt       time.Time
	Bytes          []byte
}

// Manifest is one architecture manifest of an image.
type Manifest struct {
	Digest            string
	Arch              string
	AttestationDigest string
}

// ImageRef identifies an image that is being pulled.
type ImageRef struct {
	Registry string
	Repo     string
	Tag      string
	Digest   string
	Arch     string
}

// Attestation is a build attestation bound to an image digest.
type Attestation struct {
	SubjectDigest string
	PayloadDigest string
}

// CacheEntry is a cached verification result.
type CacheEntry struct {
	Ref       string
	Digest    string
	Verified  bool
	ExpiresAt time.Time
}

// GuardPolicy is the admission policy of the signature guard.
type GuardPolicy struct {
	AllowedIssuers     []string
	RequireAttestation bool
	CacheTTL           time.Duration
}

// ImageSignatureGuard verifies image signatures before admission.
type ImageSignatureGuard struct {
	mu sync.RWMutex

	policy   GuardPolicy
	policyFP string
	roots    map[string]map[string]TrustRoot

	cacheMu sync.Mutex
	cache   map[cacheKey]cacheRecord
	order   []cacheKey
}

type cacheKey struct {
	ref    string
	digest string
}

type cacheRecord struct {
	entry    CacheEntry
	policyFP string
}

type rootsEnvelope struct {
	Version int         `json:"version"`
	Roots   []TrustRoot `json:"roots"`
}

// NewImageSignatureGuard returns a guard for the given policy.
func NewImageSignatureGuard(policy GuardPolicy) *ImageSignatureGuard {
	return &ImageSignatureGuard{
		policy:   policy,
		policyFP: fingerprintPolicy(policy),
		roots:    make(map[string]map[string]TrustRoot),
		cache:    make(map[cacheKey]cacheRecord),
	}
}

// fingerprintPolicy derives a version fingerprint from the policy fields that
// influence an admission conclusion. A cached allow decision is only reusable
// while the fingerprint stays identical.
func fingerprintPolicy(p GuardPolicy) string {
	issuers := append([]string(nil), p.AllowedIssuers...)
	sort.Strings(issuers)
	h := sha256.New()
	if p.RequireAttestation {
		h.Write([]byte("att=1\n"))
	} else {
		h.Write([]byte("att=0\n"))
	}
	h.Write([]byte("ttl="))
	h.Write([]byte(p.CacheTTL.String()))
	h.Write([]byte("\n"))
	for _, issuer := range issuers {
		h.Write([]byte(issuer))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// AddTrustRoot registers or rotates a trust root.
func (g *ImageSignatureGuard) AddTrustRoot(root TrustRoot) error {
	if root.Issuer == "" || root.KeyID == "" {
		return errInvalidRoot
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// Rotation semantics: an issuer has a single active key. Registering a new
	// key retires every previous key of that issuer, while re-adding the same
	// key id refreshes its material and validity window.
	g.roots[root.Issuer] = map[string]TrustRoot{root.KeyID: root}
	return nil
}

// TrustRoots returns the currently trusted roots.
func (g *ImageSignatureGuard) TrustRoots() []TrustRoot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	roots := make([]TrustRoot, 0)
	for _, byKey := range g.roots {
		for _, root := range byKey {
			roots = append(roots, root)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].Issuer != roots[j].Issuer {
			return roots[i].Issuer < roots[j].Issuer
		}
		return roots[i].KeyID < roots[j].KeyID
	})
	return roots
}

// Verify checks one signature against one manifest.
func (g *ImageSignatureGuard) Verify(ref ImageRef, sig Signature, man Manifest, now time.Time) error {
	// A missing object can never be admitted.
	if man.Digest == "" {
		return errMissingObject
	}
	// Empty signature bytes means the image is unsigned.
	if len(sig.Bytes) == 0 {
		return errUnsigned
	}
	// The issuer must be explicitly allowed by policy.
	if !g.issuerAllowed(sig.Issuer) {
		return errIssuerNotAllowed
	}
	// The signature must chain to a registered root for that exact issuer/key.
	root, err := g.lookupRoot(sig.Issuer, sig.KeyID)
	if err != nil {
		return err
	}
	// The root must be inside its validity window at verification time.
	if !rootActive(root, now) {
		return errRootNotActive
	}
	// The signature digest must be over the object actually being pulled.
	if sig.ManifestDigest == "" || sig.ManifestDigest != man.Digest {
		return errDigestMismatch
	}
	// Critical directives are mandatory: expired or unknown ones reject.
	return verifyCritical(sig.Critical, now)
}

// VerifyMultiArch verifies every architecture manifest of an image.
func (g *ImageSignatureGuard) VerifyMultiArch(ref ImageRef, mans []Manifest, sigs []Signature, now time.Time) error {
	if len(mans) == 0 {
		return errMissingObject
	}
	for _, man := range mans {
		var candidates []Signature
		for _, sig := range sigs {
			if sig.ManifestDigest == man.Digest {
				candidates = append(candidates, sig)
			}
		}
		if len(candidates) == 0 {
			return errUnsigned
		}
		verified := false
		for _, sig := range candidates {
			if err := g.Verify(ref, sig, man, now); err == nil {
				verified = true
				break
			}
		}
		if !verified {
			return errNoTrustRoot
		}
	}
	return nil
}

// VerifyAttestation checks that an attestation is bound to the image digest.
func (g *ImageSignatureGuard) VerifyAttestation(imageDigest string, att Attestation) error {
	if imageDigest == "" || att.SubjectDigest == "" || att.SubjectDigest != imageDigest {
		return errAttestation
	}
	return nil
}

// AdmitRef reports whether the image may be pulled.
func (g *ImageSignatureGuard) AdmitRef(ref ImageRef, sig Signature, man Manifest, att Attestation, now time.Time) bool {
	if man.Digest == "" {
		return false
	}
	refName := canonicalRef(ref)

	// Reuse a fresh cached conclusion bound to the current policy version.
	if cached, ok := g.CacheGet(refName, man.Digest, now); ok {
		return cached.Verified
	}

	ok := true
	if err := g.Verify(ref, sig, man, now); err != nil {
		ok = false
	}
	if ok && g.policy.RequireAttestation {
		if err := g.VerifyAttestation(man.Digest, att); err != nil {
			ok = false
		}
	}

	if g.policy.CacheTTL > 0 {
		_ = g.CachePut(CacheEntry{
			Ref:       refName,
			Digest:    man.Digest,
			Verified:  ok,
			ExpiresAt: now.Add(g.policy.CacheTTL),
		})
	}
	return ok
}

// CachePut stores a verification result.
func (g *ImageSignatureGuard) CachePut(e CacheEntry) error {
	if e.Ref == "" || e.Digest == "" {
		return errInvalidCacheKey
	}
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	key := cacheKey{ref: e.Ref, digest: e.Digest}
	if _, exists := g.cache[key]; !exists {
		g.order = append(g.order, key)
	}
	// Full-struct assignment under the lock keeps the entry atomic: concurrent
	// writers can never expose a half-written Verified flag or expiry.
	g.cache[key] = cacheRecord{entry: e, policyFP: g.policyFP}
	g.evictLocked()
	return nil
}

// CacheGet returns a cached verification result if it is still valid.
func (g *ImageSignatureGuard) CacheGet(ref, digest string, now time.Time) (CacheEntry, bool) {
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	key := cacheKey{ref: ref, digest: digest}
	record, ok := g.cache[key]
	if !ok {
		return CacheEntry{}, false
	}
	// Conclusions are bound to the policy version that produced them.
	if record.policyFP != g.policyFP {
		delete(g.cache, key)
		return CacheEntry{}, false
	}
	// A zero expiry means "no expiry"; otherwise the entry must be fresh.
	if !record.entry.ExpiresAt.IsZero() && !now.Before(record.entry.ExpiresAt) {
		delete(g.cache, key)
		return CacheEntry{}, false
	}
	return record.entry, true
}

// ExportRoots serialises the trust roots.
func (g *ImageSignatureGuard) ExportRoots() ([]byte, error) {
	envelope := rootsEnvelope{Version: 1, Roots: g.TrustRoots()}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ImportRoots restores trust roots from a serialised form.
func (g *ImageSignatureGuard) ImportRoots(data []byte) error {
	var envelope rootsEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return errInvalidRootsData
	}
	if len(envelope.Roots) == 0 {
		return errInvalidRootsData
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	roots := make(map[string]map[string]TrustRoot, len(envelope.Roots))
	for _, root := range envelope.Roots {
		if root.Issuer == "" || root.KeyID == "" {
			return errInvalidRoot
		}
		if roots[root.Issuer] == nil {
			roots[root.Issuer] = make(map[string]TrustRoot)
		}
		roots[root.Issuer][root.KeyID] = root
	}
	g.roots = roots
	return nil
}

func (g *ImageSignatureGuard) issuerAllowed(issuer string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, allowed := range g.policy.AllowedIssuers {
		if allowed == issuer {
			return true
		}
	}
	return false
}

func (g *ImageSignatureGuard) lookupRoot(issuer, keyID string) (TrustRoot, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	byKey, ok := g.roots[issuer]
	if !ok {
		return TrustRoot{}, errNoTrustRoot
	}
	root, ok := byKey[keyID]
	if !ok {
		return TrustRoot{}, errNoTrustRoot
	}
	return root, nil
}

func rootActive(root TrustRoot, now time.Time) bool {
	if !root.NotBefore.IsZero() && now.Before(root.NotBefore) {
		return false
	}
	if !root.NotAfter.IsZero() && !now.Before(root.NotAfter) {
		return false
	}
	return true
}

func verifyCritical(critical map[string]string, now time.Time) error {
	for key, value := range critical {
		if key != criticalExpiresField {
			return errCriticalUnknown
		}
		expires, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return errCriticalMalformed
		}
		if !now.Before(expires) {
			return errCriticalExpired
		}
	}
	return nil
}

func canonicalRef(ref ImageRef) string {
	return ref.Registry + "/" + ref.Repo + ":" + ref.Tag
}

// evictLocked keeps the cache at or below maxCacheEntries, dropping the
// oldest insertion-order keys first. Stale order entries left behind by lazy
// expiry are skipped and consumed during eviction.
func (g *ImageSignatureGuard) evictLocked() {
	for len(g.cache) > maxCacheEntries && len(g.order) > 0 {
		oldest := g.order[0]
		g.order = g.order[1:]
		delete(g.cache, oldest)
	}
}
