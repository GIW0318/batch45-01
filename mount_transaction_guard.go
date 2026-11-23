package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// MountSpec is one requested mount of a container.
type MountSpec struct {
	Source      string
	Target      string
	FSType      string
	ReadOnly    bool
	Propagation string
	UIDMap      int
	HostUID     int
	Options     []string
}

// Mount is an applied mount.
type Mount struct {
	Spec        MountSpec
	ContainerID string
}

// Op is one runtime mount operation.
type Op struct {
	Kind   string
	Source string
	Target string
}

// Transaction groups the mounts of one container.
type Transaction struct {
	ContainerID string
	Specs       []MountSpec
}

// GuardOptions configures the mount transaction guard.
type GuardOptions struct {
	MaxMounts          int
	AllowedPropagation []string
	Root               string
}

// MountTransactionGuard applies and rolls back mount transactions.
type MountTransactionGuard struct{}

// NewMountTransactionGuard returns a guard configured by opts.
func NewMountTransactionGuard(opts GuardOptions) *MountTransactionGuard {
	return &MountTransactionGuard{}
}

// CreateContainer validates specs and returns the pending transaction.
func (g *MountTransactionGuard) CreateContainer(id string, specs []MountSpec) (*Transaction, error) {
	return nil, errNotImplemented
}

// Apply mounts every spec, rolling back on the first failure.
func (g *MountTransactionGuard) Apply(tx *Transaction, exec func(Op) error) ([]Op, error) {
	return nil, errNotImplemented
}

// Cleanup unmounts everything the container still holds.
func (g *MountTransactionGuard) Cleanup(id string, exec func(Op) error) ([]Op, error) {
	return nil, errNotImplemented
}

// Mounts lists all applied mounts.
func (g *MountTransactionGuard) Mounts() []Mount {
	return nil
}

// MountsFor lists the mounts of one container.
func (g *MountTransactionGuard) MountsFor(id string) []Mount {
	return nil
}

// ValidateSpec validates one mount specification.
func (g *MountTransactionGuard) ValidateSpec(s MountSpec) error {
	return errNotImplemented
}

// PropagationFor reports the mount propagation mode of a container.
func (g *MountTransactionGuard) PropagationFor(id string) string {
	return ""
}

// Snapshot serialises the mount table state.
func (g *MountTransactionGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds mount state from a snapshot.
func (g *MountTransactionGuard) Restore(data []byte) error {
	return errNotImplemented
}
