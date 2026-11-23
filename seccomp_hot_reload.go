package task

import "errors"

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

// SeccompReloader hot swaps syscall filter rule sets.
type SeccompReloader struct {
}

// NewSeccompReloader creates a reloader with an initial rule set.
func NewSeccompReloader(initial RuleSet) *SeccompReloader {
	return &SeccompReloader{}
}

// Current returns the currently active snapshot.
func (r *SeccompReloader) Current() Snapshot {
	return Snapshot{}
}

// Reload validates and atomically installs a new rule set.
func (r *SeccompReloader) Reload(next RuleSet, opts UpdateOptions) (ReloadResult, error) {
	return ReloadResult{}, errors.New("not implemented")
}

// Evaluate decides one syscall against the current snapshot.
func (r *SeccompReloader) Evaluate(syscall, path string) (Decision, error) {
	return Decision{}, errors.New("not implemented")
}

// Journal returns the audit log of every reload attempt.
func (r *SeccompReloader) Journal() []JournalEntry {
	return nil
}

// ActiveFilters reports how many kernel filters are installed.
func (r *SeccompReloader) ActiveFilters() int {
	return 0
}

// SetMountTable replaces the mount table used by path rules.
func (r *SeccompReloader) SetMountTable(m map[uint64]string) error {
	return errors.New("not implemented")
}

// Persist serialises the current snapshot for a restart.
func (r *SeccompReloader) Persist() ([]byte, error) {
	return nil, errors.New("not implemented")
}

// Restore loads a persisted snapshot.
func (r *SeccompReloader) Restore(data []byte) error {
	return errors.New("not implemented")
}
