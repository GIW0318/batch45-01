package task

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-16"
)

func newWatcher(t *testing.T, o Options) *IntegrityWatcher {
	t.Helper()
	w, err := NewIntegrityWatcher(o)
	if err != nil {
		t.Fatalf("NewIntegrityWatcher: %v", err)
	}
	return w
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hasWatch(w *IntegrityWatcher, dir string) bool {
	for _, got := range w.Watches() {
		if got == dir {
			return true
		}
	}
	return false
}

func TestWatchLimitReportsError(t *testing.T) {
	w := newWatcher(t, Options{MaxWatches: 2})
	if err := w.AddDir("/w/a"); err != nil {
		t.Fatalf("add /w/a: %v", err)
	}
	if err := w.AddDir("/w/b"); err != nil {
		t.Fatalf("add /w/b: %v", err)
	}
	if err := w.AddDir("/w/c"); !errors.Is(err, ErrWatchLimit) {
		t.Fatalf("watch beyond limit error = %v, want ErrWatchLimit", err)
	}
	if got := len(w.Watches()); got != 2 {
		t.Fatalf("watches = %d, want 2", got)
	}
	if err := w.AddDir("/w/a"); err != nil {
		t.Fatalf("re-adding an existing watch: %v", err)
	}
	if got := len(w.Watches()); got != 2 {
		t.Fatalf("watches = %d after duplicate add, want 2", got)
	}
}

func TestMultiContainerRefsKept(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.Handle(Event{Op: OpCreate, Path: "/w/f", Container: "c1", At: time.Unix(1700000000, 0), Content: []byte("v1")}); err != nil {
		t.Fatalf("create from c1: %v", err)
	}
	if err := w.Handle(Event{Op: OpWrite, Path: "/w/f", Container: "c2", At: time.Unix(1700000001, 0), Content: []byte("v2")}); err != nil {
		t.Fatalf("write from c2: %v", err)
	}
	fs, ok := w.Files()["/w/f"]
	if !ok {
		t.Fatalf("file not recorded")
	}
	got := append([]string{}, fs.Containers...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "c1" || got[1] != "c2" {
		t.Fatalf("containers = %v, want [c1 c2]", fs.Containers)
	}
}

func TestQueueOverflowCompensated(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8, QueueSize: 1})
	for _, p := range []string{"/w/a", "/w/b", "/w/c"} {
		if err := w.Handle(Event{Op: OpCreate, Path: p, Container: "c1", At: time.Unix(1700000000, 0), Content: []byte(p)}); err != nil {
			t.Fatalf("handle %s: %v", p, err)
		}
	}
	if !w.Overflowed() {
		t.Fatalf("overflow not reported after exceeding the queue")
	}
	if got := w.Dropped(); got != 2 {
		t.Fatalf("dropped = %d, want 2", got)
	}
	if err := w.Compensate([]string{"/w/a"}); err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if w.Overflowed() {
		t.Fatalf("overflow flag not cleared by compensation")
	}
	fs, ok := w.Files()["/w/a"]
	if !ok || fs.Reason != "resynced" {
		t.Fatalf("compensated state = %+v, want reason resynced", fs)
	}
}

