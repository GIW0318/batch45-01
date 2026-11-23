package task

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-19"
)

func newPipeline(t *testing.T, o Options) *AuditPipeline {
	t.Helper()
	if o.Path == "" {
		o.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	}
	if o.QueueSize == 0 {
		o.QueueSize = 100
	}
	if o.FileMode == 0 {
		o.FileMode = 0o600
	}
	p, err := NewAuditPipeline(o)
	if err != nil {
		t.Fatalf("NewAuditPipeline: %v", err)
	}
	return p
}

func auditEvent(id, actor, action string, at time.Time) Event {
	return Event{ID: id, Actor: actor, Action: action, At: at, Fields: map[string]string{}}
}

func TestQueueFullReturnsErrorNotSilent(t *testing.T) {
	p := newPipeline(t, Options{QueueSize: 1})
	now := time.Unix(1700000000, 0)
	if err := p.Append(auditEvent("e1", "alice", "login", now)); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := p.Append(auditEvent("e2", "alice", "login", now)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("second append error = %v, want ErrQueueFull", err)
	}
	if err := p.Append(auditEvent("e3", "alice", "login", now)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third append error = %v, want ErrQueueFull", err)
	}
	if got := p.Dropped(); got != 0 {
		t.Fatalf("dropped = %d, want 0 (events must not be dropped silently)", got)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 1 || events[0].ID != "e1" {
		t.Fatalf("stored events = %+v, want only e1", events)
	}
}

func TestConcurrentAppendNoInterleavedLines(t *testing.T) {
	p := newPipeline(t, Options{QueueSize: 100})
	now := time.Unix(1700000000, 0)
	var wg sync.WaitGroup
	errCh := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := auditEvent("id-"+strconv.Itoa(i), "alice", "write", now)
			ev.Fields["index"] = strconv.Itoa(i)
			errCh <- p.Append(ev)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all (interleaved lines?): %v", err)
	}
	if len(events) != 50 {
		t.Fatalf("stored events = %d, want 50", len(events))
	}
	seen := map[string]bool{}
	for _, ev := range events {
		seen[ev.ID] = true
	}
	for i := 0; i < 50; i++ {
		if !seen["id-"+strconv.Itoa(i)] {
			t.Fatalf("event id-%d lost", i)
		}
	}
}

