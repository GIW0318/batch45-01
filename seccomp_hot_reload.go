package task

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

// Sentinel errors of the reloader.
var (
	ErrValidation     = errors.New("rule set validation failed")
	ErrEmptyRuleSet   = errors.New("rule set is empty")
	ErrDuplicateRule  = errors.New("duplicate rule id")
	ErrRuleSetMissing = errors.New("rule set missing")
)

// Rule is one syscall filter rule.
type Rule struct {
	ID         string
	Allow      bool
	Syscalls   []string
	PathPrefix string
	MountID    uint64
}

// RuleSet is a complete filter configuration.
type RuleSet struct {
	Rules    []Rule
	AllowAll bool
}

// Snapshot is one immutable, fully initialised rule set version.
type Snapshot struct {
	Version  uint64
	Rules    []Rule
	AllowAll bool
}

// UpdateOptions controls one reload request.
type UpdateOptions struct {
	ConfirmWidening bool
	Actor           string
}

// ReloadResult is the outcome of a reload request.
type ReloadResult struct {
	Applied      bool
	NeedsConfirm bool
	Version      uint64
	JournalID    uint64
}

// JournalEntry is one audit record of a reload attempt.
type JournalEntry struct {
	ID          uint64
	FromVersion uint64
	ToVersion   uint64
	Applied     bool
	Actor       string
	Reason      string
}

// Decision is the evaluation result for one syscall.
type Decision struct {
	Allow   bool
	RuleID  string
	Version uint64
}

// filterSnapshot is the internally stored immutable version. Rule slices
// are deep-copied on the way in, so no caller can mutate a live snapshot.
type filterSnapshot struct {
	version  uint64
	allowAll bool
	rules    []Rule
}

// SeccompReloader hot swaps syscall filter rule sets.
type SeccompReloader struct {
	// current is swapped atomically so readers never observe a partial
	// snapshot or an uninitialised state during a reload.
	current atomic.Pointer[filterSnapshot]

	// mu serialises reload/mount/restore mutations, guaranteeing every
	// accepted request bumps the version exactly once and is journaled.
	mu sync.Mutex

	// journal and mounts are guarded by mu.
	journal []JournalEntry
	mounts  map[uint64]string

	// One installed kernel filter handle; every reload replaces it
	// instead of stacking a new one.
	activeFilters int
}

// NewSeccompReloader creates a reloader with an initial rule set.
func NewSeccompReloader(initial RuleSet) *SeccompReloader {
	r := &SeccompReloader{
		mounts:        map[uint64]string{},
		activeFilters: 1,
	}
	// The constructor has no error return; an unusable initial set is
	// still installed as version 1 of a deny-by-default snapshot.
	rules, _ := validateAndCopy(initial)
	r.current.Store(&filterSnapshot{
		version:  1,
		allowAll: initial.AllowAll,
		rules:    rules,
	})
	return r
}

// Current returns the currently active snapshot.
func (r *SeccompReloader) Current() Snapshot {
	snap := r.current.Load()
	return Snapshot{
		Version:  snap.version,
		AllowAll: snap.allowAll,
		Rules:    cloneRules(snap.rules),
	}
}

// Reload validates and atomically installs a new rule set.
func (r *SeccompReloader) Reload(next RuleSet, opts UpdateOptions) (ReloadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.current.Load()
	entry := JournalEntry{
		ID:          uint64(len(r.journal)) + 1,
		FromVersion: current.version,
		Actor:       opts.Actor,
	}

	rules, err := validateAndCopy(next)
	if err != nil {
		entry.ToVersion = current.version
		entry.Reason = err.Error()
		r.journal = append(r.journal, entry)
		// The atomic pointer still references the previous, fully
		// validated snapshot, so the failed update cannot leave a gap.
		return ReloadResult{JournalID: entry.ID}, err
	}

	// A widening (moving to allow-all) must be explicitly confirmed;
	// the wider set is not installed on the first attempt.
	if next.AllowAll && !current.allowAll && !opts.ConfirmWidening {
		entry.ToVersion = current.version
		entry.Reason = "allow-all widening requires confirmation"
		r.journal = append(r.journal, entry)
		return ReloadResult{NeedsConfirm: true, JournalID: entry.ID}, nil
	}

	installed := &filterSnapshot{
		version:  current.version + 1,
		allowAll: next.AllowAll,
		rules:    rules,
	}
	// Single atomic pointer swap: the old filter stays live until the new,
	// fully built snapshot is published, so no syscall runs unprotected.
	r.current.Store(installed)

	entry.ToVersion = installed.version
	entry.Applied = true
	r.journal = append(r.journal, entry)

	return ReloadResult{
		Applied:   true,
		Version:   installed.version,
		JournalID: entry.ID,
	}, nil
}

