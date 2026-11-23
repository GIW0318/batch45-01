package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
)

var (
	errInvalidPropagation  = errors.New("propagation mode not allowed")
	errReadOnlyConflict    = errors.New("read only mount carries a read write option")
	errUIDMapMismatch      = errors.New("uid map does not match the host uid")
	errSourceEscapesRoot   = errors.New("source resolves outside the allowed root")
	errTooManyMounts       = errors.New("mount count exceeds the configured maximum")
	errDuplicateTarget     = errors.New("two mounts share the same target")
	errTransactionConflict = errors.New("container already holds a different mount set")
	errNilTransaction      = errors.New("nil transaction")
)

const (
	opMount   = "mount"
	opUnmount = "unmount"

	defaultPropagation = "private"
)

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
type MountTransactionGuard struct {
	opts GuardOptions

	mu         sync.RWMutex
	containers map[string]*containerState
	locks      map[string]*sync.Mutex
	journal    map[string]*journalEntry
}

type containerState struct {
	mounts      []Mount
	propagation string
	applied     bool
}

// journalEntry describes an in-flight transaction so that a snapshot taken
// mid-apply can be cleaned up after a restart.
type journalEntry struct {
	ContainerID string `json:"container_id"`
	Total       int    `json:"total"`
	Done        int    `json:"done"`
	Complete    bool   `json:"complete"`
}

// NewMountTransactionGuard returns a guard configured by opts.
func NewMountTransactionGuard(opts GuardOptions) *MountTransactionGuard {
	return &MountTransactionGuard{
		opts:       opts,
		containers: make(map[string]*containerState),
		locks:      make(map[string]*sync.Mutex),
		journal:    make(map[string]*journalEntry),
	}
}

// CreateContainer validates specs and returns the pending transaction.
func (g *MountTransactionGuard) CreateContainer(id string, specs []MountSpec) (*Transaction, error) {
	if g.opts.MaxMounts > 0 && len(specs) > g.opts.MaxMounts {
		return nil, fmt.Errorf("%w: %d > %d", errTooManyMounts, len(specs), g.opts.MaxMounts)
	}
	seen := make(map[string]struct{}, len(specs))
	copied := make([]MountSpec, len(specs))
	for i, s := range specs {
		if err := g.ValidateSpec(s); err != nil {
			return nil, err
		}
		if _, dup := seen[s.Target]; dup {
			return nil, fmt.Errorf("%w: %s", errDuplicateTarget, s.Target)
		}
		seen[s.Target] = struct{}{}
		copied[i] = cloneSpec(s)
	}
	return &Transaction{ContainerID: id, Specs: copied}, nil
}

// Apply mounts every spec, rolling back on the first failure.
func (g *MountTransactionGuard) Apply(tx *Transaction, exec func(Op) error) ([]Op, error) {
	if tx == nil {
		return nil, errNilTransaction
	}
	lock := g.containerLock(tx.ContainerID)
	lock.Lock()
	defer lock.Unlock()

	g.mu.RLock()
	st := g.containers[tx.ContainerID]
	g.mu.RUnlock()
	if st != nil && st.applied {
		if sameSpecs(st.mounts, tx.Specs) {
			// Idempotent replay: the transaction already ran, do not mount twice.
			return mountOps(st.mounts), nil
		}
		return nil, errTransactionConflict
	}

	// Journal first, then touch mount points.
	g.journalStart(tx.ContainerID, len(tx.Specs))

	var ops []Op
	var applied []Mount
	rollback := func(cause error) ([]Op, error) {
		for i := len(applied) - 1; i >= 0; i-- {
			uop := Op{Kind: opUnmount, Source: applied[i].Spec.Source, Target: applied[i].Spec.Target}
			_ = exec(uop) // best effort rollback
			ops = append(ops, uop)
		}
		g.dropContainer(tx.ContainerID)
		g.journalDrop(tx.ContainerID)
		return ops, cause
	}

	for i, s := range tx.Specs {
		if g.opts.MaxMounts > 0 && i >= g.opts.MaxMounts {
			return rollback(errTooManyMounts)
		}
		op := Op{Kind: opMount, Source: s.Source, Target: s.Target}
		if err := exec(op); err != nil {
			return rollback(err)
		}
		ops = append(ops, op)
		m := Mount{Spec: cloneSpec(s), ContainerID: tx.ContainerID}
		applied = append(applied, m)
		g.recordMount(tx.ContainerID, m)
		g.journalAdvance(tx.ContainerID, i+1)
	}

	prop := defaultPropagation
	if len(tx.Specs) > 0 {
		prop = normalizePropagation(tx.Specs[0].Propagation)
	}
	g.markApplied(tx.ContainerID, prop)
	g.journalComplete(tx.ContainerID)
	return ops, nil
}

