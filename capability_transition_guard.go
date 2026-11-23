package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Err* are the sentinel errors returned by the capability guard.
var (
	ErrUnknownCapability   = errors.New("unknown capability bit")
	errCapNotHeld          = errors.New("capability is not held by any set")
	errCapNotInBounding    = errors.New("capability is not present in the bounding set")
	errInvalidSet          = errors.New("capability set violates the bounding-set invariants")
	errFileCapDenied       = errors.New("file capability path is not in the allow list")
	errNoNewPrivs          = errors.New("file capabilities are disabled by no_new_privs")
	errVersionMismatch     = errors.New("capability version does not match the supported version")
	errNoCurrent           = errors.New("no capability state has been recorded")
	errSnapshotTooLarge    = errors.New("capability snapshot exceeds 64KB")
	errSnapshotCorrupt     = errors.New("capability snapshot is corrupt")
	errSnapshotVersion     = errors.New("capability snapshot version mismatch")
	errEmptyCapabilityMask = errors.New("capability mask must not be empty")
)

const (
	// knownCapMask covers every capability bit modelled by this guard.
	knownCapMask Cap = CapChown | CapDACOverride | CapKill | CapSetUID |
		CapSetGID | CapNetBindService | CapSysAdmin | CapNetAdmin

	// maxSnapshotBytes bounds the on-disk size of a capability snapshot.
	maxSnapshotBytes = 64 * 1024

	// snapshotMagic/Version tag guard-produced snapshot envelopes.
	snapshotMagic   = "capguard-snap"
	snapshotVersion = 1
)

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

// snapshotEnvelope is the versioned serialisation format used by Snapshot.
type snapshotEnvelope struct {
	Magic      string `json:"magic"`
	Version    int    `json:"version"`
	NoNewPrivs bool   `json:"no_new_privs"`
	Current    CapSet `json:"current"`
}

// CapabilityGuard tracks and narrows capability state.
type CapabilityGuard struct {
	mu           sync.RWMutex
	current      CapSet
	hasCurrent   bool
	noNewPrivs   bool
	allowed      map[string]struct{}
	kernelVer    int
	supportedVer int
}

// NewCapabilityGuard returns a guard configured by opts.
func NewCapabilityGuard(opts GuardOptions) *CapabilityGuard {
	allowed := make(map[string]struct{}, len(opts.AllowedFileCapPaths))
	for _, path := range opts.AllowedFileCapPaths {
		allowed[path] = struct{}{}
	}
	return &CapabilityGuard{
		allowed:      allowed,
		kernelVer:    opts.KernelCapVersion,
		supportedVer: opts.SupportedCapVersion,
	}
}

// validateMask rejects empty masks and capability bits the guard does not model.
func validateMask(mask Cap) error {
	if mask == 0 {
		return errEmptyCapabilityMask
	}
	if mask&^knownCapMask != 0 {
		return fmt.Errorf("%w: %#x", ErrUnknownCapability, mask&^knownCapMask)
	}
	return nil
}

// validateRecordedSet enforces the invariants every recorded CapSet must keep.
// The permitted set may temporarily carry capabilities outside the bounding set
// (for example before a uid transition recomputes it), but it must never be
// disjoint from the bounding set. Ambient/effective transitions may briefly
// outlive the working sets, so only structural impossibilities are rejected.
func validateRecordedSet(set CapSet) error {
	if set.Bounding&^knownCapMask != 0 ||
		set.Permitted&^knownCapMask != 0 ||
		set.Effective&^knownCapMask != 0 ||
		set.Inheritable&^knownCapMask != 0 ||
		set.Ambient&^knownCapMask != 0 {
		return fmt.Errorf("%w: unknown capability bit", ErrUnknownCapability)
	}
	if set.Permitted != 0 && set.Bounding != 0 && set.Permitted&set.Bounding == 0 {
		return fmt.Errorf("%w: permitted %#x is disjoint from bounding %#x",
			errInvalidSet, set.Permitted, set.Bounding)
	}
	return nil
}

