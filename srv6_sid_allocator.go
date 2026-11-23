package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SIDAllocator is the controller-side SID allocation table. It persists every
// mutation to disk so that a SID already handed out is never re-allocated
// after a crash or restart. Reclamation is reference-counted and counts never
// drop below zero. BSID bindings are first-wins: concurrent binds of the same
// BSID to different policies succeed exactly once.
type SIDAllocator struct {
	mu       sync.Mutex
	path     string
	refs     map[string]int
	bindings map[string]string
}

type sidAllocatorState struct {
	Refs     map[string]int    `json:"refs"`
	Bindings map[string]string `json:"bindings"`
}

// LoadSIDAllocator opens the allocation table stored at path, recovering the
// state left behind by a previous run. A missing file starts an empty table.
func LoadSIDAllocator(path string) (*SIDAllocator, error) {
	a := &SIDAllocator{
		path:     path,
		refs:     map[string]int{},
		bindings: map[string]string{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read allocator state: %w", err)
	}
	var st sidAllocatorState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("decode allocator state: %w", err)
	}
	for sid, n := range st.Refs {
		if n < 0 {
			return nil, fmt.Errorf("corrupt allocator state: SID %q has negative refcount %d", sid, n)
		}
		a.refs[sid] = n
	}
	for bsid, policy := range st.Bindings {
		a.bindings[bsid] = policy
	}
	return a, nil
}

// persist writes the table atomically: temp file, fsync, rename.
func (a *SIDAllocator) persistLocked() error {
	st := sidAllocatorState{Refs: a.refs, Bindings: a.bindings}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encode allocator state: %w", err)
	}
	tmp := a.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write allocator state: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write allocator state: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync allocator state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close allocator state: %w", err)
	}
	if err := os.Rename(tmp, a.path); err != nil {
		return fmt.Errorf("commit allocator state: %w", err)
	}
	return nil
}

// Allocate hands out a SID. A SID that is already allocated — including by a
// previous incarnation of the controller recovered from disk — is never
// handed out again.
func (a *SIDAllocator) Allocate(sid string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.refs[sid]; ok {
		return fmt.Errorf("SID %q already allocated", sid)
	}
	a.refs[sid] = 1
	return a.persistLocked()
}

// Retain adds a reference to an allocated SID.
func (a *SIDAllocator) Retain(sid string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.refs[sid]; !ok {
		return fmt.Errorf("SID %q is not allocated", sid)
	}
	a.refs[sid]++
	return a.persistLocked()
}

// Release drops one reference. The count never goes below zero; releasing an
// unknown SID is an error.
func (a *SIDAllocator) Release(sid string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n, ok := a.refs[sid]
	if !ok {
		return 0, fmt.Errorf("SID %q is not allocated", sid)
	}
	if n > 0 {
		n--
		a.refs[sid] = n
	}
	return n, a.persistLocked()
}

// RefCount reports the current reference count of a SID.
func (a *SIDAllocator) RefCount(sid string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refs[sid]
}

// IsAllocated reports whether a SID has been handed out.
func (a *SIDAllocator) IsAllocated(sid string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.refs[sid]
	return ok
}

// Bind binds a BSID to a policy. The first binder wins; rebinding the same
// policy is idempotent, binding a different policy fails.
func (a *SIDAllocator) Bind(bsid, policy string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if owner, ok := a.bindings[bsid]; ok {
		if owner == policy {
			return nil
		}
		return fmt.Errorf("BSID %q already bound to policy %q", bsid, owner)
	}
	a.bindings[bsid] = policy
	return a.persistLocked()
}

// BoundPolicy reports the policy a BSID is bound to.
func (a *SIDAllocator) BoundPolicy(bsid string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.bindings[bsid]
	return p, ok
}

// Remove deletes the persisted state file. Used by tests and tools to reset.
func RemoveSIDAllocatorState(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(path + ".tmp"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// StatePath returns the file the allocator persists to.
func (a *SIDAllocator) StatePath() string {
	return filepath.Clean(a.path)
}
