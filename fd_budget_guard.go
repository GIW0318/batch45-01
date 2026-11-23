package task

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"sync"
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

// sharedHandle is one physical handle with logical users.
type sharedHandle struct {
	refs int
}

// taskScope tracks the fds acquired by one RunTask invocation.
type taskScope struct {
	tickets map[uint64]struct{}
}

// FdBudgetGuard keeps fd usage inside per tenant budgets.
type FdBudgetGuard struct {
	mu       sync.Mutex
	nextID   uint64
	budgets  map[TenantID]Budget
	used     map[TenantID]int
	active   map[uint64]Ticket
	shared   map[TenantID]map[string]*sharedHandle
	scopes   map[TenantID][]*taskScope
	poolCfg  PoolConfig
	poolSet  bool
	poolOpen map[TenantID]int
	poolIdle map[TenantID]int
}

// NewFdBudgetGuard creates an empty guard.
func NewFdBudgetGuard() *FdBudgetGuard {
	return &FdBudgetGuard{
		nextID:   1,
		budgets:  make(map[TenantID]Budget),
		used:     make(map[TenantID]int),
		active:   make(map[uint64]Ticket),
		shared:   make(map[TenantID]map[string]*sharedHandle),
		scopes:   make(map[TenantID][]*taskScope),
		poolOpen: make(map[TenantID]int),
		poolIdle: make(map[TenantID]int),
	}
}

// SetBudget registers the fd budget of a tenant.
func (g *FdBudgetGuard) SetBudget(t TenantID, b Budget) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.budgets[t] = b
	return nil
}

// acquireLocked takes one fd from the tenant budget. Callers hold g.mu.
func (g *FdBudgetGuard) acquireLocked(t TenantID, kind, p string) (Ticket, error) {
	b, ok := g.budgets[t]
	if !ok {
		return Ticket{}, ErrUnknownTenant
	}
	used := g.used[t]
	if b.Hard > 0 && used >= b.Hard {
		return Ticket{}, ErrHardLimitReached
	}
	if b.Soft > 0 && used >= b.Soft {
		return Ticket{}, ErrSoftLimitReached
	}
	tk := Ticket{ID: g.nextID, Tenant: t, Kind: kind, Path: p}
	g.nextID++
	g.used[t] = used + 1
	g.active[tk.ID] = tk
	if stack := g.scopes[t]; len(stack) > 0 {
		top := stack[len(stack)-1]
		top.tickets[tk.ID] = struct{}{}
	}
	return tk, nil
}

// Acquire takes one fd from the tenant budget.
func (g *FdBudgetGuard) Acquire(t TenantID, kind, path string) (Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.acquireLocked(t, kind, path)
}

// Release returns one fd to the tenant budget. Releasing the same
// ticket twice is a no-op, the budget is only credited once.
func (g *FdBudgetGuard) Release(tk Ticket) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseLocked(tk)
	return nil
}

// releaseLocked frees one ticket exactly once. Callers hold g.mu.
func (g *FdBudgetGuard) releaseLocked(tk Ticket) {
	if _, ok := g.active[tk.ID]; !ok {
		return
	}
	delete(g.active, tk.ID)
	if g.used[tk.Tenant] > 0 {
		g.used[tk.Tenant]--
	}
	if tk.Kind == "shared" || tk.Kind == "log" {
		if paths, ok := g.shared[tk.Tenant]; ok {
			if h, ok := paths[tk.Path]; ok {
				h.refs--
				if h.refs <= 0 {
					delete(paths, tk.Path)
				}
			}
		}
	}
}

// Usage reports the number of fds currently held by a tenant.
func (g *FdBudgetGuard) Usage(t TenantID) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.budgets[t]; !ok {
		return 0, ErrUnknownTenant
	}
	return g.used[t], nil
}

// OpenShared opens one path once and shares it between callers.
func (g *FdBudgetGuard) OpenShared(t TenantID, path string) (Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.openSharedLocked(t, "shared", path)
}

// OpenLog returns the cached log handle of a path.
func (g *FdBudgetGuard) OpenLog(t TenantID, path string) (Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.openSharedLocked(t, "log", path)
}

// openSharedLocked reuses the physical handle of a path. Callers hold g.mu.
func (g *FdBudgetGuard) openSharedLocked(t TenantID, kind, p string) (Ticket, error) {
	if _, ok := g.budgets[t]; !ok {
		return Ticket{}, ErrUnknownTenant
	}
	tk, err := g.acquireLocked(t, kind, p)
	if err != nil {
		return Ticket{}, err
	}
	paths, ok := g.shared[t]
	if !ok {
		paths = make(map[string]*sharedHandle)
		g.shared[t] = paths
	}
	h, ok := paths[p]
	if !ok {
		h = &sharedHandle{}
		paths[p] = h
	}
	h.refs++
	return tk, nil
}

// OpenCount reports how many physical handles exist for a path.
func (g *FdBudgetGuard) OpenCount(t TenantID, path string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.budgets[t]; !ok {
		return 0, ErrUnknownTenant
	}
	if paths, ok := g.shared[t]; ok {
		if h, ok := paths[path]; ok && h.refs > 0 {
			return 1, nil
		}
	}
	return 0, nil
}

