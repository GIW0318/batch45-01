package task

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// Sentinel errors of the chain builder.
var (
	ErrNoTrustAnchor             = errors.New("no trusted path to a trust anchor")
	ErrChainTooDeep              = errors.New("chain exceeds max depth")
	ErrExpired                   = errors.New("certificate is expired")
	ErrPathLenViolation          = errors.New("path length constraint violated")
	ErrNameConstraintViolation   = errors.New("name constraint violated")
	ErrPolicyViolation           = errors.New("policy constraint violated")
	ErrRevoked                   = errors.New("certificate is revoked")
	ErrAnchorFingerprintMismatch = errors.New("trust anchor fingerprint mismatch")
	ErrMissingIntermediate       = errors.New("intermediate certificate missing")
	ErrSearchLimitExceeded       = errors.New("path search space exhausted: too many candidate paths")
)

// maxSearchNodes bounds how many path nodes one Build may expand. Cross
// signing can make the number of simple paths explode combinatorially; the
// search is truncated in a deterministic order once the budget is spent.
const maxSearchNodes = 4096

// Cert is the subset of a certificate the builder reasons about.
type Cert struct {
	Subject          string
	Issuer           string
	Serial           string
	Fingerprint      string
	SubjectKeyID     string
	AuthorityKeyID   string
	IsCA             bool
	SelfSigned       bool
	PathLen          int
	NotBefore        time.Time
	NotAfter         time.Time
	SANs             []string
	Policies         []string
	RequiredPolicies []string
	PermittedDNS     []string
	Revoked          bool
	SignatureValid   bool
}

// BuilderOptions configures one chain build.
type BuilderOptions struct {
	MaxDepth      int
	AllowAIA      bool
	CurrentTime   time.Time
	PinnedAnchors []string
}

// ChainBuilder builds certificate paths to a pinned trust anchor.
type ChainBuilder struct {
	opts BuilderOptions
}

// NewChainBuilder creates a builder with the given policy.
func NewChainBuilder(opts BuilderOptions) *ChainBuilder {
	return &ChainBuilder{opts: opts}
}

// Build returns the chain from the leaf up to the trust anchor.
func (b *ChainBuilder) Build(leaf Cert, intermediates []Cert, anchors []Cert) ([]Cert, error) {
	now := b.opts.CurrentTime
	if now.IsZero() {
		now = time.Now()
	}

	if !leaf.SignatureValid {
		return nil, ErrNoTrustAnchor
	}
	if err := checkValidity(leaf, now); err != nil {
		return nil, err
	}

	pool := dedupeAndSort(intermediates)
	anchorPool := dedupeAndSort(anchors)

	// A self signed leaf is its own issuer: it only validates when it is
	// itself a trusted anchor, never by virtue of being self signed.
	if leaf.SelfSigned || leaf.Issuer == leaf.Subject {
		if b.isTrustedAnchor(leaf, anchorPool) {
			chain := []Cert{leaf}
			if err := checkPolicies(chain); err != nil {
				return nil, err
			}
			return chain, nil
		}
		return nil, ErrNoTrustAnchor
	}

	s := &search{
		builder:       b,
		now:           now,
		intermediates: pool,
		anchors:       anchorPool,
		onPath:        map[string]bool{certKey(leaf): true},
		maxDepth:      b.opts.MaxDepth,
	}
	return s.extend([]Cert{leaf})
}

// isTrustedAnchor reports whether c may terminate a path: it must come from
// the anchor pool and, when pins are configured, match a pinned fingerprint.
func (b *ChainBuilder) isTrustedAnchor(c Cert, anchorPool []Cert) bool {
	inPool := false
	for _, a := range anchorPool {
		if certKey(a) == certKey(c) {
			inPool = true
			break
		}
	}
	if !inPool {
		return false
	}
	if len(b.opts.PinnedAnchors) > 0 && !containsString(b.opts.PinnedAnchors, c.Fingerprint) {
		return false
	}
	return true
}

// search carries the state of one depth first path exploration.
type search struct {
	builder       *ChainBuilder
	now           time.Time
	intermediates []Cert
	anchors       []Cert
	onPath        map[string]bool
	maxDepth      int
	nodes         int
}

// extend tries to grow chain until a trusted anchor terminates it. Anchor
// candidates are tried before intermediates, both in sorted order, so the
// result and any truncation point are deterministic for a given input.
func (s *search) extend(chain []Cert) ([]Cert, error) {
	s.nodes++
	if s.nodes > maxSearchNodes {
		return nil, ErrSearchLimitExceeded
	}
	if s.maxDepth > 0 && len(chain) > s.maxDepth {
		return nil, ErrChainTooDeep
	}

	cur := chain[len(chain)-1]
	need := cur.Issuer
	matched := false
	var best error

	for i := range s.anchors {
		a := s.anchors[i]
		if a.Subject != need || !keyIDMatch(cur, a) {
			continue
		}
		matched = true
		if err := s.acceptAnchor(a, chain); err != nil {
			best = preferError(best, err)
			continue
		}
		full := append(append([]Cert{}, chain...), a)
		if err := checkPolicies(full); err != nil {
			best = preferError(best, err)
			continue
		}
		return full, nil
	}

	for i := range s.intermediates {
		c := s.intermediates[i]
		if c.Subject != need || !keyIDMatch(cur, c) {
			continue
		}
		matched = true
		key := certKey(c)
		if s.onPath[key] {
			continue
		}
		if err := s.checkIntermediate(c, chain); err != nil {
			best = preferError(best, err)
			continue
		}
		s.onPath[key] = true
		next := append(append([]Cert{}, chain...), c)
		res, err := s.extend(next)
		delete(s.onPath, key)
		if err == nil {
			return res, nil
		}
		if errors.Is(err, ErrSearchLimitExceeded) {
			return nil, err
		}
		best = preferError(best, err)
	}

	if best != nil {
		return nil, best
	}
	if !matched {
		return nil, ErrMissingIntermediate
	}
	return nil, ErrNoTrustAnchor
}

