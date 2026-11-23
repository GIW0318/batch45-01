package task

import (
	"errors"
	"strings"
	"testing"
	"time"

	. "gsb/batch45-safety-b-05"
)

func newReconciler() *NftReconciler {
	return NewNftReconciler(ReconcilerOptions{Family: FamilyIPv4, Table: "filter", MaxBatch: 100})
}

func seed(t *testing.T, r *NftReconciler) {
	t.Helper()
	if err := r.DeclareChain("input"); err != nil {
		t.Fatalf("DeclareChain returned error: %v", err)
	}
	if err := r.DeclareSet(Set{Name: "allow", Family: FamilyIPv4, Timeout: 30 * time.Second}); err != nil {
		t.Fatalf("DeclareSet returned error: %v", err)
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "tcp dport 22 accept", Comment: "ssh"}); err != nil {
		t.Fatalf("AddRule returned error: %v", err)
	}
}

func mustRender(t *testing.T, r *NftReconciler) string {
	t.Helper()
	out, err := r.Render()
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("Render returned an empty table")
	}
	return out
}

func opIndex(b *Batch, kind string) int {
	for i, op := range b.Ops {
		if op.Kind == kind {
			return i
		}
	}
	return -1
}

func TestRenderIsDeterministic(t *testing.T) {
	a := newReconciler()
	b := newReconciler()
	for _, r := range []*NftReconciler{a, b} {
		if err := r.DeclareChain("input"); err != nil {
			t.Fatalf("DeclareChain returned error: %v", err)
		}
	}
	rules := []Rule{
		{Chain: "input", Expr: "tcp dport 22 accept", Comment: "ssh"},
		{Chain: "input", Expr: "tcp dport 443 accept", Comment: "https"},
		{Chain: "input", Expr: "udp dport 53 accept", Comment: "dns"},
	}
	for _, rule := range rules {
		if err := a.AddRule(rule); err != nil {
			t.Fatalf("AddRule returned error: %v", err)
		}
	}
	for i := len(rules) - 1; i >= 0; i-- {
		if err := b.AddRule(rules[i]); err != nil {
			t.Fatalf("AddRule returned error: %v", err)
		}
	}
	first := mustRender(t, a)
	second := mustRender(t, a)
	if first != second {
		t.Fatalf("Render is not stable across calls")
	}
	if first != mustRender(t, b) {
		t.Fatalf("Render depends on insertion order, rule order must be sorted deterministically")
	}
}

func TestRenderBatchIsSingleTransaction(t *testing.T) {
	r := newReconciler()
	seed(t, r)
	batch, err := r.RenderBatch()
	if err != nil {
		t.Fatalf("RenderBatch returned error: %v", err)
	}
	if len(batch.Ops) < 3 {
		t.Fatalf("batch has %d operations, want a full table install", len(batch.Ops))
	}
	if batch.Ops[0].Kind != "begin" || batch.Ops[len(batch.Ops)-1].Kind != "commit" {
		t.Fatalf("batch is not wrapped in begin/commit: %q .. %q", batch.Ops[0].Kind, batch.Ops[len(batch.Ops)-1].Kind)
	}
	table := opIndex(batch, "add_table")
	rule := opIndex(batch, "add_rule")
	if table < 0 || rule < 0 {
		t.Fatalf("batch lacks add_table or add_rule operations")
	}
	if table > rule {
		t.Fatalf("rules are added before the table exists")
	}
	if opIndex(batch, "delete_table") >= 0 {
		t.Fatalf("batch deletes the table before adding rules, leaving a window with no rules")
	}
}

func TestElementBatchIsIncremental(t *testing.T) {
	r := newReconciler()
	seed(t, r)
	batch, err := r.ElementBatch("allow", []string{"10.0.0.0/8"}, []string{"192.168.0.0/16"})
	if err != nil {
		t.Fatalf("ElementBatch returned error: %v", err)
	}
	allowed := map[string]bool{"begin": true, "commit": true, "add_element": true, "delete_element": true}
	payloads := ""
	for _, op := range batch.Ops {
		if !allowed[op.Kind] {
			t.Fatalf("element update produced %q, sets must be updated incrementally", op.Kind)
		}
		payloads += op.Payload + "\n"
	}
	if !strings.Contains(payloads, "10.0.0.0/8") || !strings.Contains(payloads, "192.168.0.0/16") {
		t.Fatalf("element batch does not contain both add and delete payloads: %q", payloads)
	}
}

