package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Controller names a cgroup v2 controller.
type Controller string

const (
	CtrlCPU    Controller = "cpu"
	CtrlMemory Controller = "memory"
	CtrlIO     Controller = "io"
	CtrlCPUSet Controller = "cpuset"
	CtrlPids   Controller = "pids"
)

// Limits is the set of limits written for a delegated cgroup.
type Limits struct {
	CPUMax    string
	CPUSet    string
	IOMax     string
	MemoryMax int64
}

// Delegation describes a cgroup delegated to a tenant.
type Delegation struct {
	Path                  string
	TenantID              string
	Controllers           []Controller
	Depth                 int
	SubtreeControlCleared bool
	Removed               bool
	Limits                Limits
}

// Stats is a consistent snapshot of a delegation's accounting.
type Stats struct {
	CPUUsec     uint64
	MemoryBytes int64
	Pids        int
	Version     uint64
}

// ProcUsage is one process considered for OOM victim selection.
type ProcUsage struct {
	PID         int
	TenantID    string
	MemoryBytes int64
}

// WardenOptions configures the delegation warden.
type WardenOptions struct {
	MaxDepth     int
	MaxChildren  int
	RootTenant   string
	DefaultQuota int
	RootQuota    int
}

// DelegationWarden owns delegated cgroup trees.
type DelegationWarden struct{}

// NewDelegationWarden returns a warden configured by opts.
func NewDelegationWarden(opts WardenOptions) *DelegationWarden {
	return &DelegationWarden{}
}

// Delegate hands path to tenantID with the requested controllers.
func (w *DelegationWarden) Delegate(tenantID, path string, ctrls []Controller) (*Delegation, error) {
	return nil, errNotImplemented
}

// Files lists the cgroup files the tenant may touch.
func (w *DelegationWarden) Files(d *Delegation) []string {
	return nil
}

// SetLimits writes limits for the delegation.
func (w *DelegationWarden) SetLimits(d *Delegation, l Limits) error {
	return errNotImplemented
}

// LimitsFor returns the effective limits of the delegation.
func (w *DelegationWarden) LimitsFor(d *Delegation) (Limits, error) {
	return Limits{}, errNotImplemented
}

// LimitFiles renders the controller files for the given limits.
func (w *DelegationWarden) LimitFiles(l Limits) map[string]string {
	return nil
}

// MoveProcess moves pid into the delegation on behalf of owner.
func (w *DelegationWarden) MoveProcess(d *Delegation, pid int, owner string) error {
	return errNotImplemented
}

// FreezeAndKill freezes then kills every process of the delegation.
func (w *DelegationWarden) FreezeAndKill(d *Delegation) ([]string, error) {
	return nil, errNotImplemented
}

// FreezeAndKillWith freezes with the supplied freezer before killing.
func (w *DelegationWarden) FreezeAndKillWith(d *Delegation, freezer func() error) ([]string, error) {
	return nil, errNotImplemented
}

// Stats returns a consistent accounting snapshot.
func (w *DelegationWarden) Stats(d *Delegation) (Stats, error) {
	return Stats{}, errNotImplemented
}

// CreateChild creates a nested delegation below parent.
func (w *DelegationWarden) CreateChild(parent *Delegation, name string) (*Delegation, error) {
	return nil, errNotImplemented
}

// Remove deletes the delegation and releases its accounting.
func (w *DelegationWarden) Remove(d *Delegation) error {
	return errNotImplemented
}

// PendingTimers reports how many timers still reference delegations.
func (w *DelegationWarden) PendingTimers() int {
	return 0
}

// Snapshot serialises the delegation state.
func (w *DelegationWarden) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds delegation state from a snapshot.
func (w *DelegationWarden) Restore(data []byte) error {
	return errNotImplemented
}

// Lookup returns the delegation registered for path.
func (w *DelegationWarden) Lookup(path string) (*Delegation, bool) {
	return nil, false
}

// QuotaFor returns the key quota of a tenant.
func (w *DelegationWarden) QuotaFor(tenantID string) int {
	return 0
}

// OOMVictim picks the process to kill when a delegation exceeds limit.
func (w *DelegationWarden) OOMVictim(d *Delegation, procs []ProcUsage, limit int64) (int, error) {
	return 0, errNotImplemented
}
