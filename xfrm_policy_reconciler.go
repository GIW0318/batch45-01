package task

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// IPsec policy and SA modes.
const (
	ModeTunnel    = "tunnel"
	ModeTransport = "transport"
)

// SA states.
const (
	SAStateActive  = "active"
	SAStateExpired = "expired"
)

// Policy is one XFRM policy.
type Policy struct {
	Name      string
	Src       string
	Dst       string
	Proto     string
	Mode      string
	Priority  int
	PFSGroup  string
	Algorithm string
	AuthAlg   string
	TunnelSrc string
	TunnelDst string
}

// SA is one XFRM state entry.
type SA struct {
	SPI          uint32
	PolicyName   string
	Src          string
	Dst          string
	ReplayWindow uint32
	Algorithm    string
	ExpiresAt    time.Time
	State        string
}

const (
	defaultSALifetime   = time.Hour
	defaultReplayWindow = 32
	firstSPI            = 0x100
)

var (
	errTunnelExists   = errors.New("tunnel already exists with different parameters")
	errTunnelNotFound = errors.New("tunnel not found")
	errSANotFound     = errors.New("sa not found")
	errSPIConflict    = errors.New("spi already in use")
)

// aeadAlgs are authenticated-encryption ciphers that must not be combined
// with a separate authentication algorithm.
var aeadAlgs = map[string]bool{
	"aes-gcm-128":       true,
	"aes-gcm-192":       true,
	"aes-gcm-256":       true,
	"aes-ccm-128":       true,
	"aes-ccm-192":       true,
	"aes-ccm-256":       true,
	"chacha20-poly1305": true,
}

// classicAlgs are encryption-only ciphers that require an auth algorithm.
var classicAlgs = map[string]bool{
	"aes-cbc-128": true,
	"aes-cbc-192": true,
	"aes-cbc-256": true,
}

var authAlgs = map[string]bool{
	"hmac-sha1":   true,
	"hmac-sha256": true,
	"hmac-sha384": true,
	"hmac-sha512": true,
}

// pfsGroups lists acceptable PFS groups; weak groups such as modp1024 are
// deliberately absent.
var pfsGroups = map[string]bool{
	"":           true,
	"ecp256":     true,
	"ecp384":     true,
	"ecp521":     true,
	"curve25519": true,
	"modp2048":   true,
	"modp3072":   true,
	"modp4096":   true,
	"modp6144":   true,
	"modp8192":   true,
}

// reconcileLease is a process-level global lease: only one replica applies a
// reconcile at a time, others return success without touching state.
var reconcileLease sync.Mutex

type policyEntry struct {
	policy Policy
	src    netip.Prefix
	dst    netip.Prefix
}

// XfrmReconciler owns XFRM policies and SAs.
type XfrmReconciler struct {
	mu       sync.RWMutex
	now      time.Time
	policies map[string]*policyEntry
	sas      map[uint32]SA
	nextSPI  uint32
}

// NewXfrmReconciler returns an empty reconciler with a fixed clock origin.
func NewXfrmReconciler(now time.Time) *XfrmReconciler {
	return &XfrmReconciler{
		now:      now,
		policies: make(map[string]*policyEntry),
		sas:      make(map[uint32]SA),
		nextSPI:  firstSPI,
	}
}

