package task

import (
	"errors"
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
type Result struct {
	Secure  bool
	Reason  string
	UsedTCP bool
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

// DnssecValidator validates DNSSEC signed answers.
type DnssecValidator struct {
}

// NewDnssecValidator creates a validator with the given policy.
func NewDnssecValidator(cfg ValidatorConfig) *DnssecValidator {
	return &DnssecValidator{}
}

// ValidateSignature checks the validity period and signer of one RRset.
func (v *DnssecValidator) ValidateSignature(rr RRset, sig RRSIG, now time.Time) (Result, error) {
	return Result{}, errors.New("not implemented")
}

// CheckSignerZone requires the signer name to belong to the zone.
func (v *DnssecValidator) CheckSignerZone(sig RRSIG, zone string) error {
	return errors.New("not implemented")
}

// CheckKey matches a DNSKEY against a DS and requires the SEP flag.
func (v *DnssecValidator) CheckKey(key DNSKEY, ds DS) (bool, error) {
	return false, errors.New("not implemented")
}

// CheckDSDigest rejects deprecated DS digest algorithms.
func (v *DnssecValidator) CheckDSDigest(ds DS) error {
	return errors.New("not implemented")
}

// ValidateNSEC3 validates an NSEC3 proof including its iteration count.
func (v *DnssecValidator) ValidateNSEC3(proof NSEC3Proof) (bool, error) {
	return false, errors.New("not implemented")
}

// ValidateWildcard requires a proven closest encloser.
func (v *DnssecValidator) ValidateWildcard(proof WildcardProof) (bool, error) {
	return false, errors.New("not implemented")
}

// ValidateCNAMEChain requires every link to be signed.
func (v *DnssecValidator) ValidateCNAMEChain(chain []SignedRRset) (Result, error) {
	return Result{}, errors.New("not implemented")
}

// Canonicalize returns the RRset records in canonical order.
func (v *DnssecValidator) Canonicalize(rr RRset) ([]string, error) {
	return nil, errors.New("not implemented")
}

// Rollover keeps retired keys usable during the rollover window.
func (v *DnssecValidator) Rollover(newKeys, oldKeys []DNSKEY, now time.Time) (KeySet, error) {
	return KeySet{}, errors.New("not implemented")
}

// Resolve validates an upstream response, retrying over TCP when truncated.
func (v *DnssecValidator) Resolve(resp Response) (Result, error) {
	return Result{}, errors.New("not implemented")
}

// CachePut stores a validated result.
func (v *DnssecValidator) CachePut(name string, typ uint16, res Result) error {
	return errors.New("not implemented")
}

// CacheGet loads a cached result.
func (v *DnssecValidator) CacheGet(name string, typ uint16) (Result, bool) {
	return Result{}, false
}
