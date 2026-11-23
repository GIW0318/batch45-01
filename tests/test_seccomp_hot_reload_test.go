package task

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	task "gsb/batch45-safety-b-25"
)

func baseSet() task.RuleSet {
	return task.RuleSet{Rules: []task.Rule{{
		ID:       "allow-read",
		Allow:    true,
		Syscalls: []string{"read", "write"},
	}}}
}

func denyRead() task.RuleSet {
	return task.RuleSet{Rules: []task.Rule{{
		ID:       "deny-read",
		Allow:    false,
		Syscalls: []string{"read"},
	}}}
}

func TestCurrentNeverNilDuringConcurrentReload(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	var (
		wg  sync.WaitGroup
		bad atomic.Int64
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			snap := r.Current()
			if snap.Version == 0 || snap.Rules == nil {
				bad.Add(1)
			}
		}
	}()
	for i := 0; i < 20; i++ {
		if _, err := r.Reload(baseSet(), task.UpdateOptions{Actor: "test"}); err != nil {
			t.Fatalf("Reload %d returned unexpected error: %v", i, err)
		}
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("readers observed %d empty or uninitialised snapshots", bad.Load())
	}
}

func TestReloadIncrementsVersion(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	if got := r.Current().Version; got != 1 {
		t.Fatalf("initial snapshot must be version 1, got %d", got)
	}
	if _, err := r.Reload(denyRead(), task.UpdateOptions{Actor: "test"}); err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	if got := r.Current().Version; got != 2 {
		t.Fatalf("each accepted reload must bump the version, got %d", got)
	}
}

func TestFailedValidationKeepsOldSnapshot(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	invalid := task.RuleSet{Rules: []task.Rule{{ID: "", Allow: true, Syscalls: []string{"read"}}}}
	_, err := r.Reload(invalid, task.UpdateOptions{Actor: "test"})
	if !errors.Is(err, task.ErrValidation) {
		t.Fatalf("a rule without id must fail validation, got %v", err)
	}
	if got := r.Current().Version; got != 1 {
		t.Fatalf("a rejected reload must not change the version, got %d", got)
	}
}

func TestEmptyRuleSetRejected(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	_, err := r.Reload(task.RuleSet{}, task.UpdateOptions{Actor: "test"})
	if !errors.Is(err, task.ErrEmptyRuleSet) {
		t.Fatalf("an empty deny-by-default rule set must be rejected, got %v", err)
	}
}

func TestJournalRecordsEveryUpdate(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	if _, err := r.Reload(denyRead(), task.UpdateOptions{Actor: "alice"}); err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	if _, err := r.Reload(baseSet(), task.UpdateOptions{Actor: "bob"}); err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	entries := r.Journal()
	if len(entries) != 2 {
		t.Fatalf("every update must be journaled, got %d entries", len(entries))
	}
	if entries[0].FromVersion != 1 || entries[0].ToVersion != 2 || !entries[0].Applied {
		t.Fatalf("first entry must describe 1 to 2 as applied, got %+v", entries[0])
	}
	if entries[1].FromVersion != 2 || entries[1].ToVersion != 3 {
		t.Fatalf("second entry must describe 2 to 3, got %+v", entries[1])
	}
}

func TestNoFilterHandleLeak(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	for i := 0; i < 10; i++ {
		if _, err := r.Reload(baseSet(), task.UpdateOptions{Actor: "test"}); err != nil {
			t.Fatalf("Reload %d returned unexpected error: %v", i, err)
		}
	}
	if got := r.ActiveFilters(); got != 1 {
		t.Fatalf("hot reload must replace the filter instead of stacking handles, got %d", got)
	}
}

func TestWideningNeedsConfirmation(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	wider := task.RuleSet{Rules: baseSet().Rules, AllowAll: true}
	res, err := r.Reload(wider, task.UpdateOptions{Actor: "test"})
	if err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	if !res.NeedsConfirm || res.Applied {
		t.Fatalf("a widened rule set must wait for confirmation, got %+v", res)
	}
	if r.Current().AllowAll {
		t.Fatalf("widened rule set must not be installed before confirmation")
	}
	res, err = r.Reload(wider, task.UpdateOptions{Actor: "test", ConfirmWidening: true})
	if err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	if !res.Applied {
		t.Fatalf("a confirmed widening must be applied, got %+v", res)
	}
}