// AddTunnel installs a policy and its SA atomically: if either side fails the
// other must not be left behind. Adding the same tunnel with identical
// parameters again is idempotent and returns the existing SPI; a different
// definition for an existing name is rejected.
func (x *XfrmReconciler) AddTunnel(p Policy) (uint32, error) {
	entry, err := x.validatePolicy(p)
	if err != nil {
		return 0, err
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if old, ok := x.policies[entry.policy.Name]; ok {
		if old.policy == entry.policy {
			for _, sa := range x.sas {
				if sa.PolicyName == entry.policy.Name {
					return sa.SPI, nil
				}
			}
		}
		return 0, fmt.Errorf("%w: %s", errTunnelExists, entry.policy.Name)
	}

	spi := x.allocSPILocked()
	x.policies[entry.policy.Name] = entry
	x.sas[spi] = SA{
		SPI:          spi,
		PolicyName:   entry.policy.Name,
		Src:          entry.policy.Src,
		Dst:          entry.policy.Dst,
		ReplayWindow: defaultReplayWindow,
		Algorithm:    entry.policy.Algorithm,
		ExpiresAt:    x.now.Add(defaultSALifetime),
		State:        SAStateActive,
	}
	return spi, nil
}

// AddSA imports one SA directly, rejecting SPI conflicts.
func (x *XfrmReconciler) AddSA(sa SA) error {
	if sa.SPI == 0 {
		return errors.New("spi must be non-zero")
	}
	if sa.Src != "" {
		if n, err := x.NormalizeCIDR(sa.Src); err == nil {
			sa.Src = n
		}
	}
	if sa.Dst != "" {
		if n, err := x.NormalizeCIDR(sa.Dst); err == nil {
			sa.Dst = n
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.sas[sa.SPI]; ok {
		return fmt.Errorf("%w: %d", errSPIConflict, sa.SPI)
	}
	x.sas[sa.SPI] = withSADefaults(sa, x.now)
	return nil
}

// DeleteTunnel removes the policy and every SA that references it.
func (x *XfrmReconciler) DeleteTunnel(name string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.policies[name]; !ok {
		return fmt.Errorf("%w: %s", errTunnelNotFound, name)
	}
	delete(x.policies, name)
	for spi, sa := range x.sas {
		if sa.PolicyName == name {
			delete(x.sas, spi)
		}
	}
	return nil
}

// Policies returns the installed policies ordered by priority.
func (x *XfrmReconciler) Policies() []Policy {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.sortedPoliciesLocked()
}

// SAs returns the installed SAs.
func (x *XfrmReconciler) SAs() []SA {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.sortedSAsLocked()
}

// NormalizeCIDR canonicalises a CIDR (host bits zeroed, IPv6 compressed).
func (x *XfrmReconciler) NormalizeCIDR(c string) (string, error) {
	p, err := netip.ParsePrefix(c)
	if err != nil {
		return "", fmt.Errorf("invalid cidr %q: %w", c, err)
	}
	return p.Masked().String(), nil
}

// Match returns the most specific policy that covers the src/dst pair.
func (x *XfrmReconciler) Match(src, dst string) (Policy, bool) {
	srcIP, err := netip.ParseAddr(src)
	if err != nil {
		return Policy{}, false
	}
	dstIP, err := netip.ParseAddr(dst)
	if err != nil {
		return Policy{}, false
	}
	x.mu.RLock()
	defer x.mu.RUnlock()
	var best *policyEntry
	bestBits := -1
	for _, e := range x.policies {
		if !e.src.Contains(srcIP) || !e.dst.Contains(dstIP) {
			continue
		}
		bits := e.src.Bits() + e.dst.Bits()
		if best == nil || bits > bestBits ||
			(bits == bestBits && lessPolicy(e.policy, best.policy)) {
			best = e
			bestBits = bits
		}
	}
	if best == nil {
		return Policy{}, false
	}
	return best.policy, true
}

// SetReplayWindow updates the replay window of an existing SA, whatever its
// age.
func (x *XfrmReconciler) SetReplayWindow(spi, n uint32) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	sa, ok := x.sas[spi]
	if !ok {
		return fmt.Errorf("%w: spi %d", errSANotFound, spi)
	}
	sa.ReplayWindow = n
	x.sas[spi] = sa
	return nil
}

// ValidateAlgorithm rejects AEAD combinations that are not compliant.
func (x *XfrmReconciler) ValidateAlgorithm(alg, auth string) error {
	if alg == "" {
		return errors.New("encryption algorithm is required")
	}
	if aeadAlgs[alg] {
		if auth != "" {
			return fmt.Errorf("aead cipher %s must not carry auth algorithm %q", alg, auth)
		}
		return nil
	}
	if classicAlgs[alg] {
		if auth == "" {
			return fmt.Errorf("cipher %s requires an authentication algorithm", alg)
		}
		if !authAlgs[auth] {
			return fmt.Errorf("unsupported auth algorithm %q", auth)
		}
		return nil
	}
	return fmt.Errorf("unsupported encryption algorithm %q", alg)
}

// Rekey replaces an expired SA with a fresh one.
func (x *XfrmReconciler) Rekey(spi uint32, now time.Time) (uint32, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	sa, ok := x.sas[spi]
	if !ok {
		return 0, fmt.Errorf("%w: spi %d", errSANotFound, spi)
	}
	newSPI := x.allocSPILocked()
	delete(x.sas, spi)
	sa.SPI = newSPI
	sa.State = SAStateActive
	sa.ExpiresAt = now.Add(defaultSALifetime)
	x.sas[newSPI] = sa
	return newSPI, nil
}

// Expired lists the SAs whose lifetime ended before now.
func (x *XfrmReconciler) Expired(now time.Time) []SA {
	x.mu.RLock()
	defer x.mu.RUnlock()
	var out []SA
	for _, sa := range x.sas {
		if sa.ExpiresAt.Before(now) {
			out = append(out, sa)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SPI < out[j].SPI })
	return out
}

// Reconcile aligns the local state with the desired state after a restart.
// A process-level global lease ensures that of two replicas reconciling at
// the same time only one applies; the other returns success unchanged.
func (x *XfrmReconciler) Reconcile(policies []Policy, sas []SA) error {
	if !reconcileLease.TryLock() {
		return nil
	}
	defer reconcileLease.Unlock()

	newPolicies := make(map[string]*policyEntry, len(policies))
	for _, p := range policies {
		entry, err := x.validatePolicy(p)
		if err != nil {
			return err
		}
		if _, dup := newPolicies[entry.policy.Name]; dup {
			return fmt.Errorf("duplicate policy in desired state: %s", entry.policy.Name)
		}
		newPolicies[entry.policy.Name] = entry
	}
	newSAs := make(map[uint32]SA, len(sas))
	for _, sa := range sas {
		if sa.SPI == 0 {
			return errors.New("desired sa has zero spi")
		}
		if _, dup := newSAs[sa.SPI]; dup {
			return fmt.Errorf("%w: %d", errSPIConflict, sa.SPI)
		}
		newSAs[sa.SPI] = withSADefaults(sa, x.now)
	}

	x.mu.Lock()
	defer x.mu.Unlock()
	x.policies = newPolicies
	x.sas = newSAs
	return nil
}

// Snapshot returns a consistent copy of policies and SAs.
func (x *XfrmReconciler) Snapshot() ([]Policy, []SA) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.sortedPoliciesLocked(), x.sortedSAsLocked()
}

// validatePolicy normalises and validates a policy without mutating state.
func (x *XfrmReconciler) validatePolicy(p Policy) (*policyEntry, error) {
	if p.Name == "" {
		return nil, errors.New("policy name is required")
	}
	switch p.Mode {
	case ModeTunnel:
		if _, err := netip.ParseAddr(p.TunnelSrc); err != nil {
			return nil, fmt.Errorf("tunnel mode requires a valid outer source: %q", p.TunnelSrc)
		}
		if _, err := netip.ParseAddr(p.TunnelDst); err != nil {
			return nil, fmt.Errorf("tunnel mode requires a valid outer destination: %q", p.TunnelDst)
		}
	case ModeTransport:
	default:
		return nil, fmt.Errorf("unsupported mode %q", p.Mode)
	}
	src, err := x.NormalizeCIDR(p.Src)
	if err != nil {
		return nil, err
	}
	dst, err := x.NormalizeCIDR(p.Dst)
	if err != nil {
		return nil, err
	}
	if err := x.ValidateAlgorithm(p.Algorithm, p.AuthAlg); err != nil {
		return nil, err
	}
	if !pfsGroups[p.PFSGroup] {
		return nil, fmt.Errorf("unsupported pfs group %q", p.PFSGroup)
	}
	p.Src, p.Dst = src, dst
	srcPrefix, _ := netip.ParsePrefix(src)
	dstPrefix, _ := netip.ParsePrefix(dst)
	return &policyEntry{policy: p, src: srcPrefix, dst: dstPrefix}, nil
}

// allocSPILocked returns the next free SPI. Caller must hold x.mu.
func (x *XfrmReconciler) allocSPILocked() uint32 {
	for {
		spi := x.nextSPI
		x.nextSPI++
		if spi == 0 {
			continue
		}
		if _, used := x.sas[spi]; !used {
			return spi
		}
	}
}

func withSADefaults(sa SA, now time.Time) SA {
	if sa.State == "" {
		sa.State = SAStateActive
	}
	if sa.ExpiresAt.IsZero() {
		sa.ExpiresAt = now.Add(defaultSALifetime)
	}
	if sa.ReplayWindow == 0 {
		sa.ReplayWindow = defaultReplayWindow
	}
	return sa
}

func lessPolicy(a, b Policy) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return a.Name < b.Name
}

func (x *XfrmReconciler) sortedPoliciesLocked() []Policy {
	out := make([]Policy, 0, len(x.policies))
	for _, e := range x.policies {
		out = append(out, e.policy)
	}
	sort.Slice(out, func(i, j int) bool { return lessPolicy(out[i], out[j]) })
	return out
}

func (x *XfrmReconciler) sortedSAsLocked() []SA {
	out := make([]SA, 0, len(x.sas))
	for _, sa := range x.sas {
		out = append(out, sa)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SPI < out[j].SPI })
	return out
}