func (s *search) acceptAnchor(a Cert, chain []Cert) error {
	if len(s.builder.opts.PinnedAnchors) > 0 && !containsString(s.builder.opts.PinnedAnchors, a.Fingerprint) {
		return ErrAnchorFingerprintMismatch
	}
	if !a.SignatureValid {
		return ErrNoTrustAnchor
	}
	if err := checkValidity(a, s.now); err != nil {
		return err
	}
	return checkCAConstraints(a, chain)
}

func (s *search) checkIntermediate(c Cert, chain []Cert) error {
	if !c.SignatureValid {
		return ErrNoTrustAnchor
	}
	if !c.IsCA {
		return ErrNoTrustAnchor
	}
	if err := checkValidity(c, s.now); err != nil {
		return err
	}
	return checkCAConstraints(c, chain)
}

// checkValidity enforces the validity window and revocation status of one
// certificate; every chain element goes through it.
func checkValidity(c Cert, now time.Time) error {
	if now.Before(c.NotBefore) || now.After(c.NotAfter) {
		return ErrExpired
	}
	if c.Revoked {
		return ErrRevoked
	}
	return nil
}

// checkCAConstraints applies the path length and name constraints of a CA
// certificate to every certificate below it on the path.
func checkCAConstraints(ca Cert, chainBelow []Cert) error {
	if ca.PathLen >= 0 {
		caBelow := 0
		for _, x := range chainBelow {
			if x.IsCA {
				caBelow++
			}
		}
		if caBelow > ca.PathLen {
			return ErrPathLenViolation
		}
	}
	if len(ca.PermittedDNS) > 0 {
		for _, x := range chainBelow {
			for _, san := range x.SANs {
				if !dnsAllowed(san, ca.PermittedDNS) {
					return ErrNameConstraintViolation
				}
			}
		}
	}
	return nil
}

// checkPolicies requires every policy named in any chain certificate's
// RequiredPolicies to be present in every certificate of the chain.
func checkPolicies(chain []Cert) error {
	for _, c := range chain {
		for _, req := range c.RequiredPolicies {
			for _, x := range chain {
				if !containsString(x.Policies, req) {
					return ErrPolicyViolation
				}
			}
		}
	}
	return nil
}

func dnsAllowed(name string, permitted []string) bool {
	for _, p := range permitted {
		p = strings.TrimPrefix(p, ".")
		if p == "" {
			continue
		}
		if name == p || strings.HasSuffix(name, "."+p) {
			return true
		}
	}
	return false
}

// keyIDMatch additionally binds issuer candidates by key identifier when
// both sides carry one.
func keyIDMatch(child, issuer Cert) bool {
	if child.AuthorityKeyID != "" && issuer.SubjectKeyID != "" {
		return child.AuthorityKeyID == issuer.SubjectKeyID
	}
	return true
}

func certKey(c Cert) string {
	return c.Subject + "\x00" + c.Serial + "\x00" + c.Fingerprint
}

// dedupeAndSort removes exact duplicates and orders candidates by
// (Subject, Serial, Fingerprint) so traversal order is deterministic.
func dedupeAndSort(in []Cert) []Cert {
	sorted := append([]Cert(nil), in...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Subject != sorted[j].Subject {
			return sorted[i].Subject < sorted[j].Subject
		}
		if sorted[i].Serial != sorted[j].Serial {
			return sorted[i].Serial < sorted[j].Serial
		}
		return sorted[i].Fingerprint < sorted[j].Fingerprint
	})
	seen := make(map[string]bool, len(sorted))
	out := make([]Cert, 0, len(sorted))
	for _, c := range sorted {
		key := certKey(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// preferError keeps the more specific of two branch failures so the caller
// sees ErrExpired/ErrRevoked/... instead of a generic ErrNoTrustAnchor.
func preferError(cur, next error) error {
	if cur == nil {
		return next
	}
	if next == nil {
		return cur
	}
	if errorRank(next) > errorRank(cur) {
		return next
	}
	return cur
}

func errorRank(err error) int {
	switch {
	case errors.Is(err, ErrSearchLimitExceeded):
		return 100
	case errors.Is(err, ErrChainTooDeep):
		return 90
	case errors.Is(err, ErrAnchorFingerprintMismatch):
		return 80
	case errors.Is(err, ErrPathLenViolation):
		return 70
	case errors.Is(err, ErrNameConstraintViolation):
		return 60
	case errors.Is(err, ErrPolicyViolation):
		return 50
	case errors.Is(err, ErrRevoked):
		return 40
	case errors.Is(err, ErrExpired):
		return 30
	case errors.Is(err, ErrNoTrustAnchor):
		return 20
	case errors.Is(err, ErrMissingIntermediate):
		return 10
	default:
		return 0
	}
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
