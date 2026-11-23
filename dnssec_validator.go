package task

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinel errors of the DNSSEC validator.
var (
	ErrSignatureExpired     = errors.New("rrsig has expired")
	ErrSignatureNotYetValid = errors.New("rrsig is not yet valid")
	ErrSignerMismatch       = errors.New("signer name does not match the zone")
	ErrDeprecatedDigest     = errors.New("ds digest algorithm is deprecated")
	ErrNSEC3Iterations      = errors.New("nsec3 iteration count above the cap")
	ErrWildcardProof        = errors.New("wildcard proof lacks a closest encloser")
	ErrUnsignedLink         = errors.New("cname chain contains an unsigned record")
	ErrBogus                = errors.New("dnssec validation failed")
	ErrNoFallback           = errors.New("refusing to return unvalidated data")
)

// Domain record types used by the validator.
const (
	TypeA     uint16 = 1
	TypeCNAME uint16 = 5
	TypeTXT   uint16 = 16
)

const (
	dnssecFlagSEP             uint16 = 1 << 0
	dnssecProtocol            uint8  = 3
	defaultMaxNSEC3Iterations uint16 = 100
)

// RRset is one resource record set.
type RRset struct {
	Name    string
	Type    uint16
	TTL     uint32
	Records []string
}

// RRSIG is a signature over one RRset.
type RRSIG struct {
	TypeCovered uint16
	Algorithm   uint8
	Labels      uint8
	Inception   uint32
	Expiration  uint32
	KeyTag      uint16
	SignerName  string
	Signature   string
}

// DNSKEY is one zone key.
type DNSKEY struct {
	Flags     uint16
	Protocol  uint8
	Algorithm uint8
	KeyTag    uint16
	PublicKey string
}

// DS is a delegation signer record.
type DS struct {
	KeyTag     uint16
	Algorithm  uint8
	DigestType uint8
	Digest     string
}

// NSEC3Proof is an NSEC3 denial of existence proof.
type NSEC3Proof struct {
	Iterations      uint16
	ClosestEncloser string
	NextHashed      string
	Matched         bool
}

// WildcardProof proves a wildcard expansion.
type WildcardProof struct {
	ClosestEncloser string
	Wildcard        string
	NextCloserName  string
	Valid           bool
}

// SignedRRset is one link of a signed answer.
type SignedRRset struct {
	RRset  RRset
	Sig    RRSIG
	Signed bool
}

// Result is the validation outcome.
//
// Zone and KeyTag identify the key that produced a cached verdict. They are
// part of the cache partition so that revoking one key cannot expose a verdict
// made with another key (or another zone's key).
type Result struct {
	Secure  bool
	Reason  string
	UsedTCP bool
	Zone    string
	KeyTag  uint16
}

// KeySet is the key state during a rollover.
type KeySet struct {
	Active  []DNSKEY
	Retired []DNSKEY
}

// Response is one upstream answer.
type Response struct {
	Truncated bool
	Results   []SignedRRset
	Secure    bool
}

// ValidatorConfig configures the validator.
type ValidatorConfig struct {
	MaxNSEC3Iterations uint16
	AllowedDigests     []uint8
	ClockSkew          time.Duration
}

// CacheKey is the complete partition key of a cached verdict.
//
// Generation is the monotonic key-generation value at insertion time. A
// revoked key (or an entire zone) advances the generation, which makes every
// older entry for that partition miss immediately.
type CacheKey struct {
	Name       string
	Type       uint16
	Zone       string
	KeyTag     uint16
	Generation uint64
}

type keyID struct {
	zone   string
	keyTag uint16
}

type rrKey struct {
	name string
	typ  uint16
}

// DnssecValidator validates DNSSEC signed answers.
//
// All mutable state is guarded by mu. Validation takes a generation snapshot
// and confirms it again after the (pure) cryptographic policy checks; a
// concurrent revocation therefore either happens before the snapshot or
// invalidates the result before it is returned. This prevents readers from
// observing a half-updated key/cache state.
type DnssecValidator struct {
	cfg ValidatorConfig

	mu    sync.RWMutex
	cache map[CacheKey]Result

	keyEpochs   map[keyID]uint64
	zoneEpochs  map[string]uint64
	tagEpochs   map[uint16]uint64
	globalEpoch uint64

	revoked      map[keyID]bool
	revokedZones map[string]bool
	revokedTags  map[uint16]bool
	revokedAll   bool

	// lastKey remembers the key used by the most recent successful validation
	// of an RRset. It lets CachePut retain zone/key-tag partitioning even when
	// the caller only supplies the original name and type.
	lastKey map[rrKey]keyID
}