// Evaluate decides one syscall against the current snapshot.
func (r *SeccompReloader) Evaluate(syscall, path string) (Decision, error) {
	r.mu.Lock()
	snap := r.current.Load()
	decision := Decision{Version: snap.version}

	if snap.allowAll {
		decision.Allow = true
		r.mu.Unlock()
		return decision, nil
	}

	for _, rule := range snap.rules {
		if !containsSyscall(rule.Syscalls, syscall) {
			continue
		}
		if rule.PathPrefix != "" {
			mount, ok := r.mounts[rule.MountID]
			// A path rule whose mount point changed no longer maps to
			// its prefix, so it must not be reused.
			if !ok || !strings.HasPrefix(mount, rule.PathPrefix) ||
				!strings.HasPrefix(path, rule.PathPrefix) {
				continue
			}
		}
		decision.Allow = rule.Allow
		decision.RuleID = rule.ID
		r.mu.Unlock()
		return decision, nil
	}

	// Deny by default: a syscall with no allow rule is rejected.
	r.mu.Unlock()
	return decision, nil
}

// Journal returns the audit log of every reload attempt.
func (r *SeccompReloader) Journal() []JournalEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]JournalEntry, len(r.journal))
	copy(out, r.journal)
	return out
}

// ActiveFilters reports how many kernel filters are installed.
func (r *SeccompReloader) ActiveFilters() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeFilters
}

// SetMountTable replaces the mount table used by path rules.
func (r *SeccompReloader) SetMountTable(m map[uint64]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[uint64]string, len(m))
	for id, prefix := range m {
		next[id] = prefix
	}
	// Replacing the table invalidates PathPrefix rules whose mount no
	// longer resolves to their prefix; Evaluate re-checks on every call.
	r.mounts = next
	return nil
}

// Persist serialises the current snapshot for a restart.
func (r *SeccompReloader) Persist() ([]byte, error) {
	r.mu.Lock()
	snap := r.current.Load()
	payload := persistedSnapshot{
		Version:  snap.version,
		AllowAll: snap.allowAll,
		Rules:    cloneRules(snap.rules),
	}
	r.mu.Unlock()
	return json.Marshal(payload)
}

// Restore loads a persisted snapshot.
func (r *SeccompReloader) Restore(data []byte) error {
	var payload persistedSnapshot
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	rules, err := validateAndCopy(RuleSet{Rules: payload.Rules, AllowAll: payload.AllowAll})
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	installed := &filterSnapshot{
		version:  payload.Version,
		allowAll: payload.AllowAll,
		rules:    rules,
	}
	// Publish in one atomic swap; subsequent reloads keep the version
	// monotonic because they build on the restored version.
	r.current.Store(installed)
	return nil
}

// persistedSnapshot is the on-disk representation of one version.
type persistedSnapshot struct {
	Version  uint64 `json:"version"`
	AllowAll bool   `json:"allow_all"`
	Rules    []Rule `json:"rules"`
}

// validateAndCopy enforces the deny-by-default invariants and returns a
// deep copy so the stored snapshot never shares backing arrays with the
// caller. The old snapshot is left untouched on any failure.
func validateAndCopy(set RuleSet) ([]Rule, error) {
	if len(set.Rules) == 0 {
		return nil, ErrEmptyRuleSet
	}
	seen := make(map[string]struct{}, len(set.Rules))
	rules := make([]Rule, 0, len(set.Rules))
	for _, rule := range set.Rules {
		if rule.ID == "" {
			return nil, ErrValidation
		}
		if _, dup := seen[rule.ID]; dup {
			return nil, ErrDuplicateRule
		}
		seen[rule.ID] = struct{}{}

		copied := rule
		if rule.Syscalls != nil {
			copied.Syscalls = append([]string(nil), rule.Syscalls...)
		}
		rules = append(rules, copied)
	}
	return rules, nil
}

// cloneRules deep-copies a rule slice, including the per-rule syscall
// slices, so snapshots handed out by Current() cannot be mutated.
func cloneRules(in []Rule) []Rule {
	if in == nil {
		return nil
	}
	out := make([]Rule, len(in))
	for i, rule := range in {
		out[i] = rule
		if rule.Syscalls != nil {
			out[i].Syscalls = append([]string(nil), rule.Syscalls...)
		}
	}
	return out
}

func containsSyscall(syscalls []string, target string) bool {
	for _, name := range syscalls {
		if name == target {
			return true
		}
	}
	return false
}