// Cleanup unmounts everything the container still holds.
func (g *MountTransactionGuard) Cleanup(id string, exec func(Op) error) ([]Op, error) {
	lock := g.containerLock(id)
	lock.Lock()
	defer lock.Unlock()

	g.mu.RLock()
	st := g.containers[id]
	var mounts []Mount
	if st != nil {
		mounts = append(mounts, st.mounts...)
	}
	g.mu.RUnlock()

	var ops []Op
	// Unmount in reverse mount order.
	for i := len(mounts) - 1; i >= 0; i-- {
		op := Op{Kind: opUnmount, Source: mounts[i].Spec.Source, Target: mounts[i].Spec.Target}
		if err := exec(op); err != nil {
			// Keep the failed mount and everything not yet attempted.
			g.setMounts(id, mounts[:i+1])
			return ops, err
		}
		ops = append(ops, op)
	}
	g.dropContainer(id)
	g.journalDrop(id)
	return ops, nil
}

// Mounts lists all applied mounts.
func (g *MountTransactionGuard) Mounts() []Mount {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := make([]string, 0, len(g.containers))
	for id := range g.containers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []Mount
	for _, id := range ids {
		out = append(out, g.containers[id].mounts...)
	}
	return out
}

// MountsFor lists the mounts of one container.
func (g *MountTransactionGuard) MountsFor(id string) []Mount {
	g.mu.RLock()
	defer g.mu.RUnlock()
	st := g.containers[id]
	if st == nil {
		return nil
	}
	return append([]Mount(nil), st.mounts...)
}

// ValidateSpec validates one mount specification.
func (g *MountTransactionGuard) ValidateSpec(s MountSpec) error {
	prop := normalizePropagation(s.Propagation)
	if !g.allowedPropagation(prop) {
		return fmt.Errorf("%w: %q", errInvalidPropagation, s.Propagation)
	}
	if s.ReadOnly {
		for _, opt := range s.Options {
			for _, tok := range strings.Split(opt, ",") {
				if strings.TrimSpace(tok) == "rw" {
					return errReadOnlyConflict
				}
			}
		}
	}
	if s.UIDMap != s.HostUID {
		return fmt.Errorf("%w: uid map %d != host uid %d", errUIDMapMismatch, s.UIDMap, s.HostUID)
	}
	return g.checkSource(s.Source)
}

// PropagationFor reports the mount propagation mode of a container.
func (g *MountTransactionGuard) PropagationFor(id string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if st := g.containers[id]; st != nil && st.propagation != "" {
		return st.propagation
	}
	return defaultPropagation
}

// Snapshot serialises the mount table state.
func (g *MountTransactionGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	snap := snapshot{
		Containers: make(map[string]snapshotContainer, len(g.containers)),
		Journal:    make(map[string]journalEntry, len(g.journal)),
	}
	for id, st := range g.containers {
		snap.Containers[id] = snapshotContainer{
			Mounts:      append([]Mount(nil), st.mounts...),
			Propagation: st.propagation,
			Applied:     st.applied,
		}
	}
	for id, e := range g.journal {
		snap.Journal[id] = *e
	}
	return json.Marshal(snap)
}