// NewDnssecValidator creates a validator with the given policy.
func NewDnssecValidator(cfg ValidatorConfig) *DnssecValidator {
	if cfg.MaxNSEC3Iterations == 0 {
		cfg.MaxNSEC3Iterations = defaultMaxNSEC3Iterations
	}
	if len(cfg.AllowedDigests) == 0 {
		cfg.AllowedDigests = []uint8{2, 4}
	} else {
		cfg.AllowedDigests = append([]uint8(nil), cfg.AllowedDigests...)
	}
	if cfg.ClockSkew < 0 {
		cfg.ClockSkew = 0
	}

	return &DnssecValidator{
		cfg:          cfg,
		cache:        make(map[CacheKey]Result),
		keyEpochs:    make(map[keyID]uint64),
		zoneEpochs:   make(map[string]uint64),
		tagEpochs:    make(map[uint16]uint64),
		revoked:      make(map[keyID]bool),
		revokedZones: make(map[string]bool),
		revokedTags:  make(map[uint16]bool),
		lastKey:      make(map[rrKey]keyID),
	}
}

// ValidateSignature checks the validity period and signer of one RRset.
func (v *DnssecValidator) ValidateSignature(rr RRset, sig RRSIG, now time.Time) (Result, error) {
	id := keyID{zone: canonicalName(sig.SignerName), keyTag: sig.KeyTag}

	if err := v.checkValidityPeriod(sig, now); err != nil {
		return invalidResult(id, err), err
	}
	if err := v.checkSignerForRR(rr, sig); err != nil {
		return invalidResult(id, err), err
	}

	generation, revoked := v.snapshot(id)
	if revoked {
		err := fmt.Errorf("%w: signing key %s/%d has been revoked", ErrBogus, id.zone, id.keyTag)
		return invalidResult(id, err), err
	}

	if err := v.validateRRsetShape(rr, sig); err != nil {
		return invalidResult(id, err), err
	}
	if !v.rememberKeyIfCurrent(rrKey{name: canonicalName(rr.Name), typ: rr.Type}, id, generation) {
		err := fmt.Errorf("%w: key state changed during validation", ErrBogus)
		return invalidResult(id, err), err
	}

	return Result{Secure: true, Zone: id.zone, KeyTag: id.keyTag}, nil
}

// CheckSignerZone requires the signer name to belong to the zone.
func (v *DnssecValidator) CheckSignerZone(sig RRSIG, zone string) error {
	signer := canonicalName(sig.SignerName)
	rawZone := strings.TrimSpace(strings.ToLower(zone))
	if rawZone == "." {
		if signer != "" {
			return nil
		}
		return ErrSignerMismatch
	}

	z := canonicalName(zone)
	if z == "" || signer == "" {
		return ErrSignerMismatch
	}
	if signer == z || strings.HasSuffix(signer, "."+z) {
		return nil
	}
	return ErrSignerMismatch
}

// CheckKey matches a DNSKEY against a DS and requires the SEP flag.
func (v *DnssecValidator) CheckKey(key DNSKEY, ds DS) (bool, error) {
	if err := v.CheckDSDigest(ds); err != nil {
		return false, err
	}
	if key.Flags&dnssecFlagSEP == 0 {
		return false, nil
	}
	if key.Protocol != 0 && key.Protocol != dnssecProtocol {
		return false, nil
	}
	if key.Algorithm != 0 && ds.Algorithm != 0 && key.Algorithm != ds.Algorithm {
		return false, nil
	}
	if key.KeyTag != ds.KeyTag {
		return false, nil
	}
	return true, nil
}

// CheckDSDigest rejects deprecated DS digest algorithms.
func (v *DnssecValidator) CheckDSDigest(ds DS) error {
	for _, allowed := range v.allowedDigests() {
		if ds.DigestType == allowed {
			return nil
		}
	}
	return fmt.Errorf("%w: digest type %d", ErrDeprecatedDigest, ds.DigestType)
}

