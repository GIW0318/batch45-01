package task

import (
	"errors"
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

// XfrmReconciler owns XFRM policies and SAs.
type XfrmReconciler struct{}

// NewXfrmReconciler returns an empty reconciler with a fixed clock origin.
func NewXfrmReconciler(now time.Time) *XfrmReconciler {
	return &XfrmReconciler{}
}

// AddTunnel installs a policy and its SA atomically: if either side fails the
// other must not be left behind. Adding the same tunnel with identical
// parameters again is idempotent and returns the existing SPI; a different
// definition for an existing name is rejected.
func (x *XfrmReconciler) AddTunnel(p Policy) (uint32, error) {
	return 0, errors.New("not implemented")
}

// AddSA imports one SA directly, rejecting SPI conflicts.
func (x *XfrmReconciler) AddSA(sa SA) error {
	return errors.New("not implemented")
}

// DeleteTunnel removes the policy and every SA that references it.
func (x *XfrmReconciler) DeleteTunnel(name string) error {
	return errors.New("not implemented")
}

// Policies returns the installed policies ordered by priority.
func (x *XfrmReconciler) Policies() []Policy {
	return nil
}

// SAs returns the installed SAs.
func (x *XfrmReconciler) SAs() []SA {
	return nil
}

// NormalizeCIDR canonicalises a CIDR (host bits zeroed, IPv6 compressed).
func (x *XfrmReconciler) NormalizeCIDR(c string) (string, error) {
	return "", errors.New("not implemented")
}

// Match returns the most specific policy that covers the src/dst pair.
func (x *XfrmReconciler) Match(src, dst string) (Policy, bool) {
	return Policy{}, false
}

// SetReplayWindow updates the replay window of an existing SA, whatever its
// age.
func (x *XfrmReconciler) SetReplayWindow(spi, n uint32) error {
	return errors.New("not implemented")
}

// ValidateAlgorithm rejects AEAD combinations that are not compliant.
func (x *XfrmReconciler) ValidateAlgorithm(alg, auth string) error {
	return errors.New("not implemented")
}

// Rekey replaces an expired SA with a fresh one.
func (x *XfrmReconciler) Rekey(spi uint32, now time.Time) (uint32, error) {
	return 0, errors.New("not implemented")
}

// Expired lists the SAs whose lifetime ended before now.
func (x *XfrmReconciler) Expired(now time.Time) []SA {
	return nil
}

// Reconcile aligns the local state with the desired state after a restart.
func (x *XfrmReconciler) Reconcile(policies []Policy, sas []SA) error {
	return errors.New("not implemented")
}

// Snapshot returns a consistent copy of policies and SAs.
func (x *XfrmReconciler) Snapshot() ([]Policy, []SA) {
	return nil, nil
}