func TestCommentEscaped(t *testing.T) {
	r := newReconciler()
	if err := r.DeclareChain("input"); err != nil {
		t.Fatalf("DeclareChain returned error: %v", err)
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "tcp dport 22 accept", Comment: `ssh" ; flush ruleset`}); err != nil {
		t.Fatalf("AddRule returned error: %v", err)
	}
	out := mustRender(t, r)
	if !strings.Contains(out, `\"`) {
		t.Fatalf("comment quote is not escaped, special characters truncate the rule: %q", out)
	}
	if strings.Contains(out, `ssh" ; flush`) {
		t.Fatalf("unescaped comment leaked into the rendered table")
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "tcp dport 80 accept", Comment: "line\nbreak"}); err == nil {
		t.Fatalf("comment with a newline must be rejected")
	}
}

func TestTimeoutUnitIsMilliseconds(t *testing.T) {
	r := newReconciler()
	if err := r.DeclareSet(Set{Name: "allow", Family: FamilyIPv4, Timeout: 90 * time.Second}); err != nil {
		t.Fatalf("DeclareSet returned error: %v", err)
	}
	out := mustRender(t, r)
	if !strings.Contains(out, "90000ms") {
		t.Fatalf("set timeout not rendered in milliseconds: %q", out)
	}
	if strings.Contains(out, "timeout 90s") || strings.Contains(out, "timeout 90000s") {
		t.Fatalf("set timeout mixes seconds and milliseconds")
	}
}

func TestCountersPreservedOnRerender(t *testing.T) {
	r := newReconciler()
	seed(t, r)
	if err := r.SetCounter("ssh", 100); err != nil {
		t.Fatalf("SetCounter returned error: %v", err)
	}
	mustRender(t, r)
	if got := r.Counters()["ssh"]; got != 100 {
		t.Fatalf("counter after render = %d, want 100", got)
	}
	mustRender(t, r)
	if got := r.Counters()["ssh"]; got != 100 {
		t.Fatalf("counter after re-render = %d, want 100 (re-apply must not reset counters)", got)
	}
}

func TestChainNamesQualifiedByTable(t *testing.T) {
	a := NewNftReconciler(ReconcilerOptions{Family: FamilyIPv4, Table: "alpha", MaxBatch: 10})
	b := NewNftReconciler(ReconcilerOptions{Family: FamilyIPv4, Table: "beta", MaxBatch: 10})
	for _, r := range []*NftReconciler{a, b} {
		if err := r.DeclareChain("input"); err != nil {
			t.Fatalf("DeclareChain returned error: %v", err)
		}
	}
	outA := mustRender(t, a)
	outB := mustRender(t, b)
	if !strings.Contains(outA, "table ip alpha") {
		t.Fatalf("render does not qualify the chain with its table: %q", outA)
	}
	if !strings.Contains(outB, "table ip beta") {
		t.Fatalf("render does not qualify the chain with its table: %q", outB)
	}
	if outA == outB {
		t.Fatalf("tables alpha and beta render identically, chain names collide")
	}
}

func TestMissingSetReferenceRejected(t *testing.T) {
	r := newReconciler()
	if err := r.DeclareChain("input"); err != nil {
		t.Fatalf("DeclareChain returned error: %v", err)
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "ip saddr @ghost accept", SetRefs: []string{"ghost"}}); err == nil {
		t.Fatalf("rule referencing an undeclared set must be rejected")
	}
	if err := r.DeclareSet(Set{Name: "allow", Family: FamilyIPv4}); err != nil {
		t.Fatalf("DeclareSet returned error: %v", err)
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "ip saddr @allow accept", SetRefs: []string{"allow"}}); err != nil {
		t.Fatalf("rule referencing a declared set must be accepted: %v", err)
	}
}