// Restore rebuilds mount state from a snapshot.
func (g *MountTransactionGuard) Restore(data []byte) error {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.containers = make(map[string]*containerState, len(snap.Containers))
	g.journal = make(map[string]*journalEntry, len(snap.Journal))
	for id, e := range snap.Journal {
		entry := e
		g.journal[id] = &entry
	}
	for id, sc := range snap.Containers {
		if e, ok := snap.Journal[id]; ok && !e.Complete {
			// The transaction was in flight when the snapshot was taken:
			// drop the half applied set instead of resurrecting it.
			continue
		}
		mounts := append([]Mount(nil), sc.Mounts...)
		for i := range mounts {
			mounts[i].Spec = cloneSpec(mounts[i].Spec)
			mounts[i].ContainerID = id
		}
		prop := sc.Propagation
		if prop == "" {
			prop = defaultPropagation
		}
		g.containers[id] = &containerState{mounts: mounts, propagation: prop, applied: sc.Applied}
	}
	return nil
}

type snapshotContainer struct {
	Mounts      []Mount `json:"mounts"`
	Propagation string  `json:"propagation"`
	Applied     bool    `json:"applied"`
}

type snapshot struct {
	Containers map[string]snapshotContainer `json:"containers"`
	Journal    map[string]journalEntry      `json:"journal"`
}

func normalizePropagation(p string) string {
	if p == "" {
		return defaultPropagation
	}
	return p
}

func (g *MountTransactionGuard) allowedPropagation(p string) bool {
	allowed := g.opts.AllowedPropagation
	if len(allowed) == 0 {
		allowed = []string{"private", "slave"}
	}
	for _, a := range allowed {
		if a == p {
			return true
		}
	}
	return false
}

func (g *MountTransactionGuard) checkSource(source string) error {
	if g.opts.Root == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(g.opts.Root)
	if err != nil {
		root, err = filepath.Abs(g.opts.Root)
		if err != nil {
			return err
		}
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("%w: %v", errSourceEscapesRoot, err)
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s", errSourceEscapesRoot, resolved)
	}
	return nil
}

func cloneSpec(s MountSpec) MountSpec {
	out := s
	if s.Options != nil {
		out.Options = append([]string(nil), s.Options...)
	}
	return out
}

func sameSpecs(mounts []Mount, specs []MountSpec) bool {
	if len(mounts) != len(specs) {
		return false
	}
	for i := range mounts {
		a, b := mounts[i].Spec, specs[i]
		if a.Source != b.Source || a.Target != b.Target || a.FSType != b.FSType ||
			a.ReadOnly != b.ReadOnly || a.Propagation != b.Propagation ||
			a.UIDMap != b.UIDMap || a.HostUID != b.HostUID ||
			!reflect.DeepEqual(a.Options, b.Options) {
			return false
		}
	}
	return true
}

func mountOps(mounts []Mount) []Op {
	ops := make([]Op, 0, len(mounts))
	for _, m := range mounts {
		ops = append(ops, Op{Kind: opMount, Source: m.Spec.Source, Target: m.Spec.Target})
	}
	return ops
}

func (g *MountTransactionGuard) containerLock(id string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	l, ok := g.locks[id]
	if !ok {
		l = &sync.Mutex{}
		g.locks[id] = l
	}
	return l
}

func (g *MountTransactionGuard) recordMount(id string, m Mount) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.containers[id]
	if st == nil {
		st = &containerState{propagation: defaultPropagation}
		g.containers[id] = st
	}
	st.mounts = append(st.mounts, m)
}

func (g *MountTransactionGuard) markApplied(id, propagation string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.containers[id]
	if st == nil {
		st = &containerState{}
		g.containers[id] = st
	}
	st.propagation = propagation
	st.applied = true
}

func (g *MountTransactionGuard) setMounts(id string, mounts []Mount) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.containers[id]
	if st == nil {
		st = &containerState{propagation: defaultPropagation}
		g.containers[id] = st
	}
	st.mounts = append([]Mount(nil), mounts...)
}

func (g *MountTransactionGuard) dropContainer(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.containers, id)
}

func (g *MountTransactionGuard) journalStart(id string, total int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.journal[id] = &journalEntry{ContainerID: id, Total: total}
}

func (g *MountTransactionGuard) journalAdvance(id string, done int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.journal[id]; e != nil {
		e.Done = done
	}
}

func (g *MountTransactionGuard) journalComplete(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.journal[id]; e != nil {
		e.Complete = true
	}
}

func (g *MountTransactionGuard) journalDrop(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.journal, id)
}