func TestMountDependentRuleNotReused(t *testing.T) {
	initial := task.RuleSet{Rules: []task.Rule{{
		ID:         "allow-data",
		Allow:      true,
		Syscalls:   []string{"open"},
		PathPrefix: "/data",
		MountID:    5,
	}}}
	r := task.NewSeccompReloader(initial)
	if err := r.SetMountTable(map[uint64]string{5: "/data"}); err != nil {
		t.Fatalf("SetMountTable returned unexpected error: %v", err)
	}
	dec, err := r.Evaluate("open", "/data/x")
	if err != nil {
		t.Fatalf("Evaluate returned unexpected error: %v", err)
	}
	if !dec.Allow {
		t.Fatalf("the rule applies while mount 5 still maps to /data, got %+v", dec)
	}
	if err := r.SetMountTable(map[uint64]string{5: "/hostdata"}); err != nil {
		t.Fatalf("SetMountTable returned unexpected error: %v", err)
	}
	dec, err = r.Evaluate("open", "/data/x")
	if err != nil {
		t.Fatalf("Evaluate returned unexpected error: %v", err)
	}
	if dec.Allow {
		t.Fatalf("a path rule must not survive a mount point change, got %+v", dec)
	}
}

func TestEvaluateUsesCurrentSnapshot(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	dec, err := r.Evaluate("read", "")
	if err != nil {
		t.Fatalf("Evaluate returned unexpected error: %v", err)
	}
	if !dec.Allow || dec.Version != 1 {
		t.Fatalf("initial snapshot allows read at version 1, got %+v", dec)
	}
	if _, err := r.Reload(denyRead(), task.UpdateOptions{Actor: "test"}); err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	dec, err = r.Evaluate("read", "")
	if err != nil {
		t.Fatalf("Evaluate returned unexpected error: %v", err)
	}
	if dec.Allow || dec.Version != 2 {
		t.Fatalf("a running syscall must see one coherent snapshot, got %+v", dec)
	}
}

func TestRollbackLeavesRulesUntouched(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	before := r.Current()
	if before.Version != 1 || len(before.Rules) != 1 {
		t.Fatalf("initial snapshot must be version 1 with one rule, got %+v", before)
	}
	invalid := task.RuleSet{Rules: []task.Rule{{ID: "", Allow: true, Syscalls: []string{"open"}}}}
	if _, err := r.Reload(invalid, task.UpdateOptions{Actor: "test"}); err == nil {
		t.Fatalf("invalid rule set must be rejected")
	}
	after := r.Current()
	if !reflect.DeepEqual(before.Rules, after.Rules) || before.Version != after.Version {
		t.Fatalf("rollback must restore the previous rules exactly, before=%+v after=%+v", before, after)
	}
}

func TestPersistRestoreKeepsLatestVersion(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	if _, err := r.Reload(denyRead(), task.UpdateOptions{Actor: "test"}); err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	data, err := r.Persist()
	if err != nil {
		t.Fatalf("Persist returned unexpected error: %v", err)
	}
	restarted := task.NewSeccompReloader(baseSet())
	if err := restarted.Restore(data); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	if got := restarted.Current().Version; got != 2 {
		t.Fatalf("restart must load the newest rule version, got %d", got)
	}
	dec, err := restarted.Evaluate("read", "")
	if err != nil {
		t.Fatalf("Evaluate returned unexpected error: %v", err)
	}
	if dec.Allow {
		t.Fatalf("restored rules must keep denying read, got %+v", dec)
	}
}

func TestConcurrentReloadsSerialized(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Reload(denyRead(), task.UpdateOptions{Actor: "test"}); err != nil {
				t.Errorf("Reload returned unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := r.Current().Version; got != 3 {
		t.Fatalf("two concurrent reloads must both take effect, got version %d", got)
	}
	if got := len(r.Journal()); got != 2 {
		t.Fatalf("both concurrent reloads must be journaled, got %d", got)
	}
}

func TestSnapshotNotAliasedToCallerSlice(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	next := task.RuleSet{Rules: []task.Rule{{ID: "allow-open", Allow: true, Syscalls: []string{"open"}}}}
	if _, err := r.Reload(next, task.UpdateOptions{Actor: "test"}); err != nil {
		t.Fatalf("Reload returned unexpected error: %v", err)
	}
	next.Rules[0].Allow = false
	snap := r.Current()
	if !snap.Rules[0].Allow {
		t.Fatalf("installed snapshot must not alias the caller slice")
	}
}

func TestDuplicateRuleIDRejected(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	dup := task.RuleSet{Rules: []task.Rule{
		{ID: "same", Allow: true, Syscalls: []string{"read"}},
		{ID: "same", Allow: false, Syscalls: []string{"write"}},
	}}
	_, err := r.Reload(dup, task.UpdateOptions{Actor: "test"})
	if !errors.Is(err, task.ErrDuplicateRule) {
		t.Fatalf("duplicate rule ids must be rejected, got %v", err)
	}
}

func TestEvaluateUnknownSyscallDeniedByDefault(t *testing.T) {
	r := task.NewSeccompReloader(baseSet())
	r.SetMountTable(map[uint64]string{})
	dec, err := r.Evaluate("ptrace", "")
	if err != nil {
		t.Fatalf("Evaluate returned unexpected error: %v", err)
	}
	if dec.Allow {
		t.Fatalf("a syscall without an allow rule must be denied by default, got %+v", dec)
	}
	if dec.Version != 1 {
		t.Fatalf("decision must carry the snapshot version, got %+v", dec)
	}
}