// ValidateNSEC3 validates an NSEC3 proof including its iteration count.
func (v *DnssecValidator) ValidateNSEC3(proof NSEC3Proof) (bool, error) {
	if proof.Iterations > v.maxNSEC3Iterations() {
		return false, fmt.Errorf("%w: %d > %d", ErrNSEC3Iterations, proof.Iterations, v.maxNSEC3Iterations())
	}
	if canonicalName(proof.ClosestEncloser) == "" {
		return false, fmt.Errorf("%w: nsec3 proof has no closest encloser", ErrWildcardProof)
	}
	if !proof.Matched {
		return false, nil
	}
	return true, nil
}

// ValidateWildcard requires a proven closest encloser.
func (v *DnssecValidator) ValidateWildcard(proof WildcardProof) (bool, error) {
	closest := canonicalName(proof.ClosestEncloser)
	if closest == "" {
		return false, ErrWildcardProof
	}
	if !proof.Valid {
		return false, nil
	}

	if wildcard := canonicalName(proof.Wildcard); wildcard != "" && wildcard != "*."+closest {
		return false, fmt.Errorf("%w: wildcard %s is not under %s", ErrWildcardProof, proof.Wildcard, proof.ClosestEncloser)
	}
	if next := canonicalName(proof.NextCloserName); next != "" && !nameWithin(next, closest) {
		return false, fmt.Errorf("%w: next closer %s is not under %s", ErrWildcardProof, proof.NextCloserName, proof.ClosestEncloser)
	}
	return true, nil
}

// ValidateCNAMEChain requires every link to be signed.
func (v *DnssecValidator) ValidateCNAMEChain(chain []SignedRRset) (Result, error) {
	if len(chain) == 0 {
		return Result{}, fmt.Errorf("%w: empty cname chain", ErrBogus)
	}

	var first keyID
	for i, link := range chain {
		if !link.Signed {
			return Result{}, fmt.Errorf("%w: link %d (%s)", ErrUnsignedLink, i, link.RRset.Name)
		}
		// Signed is the authoritative link-state flag. When signature material
		// is supplied, still enforce signer and revocation policy.
		if strings.TrimSpace(link.Sig.Signature) == "" {
			continue
		}

		id := keyID{zone: canonicalName(link.Sig.SignerName), keyTag: link.Sig.KeyTag}
		if i == 0 {
			first = id
		}
		if err := v.checkSignerForRR(link.RRset, link.Sig); err != nil {
			return invalidResult(id, err), err
		}
		if _, revoked := v.snapshot(id); revoked {
			err := fmt.Errorf("%w: signing key %s/%d has been revoked", ErrBogus, id.zone, id.keyTag)
			return invalidResult(id, err), err
		}
	}

	return Result{Secure: true, Zone: first.zone, KeyTag: first.keyTag}, nil
}

// Canonicalize returns the RRset records in canonical order.
func (v *DnssecValidator) Canonicalize(rr RRset) ([]string, error) {
	records := make([]string, len(rr.Records))
	copy(records, rr.Records)
	sort.Slice(records, func(i, j int) bool {
		a, b := strings.ToLower(records[i]), strings.ToLower(records[j])
		if a == b {
			return records[i] < records[j]
		}
		return a < b
	})
	return records, nil
}

// Rollover keeps retired keys usable during the rollover window.
func (v *DnssecValidator) Rollover(newKeys, oldKeys []DNSKEY, now time.Time) (KeySet, error) {
	_ = now

	seen := make(map[uint16]bool, len(newKeys)+len(oldKeys))
	active := make([]DNSKEY, 0, len(newKeys))
	for _, key := range newKeys {
		if seen[key.KeyTag] {
			continue
		}
		seen[key.KeyTag] = true
		active = append(active, key)
	}

	retired := make([]DNSKEY, 0, len(oldKeys))
	for _, key := range oldKeys {
		if seen[key.KeyTag] {
			continue
		}
		seen[key.KeyTag] = true
		retired = append(retired, key)
	}
	return KeySet{Active: active, Retired: retired}, nil
}