func TestRenameThenDeleteLinked(t *testing.T) {
	at := time.Unix(1700000000, 0)
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.Handle(Event{Op: OpCreate, Path: "/w/a", At: at, Content: []byte("v1")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.Handle(Event{Op: OpRename, OldPath: "/w/a", Path: "/w/b", At: at.Add(time.Second), Content: []byte("v1")}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := w.Handle(Event{Op: OpRemove, Path: "/w/b", At: at.Add(2 * time.Second)}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	files := w.Files()
	if _, ok := files["/w/a"]; ok {
		t.Fatalf("old path still recorded after rename")
	}
	fs, ok := files["/w/b"]
	if !ok {
		t.Fatalf("renamed path not recorded")
	}
	if !fs.Deleted || fs.Reason != "renamed-then-deleted" {
		t.Fatalf("state = %+v, want deleted with linked rename reason", fs)
	}
}

func TestNewSubdirWatchedRecursively(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.Handle(Event{Op: OpCreate, Path: "/w/sub", IsDir: true, At: time.Unix(1700000000, 0)}); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	if !hasWatch(w, "/w/sub") {
		t.Fatalf("new subdirectory was not watched: %v", w.Watches())
	}
	if err := w.Handle(Event{Op: OpCreate, Path: "/w/sub/file", At: time.Unix(1700000001, 0), Content: []byte("x")}); err != nil {
		t.Fatalf("create file in new subdir: %v", err)
	}
	if _, ok := w.Files()["/w/sub/file"]; !ok {
		t.Fatalf("file inside the new subdirectory was not recorded")
	}
}

func TestSymlinkOutsideRootNotFollowed(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	escape := Event{Op: OpCreate, Path: "/w/escape", LinkTarget: "/etc/shadow", At: time.Unix(1700000000, 0)}
	if err := w.Handle(escape); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("outside symlink error = %v, want ErrSymlinkEscape", err)
	}
	if _, ok := w.Files()["/w/escape"]; ok {
		t.Fatalf("outside symlink target was recorded")
	}
	inside := Event{Op: OpCreate, Path: "/w/inside", LinkTarget: "/w/target", At: time.Unix(1700000001, 0)}
	if err := w.Handle(inside); err != nil {
		t.Fatalf("inside symlink: %v", err)
	}
	follower := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8, FollowSymlink: true})
	if err := follower.Handle(escape); err != nil {
		t.Fatalf("follow symlink mode rejected the target: %v", err)
	}
}

func TestHashComputedAtEventTime(t *testing.T) {
	t1 := time.Unix(1700000000, 0)
	t2 := t1.Add(time.Second)
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.Handle(Event{Op: OpCreate, Path: "/w/f", At: t1, Content: []byte("v1")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.Handle(Event{Op: OpWrite, Path: "/w/f", At: t2, Content: []byte("v2")}); err != nil {
		t.Fatalf("write: %v", err)
	}
	atEvent, err := w.HashAt("/w/f", t1)
	if err != nil {
		t.Fatalf("hash at t1: %v", err)
	}
	if atEvent != shaHex([]byte("v1")) {
		t.Fatalf("hash at event time = %s, want hash of v1", atEvent)
	}
	current, ok := w.HashOf("/w/f")
	if !ok {
		t.Fatalf("no current hash")
	}
	if current != shaHex([]byte("v2")) {
		t.Fatalf("current hash = %s, want hash of v2", current)
	}
}

func TestConcurrentWritesHashStable(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	allowed := map[string]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		content := []byte("content-" + string(rune('a'+i)))
		allowed[shaHex(content)] = true
		wg.Add(1)
		go func(content []byte, i int) {
			defer wg.Done()
			_ = w.Handle(Event{Op: OpWrite, Path: "/w/f", At: time.Unix(int64(1700000000+i), 0), Content: content})
		}(content, i)
	}
	wg.Wait()
	hash, ok := w.HashOf("/w/f")
	if !ok {
		t.Fatalf("no hash after concurrent writes")
	}
	if !allowed[hash] {
		t.Fatalf("hash %s is not a complete content hash (torn read)", hash)
	}
	fs, ok := w.Files()["/w/f"]
	if !ok || fs.Hash != hash {
		t.Fatalf("file state hash = %q, want %q", fs.Hash, hash)
	}
}

func TestBaselineRebuiltAfterRestart(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.RebuildBaseline(map[string]string{"/w/a": "v1", "/w/b": "v2"}); err != nil {
		t.Fatalf("rebuild baseline: %v", err)
	}
	baseline := w.Baseline()
	if len(baseline) != 2 {
		t.Fatalf("baseline = %d entries, want 2", len(baseline))
	}
	if baseline["/w/a"] != shaHex([]byte("v1")) {
		t.Fatalf("baseline hash for /w/a = %s, want hash of v1", baseline["/w/a"])
	}
	if baseline["/w/b"] != shaHex([]byte("v2")) {
		t.Fatalf("baseline hash for /w/b = %s, want hash of v2", baseline["/w/b"])
	}
}

func TestExcludeIsPathComponentAware(t *testing.T) {
	w := newWatcher(t, Options{
		Roots:      []string{"/var/lib"},
		Excludes:   []string{"/var/lib/excl"},
		MaxWatches: 8,
	})
	if !w.Excluded("/var/lib/excl/a") {
		t.Fatalf("child of excluded path not excluded")
	}
	if !w.Excluded("/var/lib/excl") {
		t.Fatalf("excluded path itself not excluded")
	}
	if w.Excluded("/var/lib/excl_other/a") {
		t.Fatalf("similar prefix was excluded by string prefix matching")
	}
	if err := w.Handle(Event{Op: OpCreate, Path: "/var/lib/excl/a", At: time.Unix(1700000000, 0), Content: []byte("x")}); err != nil {
		t.Fatalf("handle excluded: %v", err)
	}
	if _, ok := w.Files()["/var/lib/excl/a"]; ok {
		t.Fatalf("excluded file was recorded")
	}
	if err := w.Handle(Event{Op: OpCreate, Path: "/var/lib/excl_other/a", At: time.Unix(1700000001, 0), Content: []byte("x")}); err != nil {
		t.Fatalf("handle similar prefix: %v", err)
	}
	if _, ok := w.Files()["/var/lib/excl_other/a"]; !ok {
		t.Fatalf("similar-prefix file was wrongly excluded")
	}
}

func TestEventTimestampsUTC(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, zone)
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.Handle(Event{Op: OpCreate, Path: "/w/f", At: at, Content: []byte("v1")}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	fs, ok := w.Files()["/w/f"]
	if !ok {
		t.Fatalf("file not recorded")
	}
	if !fs.UpdatedAt.Equal(at) {
		t.Fatalf("timestamp %v is not the same instant as %v", fs.UpdatedAt, at)
	}
	if fs.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamp location = %v, want UTC", fs.UpdatedAt.Location())
	}
}

func TestContainerDeleteReleasesWatches(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	for _, p := range []string{"/w/a", "/w/b"} {
		if err := w.Handle(Event{Op: OpCreate, Path: p, Container: "c1", At: time.Unix(1700000000, 0), Content: []byte("v")}); err != nil {
			t.Fatalf("handle %s: %v", p, err)
		}
	}
	if err := w.ReleaseContainer("c1"); err != nil {
		t.Fatalf("release container: %v", err)
	}
	if got := len(w.Files()); got != 0 {
		t.Fatalf("files = %d after container release, want 0", got)
	}
	if err := w.ReleaseContainer("c1"); err != nil {
		t.Fatalf("release unknown container: %v", err)
	}
}

func TestDeleteWithoutRenameUnknownReason(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.Handle(Event{Op: OpRemove, Path: "/w/ghost", At: time.Unix(1700000000, 0)}); err != nil {
		t.Fatalf("remove unknown path: %v", err)
	}
	fs, ok := w.Files()["/w/ghost"]
	if !ok {
		t.Fatalf("removal not recorded")
	}
	if !fs.Deleted {
		t.Fatalf("removed file not marked deleted")
	}
	if fs.Reason != "unknown" {
		t.Fatalf("reason = %q, want unknown", fs.Reason)
	}
}

func TestWatchCountNeverExceedsLimit(t *testing.T) {
	w := newWatcher(t, Options{MaxWatches: 3})
	failures := 0
	for i := 0; i < 10; i++ {
		dir := "/w/" + string(rune('a'+i))
		if err := w.AddDir(dir); err != nil {
			failures++
		}
	}
	if got := len(w.Watches()); got != 3 {
		t.Fatalf("watches = %d, want 3", got)
	}
	if failures != 7 {
		t.Fatalf("failures = %d, want 7", failures)
	}
}

func TestBaselineDetectsChange(t *testing.T) {
	w := newWatcher(t, Options{Roots: []string{"/w"}, MaxWatches: 8})
	if err := w.Handle(Event{Op: OpCreate, Path: "/w/f", At: time.Unix(1700000000, 0), Content: []byte("v1")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.RebuildBaseline(map[string]string{"/w/f": "v1"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if err := w.Handle(Event{Op: OpWrite, Path: "/w/f", At: time.Unix(1700000001, 0), Content: []byte("v2")}); err != nil {
		t.Fatalf("write: %v", err)
	}
	current, ok := w.HashOf("/w/f")
	if !ok || current != shaHex([]byte("v2")) {
		t.Fatalf("current hash = %q, want hash of v2", current)
	}
	baseline := w.Baseline()
	if len(baseline) != 1 || baseline["/w/f"] != shaHex([]byte("v1")) {
		t.Fatalf("baseline was silently rewritten: %v", baseline)
	}
	if baseline["/w/f"] == current {
		t.Fatalf("baseline change not detectable")
	}
}