// OpenTemp opens a temporary file that can be unlinked while open.
func (g *FdBudgetGuard) OpenTemp(t TenantID, path string) (Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.acquireLocked(t, "temp", path)
}

// UnlinkTemp unlinks a temporary file and closes its handle.
func (g *FdBudgetGuard) UnlinkTemp(tk Ticket) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseLocked(tk)
	return nil
}

// RunTask runs work and releases every fd on all exit paths.
func (g *FdBudgetGuard) RunTask(ctx context.Context, t TenantID, work func(context.Context) error) error {
	g.mu.Lock()
	b, ok := g.budgets[t]
	if !ok {
		g.mu.Unlock()
		return ErrUnknownTenant
	}
	if b.Soft > 0 && g.used[t] >= b.Soft {
		g.mu.Unlock()
		return ErrSoftLimitReached
	}
	scope := &taskScope{tickets: make(map[uint64]struct{})}
	g.scopes[t] = append(g.scopes[t], scope)
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		stack := g.scopes[t]
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i] == scope {
				g.scopes[t] = append(stack[:i], stack[i+1:]...)
				break
			}
		}
		ids := make([]uint64, 0, len(scope.tickets))
		for id := range scope.tickets {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			if tk, ok := g.active[id]; ok {
				g.releaseLocked(tk)
			}
		}
	}()

	return work(ctx)
}

// ConfigurePool bounds the idle connection pool.
func (g *FdBudgetGuard) ConfigurePool(c PoolConfig) error {
	if c.MaxIdle < 0 || c.MaxOpen <= 0 || c.MaxIdle > c.MaxOpen {
		return fmt.Errorf("invalid pool config: %+v", c)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.poolCfg = c
	g.poolSet = true
	return nil
}

// OpenConn takes a pooled connection.
func (g *FdBudgetGuard) OpenConn(t TenantID) (Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.budgets[t]; !ok {
		return Ticket{}, ErrUnknownTenant
	}
	if g.poolSet && g.poolOpen[t] >= g.poolCfg.MaxOpen {
		return Ticket{}, ErrHardLimitReached
	}
	tk := Ticket{ID: g.nextID, Tenant: t, Kind: "conn"}
	g.nextID++
	g.poolOpen[t]++
	return tk, nil
}

// CloseConn closes or recycles a pooled connection. Idle connections
// are capped at MaxIdle, anything beyond that is closed for real.
func (g *FdBudgetGuard) CloseConn(tk Ticket, reuse bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if reuse && g.poolSet && g.poolIdle[tk.Tenant] < g.poolCfg.MaxIdle {
		g.poolIdle[tk.Tenant]++
		return nil
	}
	if g.poolOpen[tk.Tenant] > 0 {
		g.poolOpen[tk.Tenant]--
	}
	return nil
}

// PoolStats reports pool occupancy for one tenant.
func (g *FdBudgetGuard) PoolStats(t TenantID) (PoolSnapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.budgets[t]; !ok {
		return PoolSnapshot{}, ErrUnknownTenant
	}
	return PoolSnapshot{Idle: g.poolIdle[t], Open: g.poolOpen[t]}, nil
}

// LeakReport finds per tenant fd leaks among process observations.
func (g *FdBudgetGuard) LeakReport(procs []ProcFdInfo) ([]Leak, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	totals := make(map[TenantID]int)
	for _, p := range procs {
		totals[p.Tenant] += p.Open
	}
	var leaks []Leak
	for _, p := range procs {
		b, ok := g.budgets[p.Tenant]
		if !ok {
			continue
		}
		if b.Soft > 0 && totals[p.Tenant] > b.Soft {
			leaks = append(leaks, Leak{
				Tenant: p.Tenant,
				PID:    p.PID,
				Count:  p.Open,
				Reason: fmt.Sprintf("tenant %s holds %d fds, soft limit is %d", p.Tenant, totals[p.Tenant], b.Soft),
			})
		}
	}
	return leaks, nil
}

// Spawn builds the fd inheritance plan for a child process. The child
// never inherits parent fds, only its argv.
func (g *FdBudgetGuard) Spawn(t TenantID, argv []string, inherit []string) (SpawnPlan, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.budgets[t]; !ok {
		return SpawnPlan{}, ErrUnknownTenant
	}
	cp := make([]string, len(argv))
	copy(cp, argv)
	return SpawnPlan{Argv: cp, FDs: []string{}}, nil
}

// Walk lists files under root, closing every directory handle.
func (g *FdBudgetGuard) Walk(t TenantID, root string, tree map[string][]string) ([]string, error) {
	if _, err := g.Usage(t); err != nil {
		return nil, err
	}
	var files []string
	var walk func(dir string) error
	walk = func(dir string) error {
		tk, err := g.Acquire(t, "dir", dir)
		if err != nil {
			return err
		}
		defer func() { _ = g.Release(tk) }()
		for _, name := range tree[dir] {
			p := path.Join(dir, name)
			files = append(files, p)
			if _, isDir := tree[p]; isDir {
				if err := walk(p); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return files, nil
}

// RecoverAfterRestart closes handles leaked by a previous process.
func (g *FdBudgetGuard) RecoverAfterRestart(state []Ticket) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	closed := 0
	for _, tk := range state {
		if _, ok := g.active[tk.ID]; ok {
			g.releaseLocked(tk)
		}
		closed++
	}
	return closed, nil
}
