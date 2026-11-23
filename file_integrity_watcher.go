package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// BaselinePath is where the baseline is persisted atomically. When
	// empty, a default file inside os.TempDir() is used.
	BaselinePath string
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

// hashEntry is one historical hash observation of a path.
type hashEntry struct {
	at   time.Time
	hash string
}

// IntegrityWatcher merges inotify events from many containers.
type IntegrityWatcher struct {
	mu           sync.Mutex
	roots        []string
	excludes     []string
	maxWatches   int
	queueSize    int
	followLink   bool
	baselinePath string

	watches    map[string]struct{}
	queue      []string
	dropped    int
	overflowed bool

	files    map[string]FileState
	renamed  map[string]bool
	history  map[string][]hashEntry
	baseline map[string]string
}

func shaHex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func cleanPath(p string) string {
	if p == "" {
		return p
	}
	return filepath.Clean(p)
}

// withinRoot reports whether p equals root or lives under it, comparing
// whole path components so "/data/tmp" never matches "/data/tmp2".
func withinRoot(root, p string) bool {
	if root == "" {
		return false
	}
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

// NewIntegrityWatcher creates a watcher and registers the configured roots.
func NewIntegrityWatcher(o Options) (*IntegrityWatcher, error) {
	w := &IntegrityWatcher{
		maxWatches:   o.MaxWatches,
		queueSize:    o.QueueSize,
		followLink:   o.FollowSymlink,
		baselinePath: o.BaselinePath,
		watches:      map[string]struct{}{},
		files:        map[string]FileState{},
		renamed:      map[string]bool{},
		history:      map[string][]hashEntry{},
		baseline:     map[string]string{},
	}
	if w.baselinePath == "" {
		w.baselinePath = filepath.Join(os.TempDir(), "gsb-integrity-baseline.json")
	}
	for _, r := range o.Roots {
		w.roots = append(w.roots, cleanPath(r))
	}
	for _, e := range o.Excludes {
		w.excludes = append(w.excludes, cleanPath(e))
	}
	for _, r := range o.Roots {
		if err := w.AddDir(r); err != nil {
			return nil, err
		}
	}
	// Reload the persisted baseline so a restart does not lose it. A
	// half-written (torn) file is reported instead of silently ignored.
	base, err := loadBaseline(w.baselinePath)
	if err != nil {
		return nil, err
	}
	w.baseline = base
	return w, nil
}

func loadBaseline(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("integrity: read baseline: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("integrity: corrupt baseline file %s: %w", path, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// persistBaseline writes the whole baseline to a temporary file in the same
// directory and atomically renames it over the old one, so a power cut can
// only ever expose the complete old or the complete new baseline.
func (w *IntegrityWatcher) persistBaselineLocked() error {
	data, err := json.Marshal(w.baseline)
	if err != nil {
		return fmt.Errorf("integrity: encode baseline: %w", err)
	}
	dir := filepath.Dir(w.baselinePath)
	tmp, err := os.CreateTemp(dir, ".integrity-baseline-*")
	if err != nil {
		return fmt.Errorf("integrity: create temp baseline: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("integrity: write temp baseline: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("integrity: fsync temp baseline: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("integrity: close temp baseline: %w", err)
	}
	if err := os.Rename(tmpName, w.baselinePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("integrity: replace baseline: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// AddDir adds one watch. Exceeding MaxWatches must fail loudly.
func (w *IntegrityWatcher) AddDir(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.addWatchLocked(path)
}

func (w *IntegrityWatcher) addWatchLocked(path string) error {
	p := cleanPath(path)
	if _, ok := w.watches[p]; ok {
		return nil
	}
	if w.maxWatches > 0 && len(w.watches) >= w.maxWatches {
		return ErrWatchLimit
	}
	w.watches[p] = struct{}{}
	return nil
}

// Watches returns the watched directories.
func (w *IntegrityWatcher) Watches() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.watches))
	for d := range w.watches {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Handle applies one event. The hash is computed from the content captured in
// the event, never from the file after the fact.
func (w *IntegrityWatcher) Handle(ev Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.excludedLocked(ev.Path) || (ev.OldPath != "" && w.excludedLocked(ev.OldPath)) {
		return nil
	}
	if ev.LinkTarget != "" && !w.followLink && !w.insideRootsLocked(ev.LinkTarget) {
		return ErrSymlinkEscape
	}
	if w.queueSize > 0 && len(w.queue) >= w.queueSize {
		w.dropped++
		w.overflowed = true
		return nil
	}
	w.queue = append(w.queue, ev.Path)

	at := ev.At.UTC()
	path := cleanPath(ev.Path)

	switch ev.Op {
	case OpCreate, OpWrite:
		if ev.IsDir {
			// Recursively watch freshly created subdirectories.
			if err := w.addWatchLocked(path); err != nil {
				return err
			}
			return nil
		}
		w.applyWriteLocked(path, ev.Container, at, shaHex(ev.Content))
	case OpRename:
		old := cleanPath(ev.OldPath)
		if ev.IsDir {
			delete(w.watches, old)
			if err := w.addWatchLocked(path); err != nil {
				return err
			}
		}
		st, ok := w.files[old]
		if ok {
			delete(w.files, old)
		} else {
			st = FileState{}
		}
		st.Path = path
		st.Hash = shaHex(ev.Content)
		if len(ev.Content) == 0 && ok {
			st.Hash = w.historyLastLocked(old, at)
		}
		st.Deleted = false
		st.Reason = ""
		st.UpdatedAt = at
		st.Containers = appendUnique(st.Containers, ev.Container)
		w.files[path] = st
		w.renamed[path] = true
		delete(w.renamed, old)
		w.history[path] = append(w.history[path], hashEntry{at: at, hash: st.Hash})
	case OpRemove:
		st, ok := w.files[path]
		if !ok {
			st = FileState{Path: path}
		}
		st.Deleted = true
		st.UpdatedAt = at
		if w.renamed[path] {
			st.Reason = "renamed-then-deleted"
		} else if st.Reason == "" {
			st.Reason = "unknown"
		}
		w.files[path] = st
	}
	return nil
}

func (w *IntegrityWatcher) applyWriteLocked(path, container string, at time.Time, hash string) {
	st, ok := w.files[path]
	if !ok {
		st = FileState{Path: path}
	}
	st.Containers = appendUnique(st.Containers, container)
	// Apply writes in event-time order so late arrivals never clobber a
	// newer observation.
	if !ok || !at.Before(st.UpdatedAt) {
		st.Hash = hash
		st.Deleted = false
		st.Reason = ""
		st.UpdatedAt = at
	}
	w.files[path] = st
	w.history[path] = append(w.history[path], hashEntry{at: at, hash: hash})
}

func (w *IntegrityWatcher) historyLastLocked(path string, at time.Time) string {
	best := ""
	var bestAt time.Time
	for _, h := range w.history[path] {
		if !h.at.After(at) && (best == "" || !h.at.Before(bestAt)) {
			best, bestAt = h.hash, h.at
		}
	}
	return best
}

func appendUnique(list []string, id string) []string {
	if id == "" {
		return list
	}
	for _, existing := range list {
		if existing == id {
			return list
		}
	}
	return append(list, id)
}

func (w *IntegrityWatcher) excludedLocked(path string) bool {
	p := cleanPath(path)
	for _, ex := range w.excludes {
		if withinRoot(ex, p) {
			return true
		}
	}
	return false
}

func (w *IntegrityWatcher) insideRootsLocked(target string) bool {
	t := cleanPath(target)
	for _, r := range w.roots {
		if withinRoot(r, t) {
			return true
		}
	}
	return false
}

// HashOf returns the latest hash of a path.
func (w *IntegrityWatcher) HashOf(path string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, ok := w.files[cleanPath(path)]
	if !ok {
		return "", false
	}
	return st.Hash, true
}

// HashAt returns the hash recorded for the event at the given instant.
func (w *IntegrityWatcher) HashAt(path string, at time.Time) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	at = at.UTC()
	best := ""
	var bestAt time.Time
	found := false
	for _, h := range w.history[cleanPath(path)] {
		if !h.at.After(at) && (!found || !h.at.Before(bestAt)) {
			best, bestAt, found = h.hash, h.at, true
		}
	}
	if !found {
		return "", fmt.Errorf("integrity: no hash recorded for %s at or before %s", path, at)
	}
	return best, nil
}

// Baseline returns the current path to hash baseline.
func (w *IntegrityWatcher) Baseline() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.baseline))
	for k, v := range w.baseline {
		out[k] = v
	}
	return out
}

// RebuildBaseline recreates the baseline after a restart from a path to
// content snapshot.
func (w *IntegrityWatcher) RebuildBaseline(snapshot map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	base := make(map[string]string, len(snapshot))
	for p, content := range snapshot {
		base[cleanPath(p)] = shaHex([]byte(content))
	}
	w.baseline = base
	return w.persistBaselineLocked()
}

// Excluded reports whether path is excluded, comparing whole path components.
func (w *IntegrityWatcher) Excluded(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.excludedLocked(path)
}

// Files returns a copy of the recorded file states.
func (w *IntegrityWatcher) Files() map[string]FileState {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]FileState, len(w.files))
	for k, v := range w.files {
		v.Containers = append([]string(nil), v.Containers...)
		out[k] = v
	}
	return out
}

// Compensate resynchronises the given paths after a queue overflow.
func (w *IntegrityWatcher) Compensate(paths []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, p := range paths {
		p = cleanPath(p)
		st, ok := w.files[p]
		if !ok {
			st = FileState{Path: p}
		}
		st.Reason = "resynced"
		w.files[p] = st
	}
	w.queue = w.queue[:0]
	w.dropped = 0
	w.overflowed = false
	return nil
}

// Overflowed reports whether events were dropped since the last compensation.
func (w *IntegrityWatcher) Overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflowed
}

// Dropped returns the number of events dropped since the last compensation.
func (w *IntegrityWatcher) Dropped() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped
}

// ReleaseContainer removes one container from every file reference and drops
// states that no longer have any reference.
func (w *IntegrityWatcher) ReleaseContainer(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for p, st := range w.files {
		kept := st.Containers[:0]
		had := false
		for _, c := range st.Containers {
			if c == id {
				had = true
				continue
			}
			kept = append(kept, c)
		}
		if !had {
			continue
		}
		if len(kept) == 0 {
			delete(w.files, p)
			delete(w.history, p)
			delete(w.renamed, p)
			continue
		}
		st.Containers = kept
		w.files[p] = st
	}
	return nil
}
