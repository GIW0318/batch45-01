package task

import (
	"errors"
	"time"
)

// Event operations.
const (
	OpCreate = "create"
	OpWrite  = "write"
	OpRemove = "remove"
	OpRename = "rename"
)

var (
	ErrWatchLimit    = errors.New("integrity: inotify watch limit reached")
	ErrSymlinkEscape = errors.New("integrity: symlink target outside monitored roots")
)

// Options configures the watcher.
type Options struct {
	Roots         []string
	Excludes      []string
	MaxWatches    int
	QueueSize     int
	FollowSymlink bool
}

// Event is one filesystem notification plus the content captured at that
// instant.
type Event struct {
	Op         string
	Path       string
	OldPath    string
	IsDir      bool
	Container  string
	At         time.Time
	Content    []byte
	LinkTarget string
}

// FileState is the recorded state of one monitored file.
type FileState struct {
	Path       string
	Hash       string
	Containers []string
	Deleted    bool
	Reason     string
	UpdatedAt  time.Time
}

// IntegrityWatcher merges inotify events from many containers.
type IntegrityWatcher struct{}

// NewIntegrityWatcher creates a watcher and registers the configured roots.
func NewIntegrityWatcher(o Options) (*IntegrityWatcher, error) {
	return nil, errors.New("not implemented")
}

// AddDir adds one watch. Exceeding MaxWatches must fail loudly.
func (w *IntegrityWatcher) AddDir(path string) error {
	return errors.New("not implemented")
}

// Watches returns the watched directories.
func (w *IntegrityWatcher) Watches() []string {
	return nil
}

// Handle applies one event. The hash is computed from the content captured in
// the event, never from the file after the fact.
func (w *IntegrityWatcher) Handle(ev Event) error {
	return errors.New("not implemented")
}

// HashOf returns the latest hash of a path.
func (w *IntegrityWatcher) HashOf(path string) (string, bool) {
	return "", false
}

// HashAt returns the hash recorded for the event at the given instant.
func (w *IntegrityWatcher) HashAt(path string, at time.Time) (string, error) {
	return "", errors.New("not implemented")
}

// Baseline returns the current path to hash baseline.
func (w *IntegrityWatcher) Baseline() map[string]string {
	return nil
}

// RebuildBaseline recreates the baseline after a restart from a path to
// content snapshot.
func (w *IntegrityWatcher) RebuildBaseline(snapshot map[string]string) error {
	return errors.New("not implemented")
}

// Excluded reports whether path is excluded, comparing whole path components.
func (w *IntegrityWatcher) Excluded(path string) bool {
	return false
}

// Files returns a copy of the recorded file states.
func (w *IntegrityWatcher) Files() map[string]FileState {
	return nil
}

// Compensate resynchronises the given paths after a queue overflow.
func (w *IntegrityWatcher) Compensate(paths []string) error {
	return errors.New("not implemented")
}

// Overflowed reports whether events were dropped since the last compensation.
func (w *IntegrityWatcher) Overflowed() bool {
	return false
}

// Dropped returns the number of events dropped since the last compensation.
func (w *IntegrityWatcher) Dropped() int {
	return 0
}

// ReleaseContainer removes one container from every file reference and drops
// states that no longer have any reference.
func (w *IntegrityWatcher) ReleaseContainer(id string) error {
	return errors.New("not implemented")
}