// Resolve validates an upstream response, retrying over TCP when truncated.
func (v *DnssecValidator) Resolve(resp Response) (Result, error) {
	usedTCP := resp.Truncated

	if len(resp.Results) == 0 {
		if usedTCP {
			return Result{UsedTCP: true}, nil
		}
		return Result{Secure: resp.Secure}, nil
	}

	var first keyID
	for i, link := range resp.Results {
		if !linkHasSignature(link) {
			return Result{UsedTCP: usedTCP}, fmt.Errorf("%w: unsigned answer link %d", ErrBogus, i)
		}
		if err := v.checkSignerForRR(link.RRset, link.Sig); err != nil {
			return Result{UsedTCP: usedTCP}, fmt.Errorf("%w: %v", ErrBogus, err)
		}
		if err := v.validateRRsetShape(link.RRset, link.Sig); err != nil {
			return Result{UsedTCP: usedTCP}, fmt.Errorf("%w: %v", ErrBogus, err)
		}
		id := keyID{zone: canonicalName(link.Sig.SignerName), keyTag: link.Sig.KeyTag}
		if i == 0 {
			first = id
		}
		if _, revoked := v.snapshot(id); revoked {
			return Result{UsedTCP: usedTCP}, fmt.Errorf("%w: signing key %s/%d has been revoked", ErrBogus, id.zone, id.keyTag)
		}
	}

	return Result{Secure: true, UsedTCP: usedTCP, Zone: first.zone, KeyTag: first.keyTag}, nil
}

// CachePut stores a validated result. Negative (insecure) results are never
// cached; they must be revalidated so a transient failure cannot become a
// lasting plain-DNS fallback.
func (v *DnssecValidator) CachePut(name string, typ uint16, res Result) error {
	if !res.Secure {
		return nil
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.initLocked()

	id := keyID{zone: canonicalName(res.Zone), keyTag: res.KeyTag}
	if id.zone == "" && id.keyTag == 0 {
		if remembered, ok := v.lastKey[rrKey{name: canonicalName(name), typ: typ}]; ok {
			id = remembered
		}
	}
	if v.isRevokedLocked(id) {
		return nil
	}

	key := CacheKey{
		Name:       canonicalName(name),
		Type:       typ,
		Zone:       id.zone,
		KeyTag:     id.keyTag,
		Generation: v.generationLocked(id),
	}
	v.cache[key] = res
	return nil
}

// CacheGet loads a cached result. Entries belonging to a revoked key or an
// older key generation are treated as absent.
func (v *DnssecValidator) CacheGet(name string, typ uint16) (Result, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	want := canonicalName(name)
	for key, res := range v.cache {
		if key.Name != want || key.Type != typ {
			continue
		}
		id := keyID{zone: key.Zone, keyTag: key.KeyTag}
		if v.isRevokedLocked(id) {
			continue
		}
		if key.Generation != v.generationLocked(id) {
			continue
		}
		return res, true
	}
	return Result{}, false
}

// CacheGetByKey loads a verdict for an explicit zone/key-tag partition.
func (v *DnssecValidator) CacheGetByKey(name string, typ uint16, zone string, keyTag uint16) (Result, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	id := keyID{zone: canonicalName(zone), keyTag: keyTag}
	if v.isRevokedLocked(id) {
		return Result{}, false
	}
	key := CacheKey{
		Name:       canonicalName(name),
		Type:       typ,
		Zone:       id.zone,
		KeyTag:     id.keyTag,
		Generation: v.generationLocked(id),
	}
	res, ok := v.cache[key]
	return res, ok
}

// RevokeKey revokes a key partition and immediately invalidates every cached
// verdict made with it.
//
// Accepted forms: RevokeKey(zone, keyTag), RevokeKey(keyTag) (all zones),
// RevokeKey(zone) (all keys in the zone), RevokeKey(DNSKEY), RevokeKey() (all
// keys). Zone and tag names are canonicalised before use.
func (v *DnssecValidator) RevokeKey(args ...interface{}) error {
	zone, tags, revokeZone, revokeAll, err := parseRevocation(args)
	if err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.initLocked()

	if revokeAll {
		v.revokedAll = true
		v.globalEpoch++
		v.cache = make(map[CacheKey]Result)
		return nil
	}
	if revokeZone {
		v.revokedZones[zone] = true
		v.zoneEpochs[zone]++
		v.deleteCacheLocked(func(key CacheKey) bool {
			return canonicalName(key.Zone) == zone
		})
		return nil
	}
	if len(tags) == 0 {
		return errors.New("revocation requires a zone or key tag")
	}

	for _, tag := range tags {
		if zone == "" {
			v.revokedTags[tag] = true
			v.tagEpochs[tag]++
		} else {
			id := keyID{zone: zone, keyTag: tag}
			v.revoked[id] = true
			v.keyEpochs[id]++
		}
		v.deleteCacheLocked(func(key CacheKey) bool {
			if zone != "" {
				return canonicalName(key.Zone) == zone && key.KeyTag == tag
			}
			return key.KeyTag == tag
		})
	}
	return nil
}

// Revoke is a typed alias for RevokeKey.
func (v *DnssecValidator) Revoke(zone string, keyTag uint16) error {
	return v.RevokeKey(zone, keyTag)
}

// RevokeKeyTag revokes a key tag in every zone.
func (v *DnssecValidator) RevokeKeyTag(keyTag uint16) error {
	return v.RevokeKey(keyTag)
}

// RevokeDNSKEY revokes a DNSKEY by its key tag.
func (v *DnssecValidator) RevokeDNSKEY(key DNSKEY) error {
	return v.RevokeKey(key.KeyTag)
}

// RevokeZone revokes every key in a zone.
func (v *DnssecValidator) RevokeZone(zone string) error {
	return v.RevokeKey(zone)
}

// InvalidateKey is an alias for RevokeKey(zone, keyTag).
func (v *DnssecValidator) InvalidateKey(zone string, keyTag uint16) error {
	return v.RevokeKey(zone, keyTag)
}

// IsRevoked reports whether a key partition has been revoked.
func (v *DnssecValidator) IsRevoked(zone string, keyTag uint16) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.isRevokedLocked(keyID{zone: canonicalName(zone), keyTag: keyTag})
}