func TestSequenceContinuesAcrossRestart(t *testing.T) {
	now := time.Unix(1700000000, 0)
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	first := newPipeline(t, Options{Path: path})
	for i := 0; i < 3; i++ {
		if err := first.Append(auditEvent("e"+strconv.Itoa(i), "alice", "op", now)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	events, err := first.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	second := newPipeline(t, Options{Path: path})
	if err := second.Restore(events); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := second.Seq(); got != 3 {
		t.Fatalf("seq after restore = %d, want 3", got)
	}
	if err := second.Append(auditEvent("e-new", "bob", "op", now)); err != nil {
		t.Fatalf("append after restart: %v", err)
	}
	rest, err := second.ReadAll()
	if err != nil {
		t.Fatalf("read all after restart: %v", err)
	}
	if len(rest) != 1 || rest[0].Seq != 4 {
		t.Fatalf("event after restart = %+v, want seq 4", rest)
	}
}

func TestSensitiveFieldsRedacted(t *testing.T) {
	p := newPipeline(t, Options{})
	now := time.Unix(1700000000, 0)
	ev := auditEvent("e1", "alice", "login", now)
	ev.Fields = map[string]string{
		"password":      "hunter2",
		"token":         "abc123",
		"authorization": "Bearer xyz",
		"user":          "alice",
	}
	if err := p.Append(ev); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	for _, key := range []string{"password", "token", "authorization"} {
		if got := events[0].Fields[key]; got != "***" {
			t.Fatalf("field %q = %q, want ***", key, got)
		}
	}
	if got := events[0].Fields["user"]; got != "alice" {
		t.Fatalf("non-sensitive field = %q, want alice", got)
	}
	if got := p.Redact("token=abc&user=bob"); got != "token=***&user=bob" {
		t.Fatalf("Redact = %q, want token=***&user=bob", got)
	}
}

func TestRotationReopensNewInode(t *testing.T) {
	p := newPipeline(t, Options{})
	now := time.Unix(1700000000, 0)
	path := p.FilePath()
	if path == "" {
		t.Fatalf("empty file path")
	}
	if err := p.Append(auditEvent("before", "alice", "op", now)); err != nil {
		t.Fatalf("append before rotate: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := p.Rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := p.Append(auditEvent("after", "alice", "op", now)); err != nil {
		t.Fatalf("append after rotate: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush after rotate: %v", err)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read rotated file: %v", err)
	}
	if !strings.Contains(string(rotated), "before") {
		t.Fatalf("rotated file lost the pre-rotation event: %s", rotated)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current file: %v", err)
	}
	if !strings.Contains(string(current), "after") {
		t.Fatalf("post-rotation event not written: %s", current)
	}
	if strings.Contains(string(current), "before") {
		t.Fatalf("post-rotation event went to the old inode")
	}
}

func TestAuditOnlyWrittenOnCommit(t *testing.T) {
	p := newPipeline(t, Options{})
	now := time.Unix(1700000000, 0)
	tx, err := p.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := p.AppendTx(tx, auditEvent("rolled-back", "alice", "op", now)); err != nil {
		t.Fatalf("append tx: %v", err)
	}
	if err := p.Rollback(tx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all after rollback: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events after rollback = %d, want 0", len(events))
	}
	tx2, err := p.Begin()
	if err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	if err := p.AppendTx(tx2, auditEvent("committed", "alice", "op", now)); err != nil {
		t.Fatalf("append tx 2: %v", err)
	}
	if err := p.Commit(tx2); err != nil {
		t.Fatalf("commit: %v", err)
	}
	events, err = p.ReadAll()
	if err != nil {
		t.Fatalf("read all after commit: %v", err)
	}
	if len(events) != 1 || events[0].ID != "committed" || events[0].TxID != tx2.ID {
		t.Fatalf("committed event = %+v, want committed event with tx id", events)
	}
	if err := p.AppendTx(tx2, auditEvent("late", "alice", "op", now)); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("append to closed tx error = %v, want ErrTxClosed", err)
	}
}

func TestSyslogOfflineBuffersLocally(t *testing.T) {
	p := newPipeline(t, Options{SyslogAddr: "tcp://syslog.invalid:514"})
	now := time.Unix(1700000000, 0)
	p.SetSyslogUp(false)
	for i := 0; i < 2; i++ {
		if err := p.Append(auditEvent("e"+strconv.Itoa(i), "alice", "op", now)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush while offline: %v", err)
	}
	if got := p.SyslogBacklog(); got != 2 {
		t.Fatalf("syslog backlog = %d, want 2", got)
	}
	p.SetSyslogUp(true)
	if err := p.Flush(); err != nil {
		t.Fatalf("flush while online: %v", err)
	}
	if got := p.SyslogBacklog(); got != 0 {
		t.Fatalf("syslog backlog = %d after reconnect, want 0", got)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("stored events = %d, want 2", len(events))
	}
}

func TestDuplicateEventSuppressed(t *testing.T) {
	p := newPipeline(t, Options{})
	now := time.Unix(1700000000, 0)
	ev := auditEvent("same-id", "alice", "op", now)
	if err := p.Append(ev); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := p.Append(ev); err != nil {
		t.Fatalf("duplicate append: %v", err)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (duplicate suppressed)", len(events))
	}
	if got := p.Dropped(); got != 0 {
		t.Fatalf("dropped = %d, want 0", got)
	}
}

func TestTimestampsStoredUTC(t *testing.T) {
	p := newPipeline(t, Options{})
	zone := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, zone)
	if err := p.Append(auditEvent("e1", "alice", "op", at)); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if !events[0].At.Equal(at) {
		t.Fatalf("stored time %v is not the same instant as %v", events[0].At, at)
	}
	if events[0].At.Location() != time.UTC {
		t.Fatalf("stored time location = %v, want UTC", events[0].At.Location())
	}
}

func TestFileModeRestricted(t *testing.T) {
	p := newPipeline(t, Options{FileMode: 0o600})
	now := time.Unix(1700000000, 0)
	if err := p.Append(auditEvent("e1", "alice", "op", now)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := p.FileMode() & 0o777; got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
	info, err := os.Stat(p.FilePath())
	if err != nil {
		t.Fatalf("stat audit file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("on-disk permission = %o, want 600", got)
	}
}

func TestDiskFullReturnsError(t *testing.T) {
	p := newPipeline(t, Options{MaxBytes: 2048})
	now := time.Unix(1700000000, 0)
	if err := p.Append(auditEvent("small", "alice", "op", now)); err != nil {
		t.Fatalf("append small: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	big := auditEvent("big", "alice", "op", now)
	big.Fields["blob"] = strings.Repeat("x", 4096)
	if err := p.Append(big); !errors.Is(err, ErrDiskFull) {
		t.Fatalf("append beyond MaxBytes error = %v, want ErrDiskFull", err)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 1 || events[0].ID != "small" {
		t.Fatalf("stored events = %+v, want only the small event", events)
	}
}

func TestQueryReturnsRedacted(t *testing.T) {
	p := newPipeline(t, Options{})
	now := time.Unix(1700000000, 0)
	ev := auditEvent("e1", "alice", "login", now)
	ev.Fields = map[string]string{"token": "secret-value", "user": "alice"}
	if err := p.Append(ev); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := p.Query(Filter{Actor: "alice"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("query returned %d events, want 1", len(events))
	}
	if got := events[0].Fields["token"]; got != "***" {
		t.Fatalf("query returned unredacted token: %q", got)
	}
	if got := events[0].Fields["user"]; got != "alice" {
		t.Fatalf("query lost the non-sensitive field: %q", got)
	}
}

func TestSeqNotResetByRotation(t *testing.T) {
	p := newPipeline(t, Options{})
	now := time.Unix(1700000000, 0)
	for i := 0; i < 2; i++ {
		if err := p.Append(auditEvent("e"+strconv.Itoa(i), "alice", "op", now)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := p.Rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := p.Append(auditEvent("e-after", "alice", "op", now)); err != nil {
		t.Fatalf("append after rotate: %v", err)
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 3 {
		t.Fatalf("event after rotation = %+v, want seq 3", events)
	}
	if got := p.Seq(); got != 3 {
		t.Fatalf("seq = %d after rotation, want 3", got)
	}
}

func TestQueryOrderingAndFilter(t *testing.T) {
	p := newPipeline(t, Options{})
	base := time.Unix(1700000000, 0)
	evs := []Event{
		auditEvent("a1", "alice", "login", base),
		auditEvent("b1", "bob", "login", base.Add(time.Minute)),
		auditEvent("a2", "alice", "delete", base.Add(2*time.Minute)),
	}
	for _, ev := range evs {
		if err := p.Append(ev); err != nil {
			t.Fatalf("append %s: %v", ev.ID, err)
		}
	}
	alice, err := p.Query(Filter{Actor: "alice"})
	if err != nil {
		t.Fatalf("query actor: %v", err)
	}
	if len(alice) != 2 || alice[0].Seq >= alice[1].Seq {
		t.Fatalf("alice events = %+v, want 2 ordered events", alice)
	}
	window, err := p.Query(Filter{Since: base.Add(30 * time.Second), Until: base.Add(90 * time.Second)})
	if err != nil {
		t.Fatalf("query window: %v", err)
	}
	if len(window) != 1 || window[0].ID != "b1" {
		t.Fatalf("window events = %+v, want only b1", window)
	}
	all, err := p.Query(Filter{})
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all events = %d, want 3", len(all))
	}
}

func TestPerGoroutineOrderPreserved(t *testing.T) {
	p := newPipeline(t, Options{})
	now := time.Unix(1700000000, 0)
	for i := 0; i < 100; i++ {
		if err := p.Append(auditEvent("id-"+strconv.Itoa(i), "alice", "op", now)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	events, err := p.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(events) != 100 {
		t.Fatalf("events = %d, want 100", len(events))
	}
	for i, ev := range events {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("event %d has seq %d, want %d", i, ev.Seq, i+1)
		}
	}
}
