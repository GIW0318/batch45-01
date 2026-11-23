package task

import (
	"context"
	"errors"
)

// Sentinel errors of the fd guard.
var (
	ErrSoftLimitReached = errors.New("fd soft limit reached")
	ErrHardLimitReached = errors.New("fd hard limit reached")
	ErrUnknownTenant    = errors.New("unknown tenant")
)

// TenantID identifies the tenant that owns file handles.
type TenantID string

// Budget is the soft and hard fd budget of one tenant.
type Budget struct {
	Soft int
	Hard int
}

// Ticket is a logical handle granted by the guard.
type Ticket struct {
	ID     uint64
	Tenant TenantID
	Kind   string
	Path   string
}

// PoolConfig bounds the per tenant connection pool.
type PoolConfig struct {
	MaxIdle int
	MaxOpen int
}

// PoolSnapshot reports pool occupancy.
type PoolSnapshot struct {
	Idle int
	Open int
}

// ProcFdInfo is one process observation used by leak detection.
type ProcFdInfo struct {
	Tenant TenantID
	PID    int
	Open   int
}

// Leak describes fds that a tenant leaked.
type Leak struct {
	Tenant TenantID
	PID    int
	Count  int
	Reason string
}

// SpawnPlan describes which fds a child process may inherit.
type SpawnPlan struct {
	Argv []string
	FDs  []string
}

// FdBudgetGuard keeps fd usage inside per tenant budgets.
type FdBudgetGuard struct {
}

// NewFdBudgetGuard creates an empty guard.
func NewFdBudgetGuard() *FdBudgetGuard {
	return &FdBudgetGuard{}
}

// SetBudget registers the fd budget of a tenant.
func (g *FdBudgetGuard) SetBudget(t TenantID, b Budget) error {
	return errors.New("not implemented")
}

// Acquire takes one fd from the tenant budget.
func (g *FdBudgetGuard) Acquire(t TenantID, kind, path string) (Ticket, error) {
	return Ticket{}, errors.New("not implemented")
}

// Release returns one fd to the tenant budget.
func (g *FdBudgetGuard) Release(tk Ticket) error {
	return errors.New("not implemented")
}

// Usage reports the number of fds currently held by a tenant.
func (g *FdBudgetGuard) Usage(t TenantID) (int, error) {
	return 0, errors.New("not implemented")
}

// OpenShared opens one path once and shares it between callers.
func (g *FdBudgetGuard) OpenShared(t TenantID, path string) (Ticket, error) {
	return Ticket{}, errors.New("not implemented")
}

// OpenLog returns the cached log handle of a path.
func (g *FdBudgetGuard) OpenLog(t TenantID, path string) (Ticket, error) {
	return Ticket{}, errors.New("not implemented")
}

// OpenCount reports how many physical handles exist for a path.
func (g *FdBudgetGuard) OpenCount(t TenantID, path string) (int, error) {
	return 0, errors.New("not implemented")
}

// OpenTemp opens a temporary file that can be unlinked while open.
func (g *FdBudgetGuard) OpenTemp(t TenantID, path string) (Ticket, error) {
	return Ticket{}, errors.New("not implemented")
}

// UnlinkTemp unlinks a temporary file and closes its handle.
func (g *FdBudgetGuard) UnlinkTemp(tk Ticket) error {
	return errors.New("not implemented")
}

// RunTask runs work and releases every fd on all exit paths.
func (g *FdBudgetGuard) RunTask(ctx context.Context, t TenantID, work func(context.Context) error) error {
	return errors.New("not implemented")
}

// ConfigurePool bounds the idle connection pool.
func (g *FdBudgetGuard) ConfigurePool(c PoolConfig) error {
	return errors.New("not implemented")
}

// OpenConn takes a pooled connection.
func (g *FdBudgetGuard) OpenConn(t TenantID) (Ticket, error) {
	return Ticket{}, errors.New("not implemented")
}

// CloseConn closes or recycles a pooled connection.
func (g *FdBudgetGuard) CloseConn(tk Ticket, reuse bool) error {
	return errors.New("not implemented")
}

// PoolStats reports pool occupancy for one tenant.
func (g *FdBudgetGuard) PoolStats(t TenantID) (PoolSnapshot, error) {
	return PoolSnapshot{}, errors.New("not implemented")
}

// LeakReport finds per tenant fd leaks among process observations.
func (g *FdBudgetGuard) LeakReport(procs []ProcFdInfo) ([]Leak, error) {
	return nil, errors.New("not implemented")
}

// Spawn builds the fd inheritance plan for a child process.
func (g *FdBudgetGuard) Spawn(t TenantID, argv []string, inherit []string) (SpawnPlan, error) {
	return SpawnPlan{}, errors.New("not implemented")
}

// Walk lists files under root, closing every directory handle.
func (g *FdBudgetGuard) Walk(t TenantID, root string, tree map[string][]string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// RecoverAfterRestart closes handles leaked by a previous process.
func (g *FdBudgetGuard) RecoverAfterRestart(state []Ticket) (int, error) {
	return 0, errors.New("not implemented")
}