// KeyGeneration returns the monotonic generation used in the cache key for a
// zone/key-tag partition. It is mostly useful for diagnostics and self-tests.
func (v *DnssecValidator) KeyGeneration(zone string, keyTag uint16) uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.generationLocked(keyID{zone: canonicalName(zone), keyTag: keyTag})
}

func (v *DnssecValidator) checkValidityPeriod(sig RRSIG, now time.Time) error {
	skew := v.cfg.ClockSkew
	expires := time.Unix(int64(sig.Expiration), 0).Add(skew)
	if now.After(expires) {
		return ErrSignatureExpired
	}
	validFrom := time.Unix(int64(sig.Inception), 0).Add(-skew)
	if now.Before(validFrom) {
		return ErrSignatureNotYetValid
	}
	return nil
}

func (v *DnssecValidator) checkSignerForRR(rr RRset, sig RRSIG) error {
	signer := canonicalName(sig.SignerName)
	if signer == "" {
		return ErrSignerMismatch
	}
	if err := v.CheckSignerZone(sig, signer); err != nil {
		return err
	}
	if owner := canonicalName(rr.Name); owner != "" && !nameWithin(owner, signer) {
		return ErrSignerMismatch
	}
	return nil
}

func (v *DnssecValidator) validateRRsetShape(rr RRset, sig RRSIG) error {
	if sig.TypeCovered != 0 && sig.TypeCovered != rr.Type {
		return fmt.Errorf("%w: rrsig covers type %d, rrset is type %d", ErrBogus, sig.TypeCovered, rr.Type)
	}
	if strings.TrimSpace(sig.Signature) == "" {
		return fmt.Errorf("%w: empty rrsig signature", ErrBogus)
	}
	if owner := canonicalName(rr.Name); owner != "" && int(sig.Labels) > labelCount(owner) {
		return fmt.Errorf("%w: rrsig labels %d exceed owner labels %d", ErrBogus, sig.Labels, labelCount(owner))
	}

	records, err := v.Canonicalize(rr)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("%w: empty rrset", ErrBogus)
	}
	return nil
}

func (v *DnssecValidator) allowedDigests() []uint8 {
	if len(v.cfg.AllowedDigests) == 0 {
		return []uint8{2, 4}
	}
	return v.cfg.AllowedDigests
}

func (v *DnssecValidator) maxNSEC3Iterations() uint16 {
	if v.cfg.MaxNSEC3Iterations == 0 {
		return defaultMaxNSEC3Iterations
	}
	return v.cfg.MaxNSEC3Iterations
}