func TestApplyRollsBackOnFailure(t *testing.T) {
	r := newReconciler()
	seed(t, r)
	before, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	batch, err := r.RenderBatch()
	if err != nil {
		t.Fatalf("RenderBatch returned error: %v", err)
	}
	calls := 0
	err = r.Apply(batch, func(*Batch) error {
		calls++
		if calls == 2 {
			return errors.New("kernel rejected batch")
		}
		return nil
	})
	if err == nil {
		t.Fatalf("Apply must report the executor failure")
	}
	after, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if after != before {
		t.Fatalf("state changed after a failed batch, rollback is missing")
	}
}

func TestIPv6RuleRejectedInIPv4Table(t *testing.T) {
	r := newReconciler()
	if err := r.DeclareChain("input"); err != nil {
		t.Fatalf("DeclareChain returned error: %v", err)
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "tcp dport 22 accept", Family: FamilyIPv6}); err == nil {
		t.Fatalf("ipv6 rule must be rejected in an ipv4 table")
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "tcp dport 22 accept", Family: FamilyIPv4}); err != nil {
		t.Fatalf("ipv4 rule must be accepted: %v", err)
	}
}

func TestWholesaleICMPRejected(t *testing.T) {
	r := newReconciler()
	if err := r.DeclareChain("input"); err != nil {
		t.Fatalf("DeclareChain returned error: %v", err)
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "icmp accept"}); err == nil {
		t.Fatalf("blanket icmp accept must be rejected")
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "icmp type echo-request accept"}); err != nil {
		t.Fatalf("typed icmp rule must be accepted: %v", err)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	r := newReconciler()
	seed(t, r)
	out := mustRender(t, r)
	restored := newReconciler()
	if err := restored.Restore(out); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	again := mustRender(t, restored)
	if again != out {
		t.Fatalf("restored table differs from the snapshot")
	}
	if !strings.Contains(again, "chain input") {
		t.Fatalf("restored table has no rules, a restart must not start from an empty table")
	}
}

func TestRuleRequiresDeclaredChain(t *testing.T) {
	r := newReconciler()
	if err := r.AddRule(Rule{Chain: "input", Expr: "tcp dport 22 accept"}); err == nil {
		t.Fatalf("rule added to an undeclared chain must be rejected")
	}
	if err := r.DeclareChain("input"); err != nil {
		t.Fatalf("DeclareChain returned error: %v", err)
	}
	if err := r.AddRule(Rule{Chain: "input", Expr: "tcp dport 22 accept"}); err != nil {
		t.Fatalf("rule added to a declared chain must be accepted: %v", err)
	}
}

func TestMaxBatchEnforced(t *testing.T) {
	small := NewNftReconciler(ReconcilerOptions{Family: FamilyIPv4, Table: "small", MaxBatch: 2})
	if err := small.DeclareChain("input"); err != nil {
		t.Fatalf("DeclareChain returned error: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := small.AddRule(Rule{Chain: "input", Expr: "tcp dport 22 accept"}); err != nil {
			t.Fatalf("AddRule returned error: %v", err)
		}
	}
	if _, err := small.RenderBatch(); err == nil {
		t.Fatalf("batch exceeding MaxBatch must be rejected")
	}
}

func TestSetElementValidation(t *testing.T) {
	r := newReconciler()
	seed(t, r)
	if _, err := r.ElementBatch("allow", []string{"10.0.0.0/8"}, nil); err != nil {
		t.Fatalf("valid element rejected: %v", err)
	}
	if _, err := r.ElementBatch("allow", []string{"10.0.0.0/8 unexpected"}, nil); err == nil {
		t.Fatalf("element with whitespace must be rejected")
	}
	if _, err := r.ElementBatch("missing", []string{"10.0.0.0/8"}, nil); err == nil {
		t.Fatalf("element batch for an undeclared set must be rejected")
	}
}