// Drop removes a capability from every set.
func (g *CapabilityGuard) Drop(set CapSet, drop Cap) (CapSet, error) {
	if err := validateMask(drop); err != nil {
		return CapSet{}, err
	}
	held := set.Bounding | set.Permitted | set.Effective | set.Inheritable | set.Ambient
	if held&drop == 0 {
		return CapSet{}, fmt.Errorf("%w: %#x", errCapNotHeld, drop)
	}
	clear := ^drop
	out := CapSet{
		Bounding:    set.Bounding & clear,
		Permitted:   set.Permitted & clear,
		Effective:   set.Effective & clear,
		Inheritable: set.Inheritable & clear,
		Ambient:     set.Ambient & clear,
	}
	return out, nil
}

// DropBounding removes a capability from the bounding set and recomputes the rest.
func (g *CapabilityGuard) DropBounding(set CapSet, drop Cap) (CapSet, error) {
	if err := validateMask(drop); err != nil {
		return CapSet{}, err
	}
	if set.Bounding&drop == 0 {
		return CapSet{}, fmt.Errorf("%w: %#x", errCapNotInBounding, drop)
	}
	out, err := g.Drop(set, drop)
	if err != nil {
		return CapSet{}, err
	}
	return out, nil
}

// DropAllExcept keeps only the listed capability in the recorded state.
func (g *CapabilityGuard) DropAllExcept(keep Cap) (CapSet, error) {
	if err := validateMask(keep); err != nil {
		return CapSet{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.hasCurrent {
		return CapSet{}, errNoCurrent
	}
	out := CapSet{
		Bounding:    keep,
		Permitted:   keep,
		Effective:   keep,
		Inheritable: 0,
		Ambient:     0,
	}
	if err := validateRecordedSet(out); err != nil {
		return CapSet{}, err
	}
	g.current = out
	return out, nil
}

// PlanExec computes the capability state after an exec. It is a pure
// computation: it never reads or writes the guard's recorded state, so the
// same (set, plan) pair always yields the same result, even concurrently.
func (g *CapabilityGuard) PlanExec(set CapSet, plan ExecPlan) (CapSet, ExecPlan, error) {
	if err := validateRecordedSet(set); err != nil {
		return CapSet{}, ExecPlan{}, err
	}

	out := plan // copy: the caller's plan is never mutated
	out.ZeroEffective = true

	outSet := CapSet{
		Bounding:    set.Bounding,
		Permitted:   set.Permitted & set.Bounding,
		Effective:   0, // effective is always re-evaluated to empty on execve
		Inheritable: 0, // inheritable caps never leak into the child image
		Ambient:     set.Ambient & set.Bounding,
	}
	if plan.SetUID || plan.SetGID {
		// A privilege-changing exec drops ambient capabilities outright.
		out.AmbientClear = true
		outSet.Ambient = 0
	}
	if plan.AmbientClear {
		out.AmbientClear = true
		outSet.Ambient = 0
	}
	return outSet, out, nil
}

// ApplyFileCap validates and applies a file capability entry.
func (g *CapabilityGuard) ApplyFileCap(fc FileCap) error {
	if err := validateMask(fc.Caps); err != nil {
		return err
	}
	g.mu.RLock()
	deniedByNNP := g.noNewPrivs
	_, allowed := g.allowed[fc.Path]
	g.mu.RUnlock()

	if deniedByNNP {
		return fmt.Errorf("%w: %s", errNoNewPrivs, fc.Path)
	}
	if !allowed {
		return fmt.Errorf("%w: %s", errFileCapDenied, fc.Path)
	}
	return nil
}

// EnableNoNewPrivs turns on no_new_privs for the process.
func (g *CapabilityGuard) EnableNoNewPrivs() error {
	g.mu.Lock()
	g.noNewPrivs = true
	g.mu.Unlock()
	return nil
}

// CheckVersion validates the running kernel capability version.
func (g *CapabilityGuard) CheckVersion(kernel int) error {
	if kernel != g.supportedVer || kernel != g.kernelVer {
		return fmt.Errorf("%w: kernel=%d configured=%d supported=%d",
			errVersionMismatch, kernel, g.kernelVer, g.supportedVer)
	}
	return nil
}

// SetCurrent records the current capability state.
func (g *CapabilityGuard) SetCurrent(set CapSet) error {
	if err := validateRecordedSet(set); err != nil {
		return err
	}
	g.mu.Lock()
	g.current = set
	g.hasCurrent = true
	g.mu.Unlock()
	return nil
}

// Current returns the recorded capability state.
func (g *CapabilityGuard) Current() CapSet {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.current
}

// SetUIDTo changes the uid and recomputes every working set against the
// bounding set so privileges dropped from the boundary cannot reappear.
func (g *CapabilityGuard) SetUIDTo(uid int) (CapSet, error) {
	_ = uid
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.hasCurrent {
		return CapSet{}, errNoCurrent
	}
	bounding := g.current.Bounding
	out := CapSet{
		Bounding:    bounding,
		Permitted:   g.current.Permitted & bounding,
		Effective:   g.current.Effective & bounding,
		Inheritable: g.current.Inheritable & bounding,
		Ambient:     g.current.Ambient & bounding,
	}
	if err := validateRecordedSet(out); err != nil {
		return CapSet{}, err
	}
	g.current = out
	return out, nil
}

// ApplyUpstreamCaps intersects upstream capability grants with the local
// baseline, so an upstream payload can never add a missing capability.
func (g *CapabilityGuard) ApplyUpstreamCaps(upstream CapSet) (CapSet, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.hasCurrent {
		return CapSet{}, errNoCurrent
	}
	base := g.current
	out := CapSet{
		Bounding:    base.Bounding & upstream.Bounding,
		Permitted:   base.Permitted & upstream.Permitted,
		Effective:   base.Effective & upstream.Effective,
		Inheritable: base.Inheritable & upstream.Inheritable,
		Ambient:     base.Ambient & upstream.Ambient,
	}
	if err := validateRecordedSet(out); err != nil {
		return CapSet{}, err
	}
	g.current = out
	return out, nil
}

// Snapshot serialises the capability state.
func (g *CapabilityGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	if !g.hasCurrent {
		g.mu.RUnlock()
		return nil, errNoCurrent
	}
	envelope := snapshotEnvelope{
		Magic:      snapshotMagic,
		Version:    snapshotVersion,
		NoNewPrivs: g.noNewPrivs,
		Current:    g.current,
	}
	g.mu.RUnlock()

	data, err := json.Marshal(&envelope)
	if err != nil {
		return nil, fmt.Errorf("encode capability snapshot: %w", err)
	}
	if len(data) > maxSnapshotBytes {
		return nil, fmt.Errorf("%w: %d bytes", errSnapshotTooLarge, len(data))
	}
	return data, nil
}

// Restore rebuilds capability state from a snapshot.
func (g *CapabilityGuard) Restore(data []byte) error {
	if len(data) == 0 {
		return errSnapshotCorrupt
	}
	if len(data) > maxSnapshotBytes {
		return fmt.Errorf("%w: %d bytes", errSnapshotTooLarge, len(data))
	}
	var envelope snapshotEnvelope
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&envelope); err != nil {
		return fmt.Errorf("%w: %v", errSnapshotCorrupt, err)
	}
	if envelope.Magic != snapshotMagic {
		return fmt.Errorf("%w: bad magic %q", errSnapshotCorrupt, envelope.Magic)
	}
	if envelope.Version != snapshotVersion {
		return fmt.Errorf("%w: got %d want %d",
			errSnapshotVersion, envelope.Version, snapshotVersion)
	}
	if err := validateRecordedSet(envelope.Current); err != nil {
		return fmt.Errorf("%w: %v", errSnapshotCorrupt, err)
	}

	g.mu.Lock()
	g.current = envelope.Current
	g.hasCurrent = true
	g.noNewPrivs = envelope.NoNewPrivs
	g.mu.Unlock()
	return nil
}