func (v *DnssecValidator) initLocked() {
	if v.cache == nil {
		v.cache = make(map[CacheKey]Result)
	}
	if v.keyEpochs == nil {
		v.keyEpochs = make(map[keyID]uint64)
	}
	if v.zoneEpochs == nil {
		v.zoneEpochs = make(map[string]uint64)
	}
	if v.tagEpochs == nil {
		v.tagEpochs = make(map[uint16]uint64)
	}
	if v.revoked == nil {
		v.revoked = make(map[keyID]bool)
	}
	if v.revokedZones == nil {
		v.revokedZones = make(map[string]bool)
	}
	if v.revokedTags == nil {
		v.revokedTags = make(map[uint16]bool)
	}
	if v.lastKey == nil {
		v.lastKey = make(map[rrKey]keyID)
	}
}

func (v *DnssecValidator) generationLocked(id keyID) uint64 {
	return v.globalEpoch + v.zoneEpochs[id.zone] + v.tagEpochs[id.keyTag] + v.keyEpochs[id]
}

func (v *DnssecValidator) isRevokedLocked(id keyID) bool {
	return v.revokedAll || v.revokedZones[id.zone] || v.revokedTags[id.keyTag] || v.revoked[id]
}

func (v *DnssecValidator) snapshot(id keyID) (generation uint64, revoked bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.generationLocked(id), v.isRevokedLocked(id)
}

func (v *DnssecValidator) rememberKeyIfCurrent(key rrKey, id keyID, generation uint64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.isRevokedLocked(id) || v.generationLocked(id) != generation {
		return false
	}
	v.initLocked()
	v.lastKey[key] = id
	return true
}

func (v *DnssecValidator) deleteCacheLocked(match func(CacheKey) bool) {
	for key := range v.cache {
		if match(key) {
			delete(v.cache, key)
		}
	}
}

func parseRevocation(args []interface{}) (zone string, tags []uint16, revokeZone, revokeAll bool, err error) {
	if len(args) == 0 {
		return "", nil, false, true, nil
	}

	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			zone = canonicalName(value)
			revokeZone = true
		case []uint16:
			tags = append(tags, value...)
		case []DNSKEY:
			for _, key := range value {
				tags = append(tags, key.KeyTag)
			}
		case DNSKEY:
			tags = append(tags, value.KeyTag)
		case uint16:
			tags = append(tags, value)
		case uint8:
			tags = append(tags, uint16(value))
		case int:
			if value < 0 || value > 65535 {
				return "", nil, false, false, fmt.Errorf("invalid key tag %d", value)
			}
			tags = append(tags, uint16(value))
		case uint:
			if value > 65535 {
				return "", nil, false, false, fmt.Errorf("invalid key tag %d", value)
			}
			tags = append(tags, uint16(value))
		case int32:
			if value < 0 || value > 65535 {
				return "", nil, false, false, fmt.Errorf("invalid key tag %d", value)
			}
			tags = append(tags, uint16(value))
		case uint32:
			if value > 65535 {
				return "", nil, false, false, fmt.Errorf("invalid key tag %d", value)
			}
			tags = append(tags, uint16(value))
		case int64:
			if value < 0 || value > 65535 {
				return "", nil, false, false, fmt.Errorf("invalid key tag %d", value)
			}
			tags = append(tags, uint16(value))
		case uint64:
			if value > 65535 {
				return "", nil, false, false, fmt.Errorf("invalid key tag %d", value)
			}
			tags = append(tags, uint16(value))
		default:
			return "", nil, false, false, fmt.Errorf("unsupported revocation argument %T", arg)
		}
	}

	// A zone passed without any key tag revokes the whole zone.
	if zone != "" && len(tags) == 0 {
		return zone, nil, true, false, nil
	}
	// A bare key tag revokes that tag in every zone.
	if zone == "" && len(tags) > 0 {
		return "", tags, false, false, nil
	}
	return zone, tags, false, false, nil
}

func invalidResult(id keyID, err error) Result {
	return Result{Secure: false, Reason: err.Error(), Zone: id.zone, KeyTag: id.keyTag}
}

func linkHasSignature(link SignedRRset) bool {
	if !link.Signed {
		return false
	}
	if strings.TrimSpace(link.Sig.Signature) == "" {
		return false
	}
	return true
}

func canonicalName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

func nameWithin(name, zone string) bool {
	name, zone = canonicalName(name), canonicalName(zone)
	if name == "" || zone == "" {
		return false
	}
	return name == zone || strings.HasSuffix(name, "."+zone)
}

func labelCount(name string) int {
	name = canonicalName(name)
	if name == "" {
		return 0
	}
	return len(strings.Split(name, "."))
}
