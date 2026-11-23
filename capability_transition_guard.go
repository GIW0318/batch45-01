package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Cap is a Linux capability bit.
type Cap uint64

const (
	CapChown Cap = 1 << iota
	CapDACOverride
	CapKill
	CapSetUID
	CapSetGID
	CapNetBindService
	CapSysAdmin
	CapNetAdmin
)

// CapSet is the five capability sets of a process.
type CapSet struct {
	Bounding    Cap
	Permitted   Cap
	Effective   Cap
	Inheritable Cap
	Ambient     Cap
}

// FileCap is a file capability entry.
type FileCap struct {
	Path string
	Caps Cap
}

// ExecPlan describes an exec transition.
type ExecPlan struct {
	Binary        string
	SetUID        bool
	SetGID        bool
	NoNewPrivs    bool
	AmbientClear  bool
	ZeroEffective bool
}

// GuardOptions configures the capability guard.
type GuardOptions struct {
	AllowedFileCapPaths []string
	KernelCapVersion    int
	SupportedCapVersion int
}

// CapabilityGuard tracks and narrows capability state.
type CapabilityGuard struct{}

// NewCapabilityGuard returns a guard configured by opts.
func NewCapabilityGuard(opts GuardOptions) *CapabilityGuard {
	return &CapabilityGuard{}
}

// Drop removes a capability from every set.
func (g *CapabilityGuard) Drop(set CapSet, drop Cap) (CapSet, error) {
	return CapSet{}, errNotImplemented
}

// DropBounding removes a capability from the bounding set and recomputes the rest.
func (g *CapabilityGuard) DropBounding(set CapSet, drop Cap) (CapSet, error) {
	return CapSet{}, errNotImplemented
}

// DropAllExcept keeps only the listed capability.
func (g *CapabilityGuard) DropAllExcept(keep Cap) (CapSet, error) {
	return CapSet{}, errNotImplemented
}

// PlanExec computes the capability state after an exec.
func (g *CapabilityGuard) PlanExec(set CapSet, plan ExecPlan) (CapSet, ExecPlan, error) {
	return CapSet{}, ExecPlan{}, errNotImplemented
}

// ApplyFileCap validates and applies a file capability entry.
func (g *CapabilityGuard) ApplyFileCap(fc FileCap) error {
	return errNotImplemented
}

// EnableNoNewPrivs turns on no_new_privs for the process.
func (g *CapabilityGuard) EnableNoNewPrivs() error {
	return errNotImplemented
}

// CheckVersion validates the running kernel capability version.
func (g *CapabilityGuard) CheckVersion(kernel int) error {
	return errNotImplemented
}

// SetCurrent records the current capability state.
func (g *CapabilityGuard) SetCurrent(set CapSet) error {
	return errNotImplemented
}

// Current returns the recorded capability state.
func (g *CapabilityGuard) Current() CapSet {
	return CapSet{}
}

// SetUIDTo changes the uid and recomputes permitted capabilities.
func (g *CapabilityGuard) SetUIDTo(uid int) (CapSet, error) {
	return CapSet{}, errNotImplemented
}

// ApplyUpstreamCaps intersects upstream capability grants with the local baseline.
func (g *CapabilityGuard) ApplyUpstreamCaps(upstream CapSet) (CapSet, error) {
	return CapSet{}, errNotImplemented
}

// Snapshot serialises the capability state.
func (g *CapabilityGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds capability state from a snapshot.
func (g *CapabilityGuard) Restore(data []byte) error {
	return errNotImplemented
}
