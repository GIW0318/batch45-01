package task

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	. "gsb/batch45-safety-b-02"
)

type recordingExecutor struct {
	mu     sync.Mutex
	steps  []Step
	failOn string
}

func (r *recordingExecutor) Exec(s Step) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, s)
	if r.failOn != "" && s.Op == r.failOn {
		return errors.New("exec failed: " + s.Op)
	}
	return nil
}

func (r *recordingExecutor) count(op string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.steps {
		if s.Op == op {
			n++
		}
	}
	return n
}

type countingExecutor struct {
	mu        sync.Mutex
	restricts int
}

func (c *countingExecutor) Exec(s Step) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.Op == "restrict_self" {
		c.restricts++
	}
	return nil
}

func mustBuild(t *testing.T, b *RulesetBuilder) *Ruleset {
	t.Helper()
	rs, err := b.Build(3, BuildOptions{RequiredABI: 3, NoNewPrivs: true})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	return rs
}

func mustPlan(t *testing.T, b *RulesetBuilder, rs *Ruleset) []Step {
	t.Helper()
	steps, err := b.Plan(rs)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	return steps
}

func stepIndex(steps []Step, op string) int {
	for i, s := range steps {
		if s.Op == op {
			return i
		}
	}
	return -1
}

func TestPathMatchIsComponentWise(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	if !b.Match("/data", AccessRead) {
		t.Fatalf("rule path /data must match itself")
	}
	if b.Match("/database/secret.txt", AccessRead) {
		t.Fatalf("/data must not grant access to /database (string prefix match)")
	}
	if !b.Match("/data/sub/file.txt", AccessRead) {
		t.Fatalf("/data must grant access below the directory")
	}
}

func TestNestedPathInheritsRule(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/srv/app", AccessRead|AccessExecute); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	if !b.Match("/srv/app/bin/run", AccessRead|AccessExecute) {
		t.Errorf("nested file must inherit read and execute")
	}
	if b.Match("/srv/app/bin/run", AccessWrite) {
		t.Errorf("write was never granted and must not match")
	}
}

func TestAccessMergedPerPath(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	if err := b.AddPathRule("/data", AccessWrite); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	if got := b.HandledAccess(); got&AccessRead == 0 || got&AccessWrite == 0 {
		t.Fatalf("HandledAccess = %#x, want both read and write", got)
	}
	steps := mustPlan(t, b, rs)
	n, access := 0, Access(0)
	for _, s := range steps {
		if s.Op == "add_path_rule" && s.Path == "/data" {
			n++
			access |= s.Access
		}
	}
	if n != 1 {
		t.Fatalf("got %d path rule steps for /data, want a single merged rule", n)
	}
	if access != AccessRead|AccessWrite {
		t.Fatalf("merged access = %#x, want %#x", access, AccessRead|AccessWrite)
	}
}

func TestHandledAccessIsUnion(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/a", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	if err := b.AddPathRule("/b", AccessWrite); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	if got := b.HandledAccess(); got != AccessRead|AccessWrite {
		t.Fatalf("HandledAccess = %#x, want %#x (union of all rules)", got, AccessRead|AccessWrite)
	}
}

func TestABIBelowRequiredRejected(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	if _, err := b.Build(3, BuildOptions{RequiredABI: 3}); err != nil {
		t.Fatalf("ABI equal to the requirement must build: %v", err)
	}
	if _, err := b.Build(1, BuildOptions{RequiredABI: 3}); err == nil {
		t.Fatalf("ABI 1 must be rejected when ABI 3 is required")
	}
}

func TestPlanNoNewPrivsBeforeRestrict(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	steps := mustPlan(t, b, rs)
	nnp := stepIndex(steps, "no_new_privs")
	restrict := stepIndex(steps, "restrict_self")
	if nnp < 0 {
		t.Fatalf("plan has no no_new_privs step: %+v", steps)
	}
	if restrict < 0 {
		t.Fatalf("plan has no restrict_self step: %+v", steps)
	}
	if nnp > restrict {
		t.Fatalf("no_new_privs is applied after restrict_self")
	}
}

func TestApplyRequiresNoNewPrivs(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	ok, err := b.Build(3, BuildOptions{RequiredABI: 3, NoNewPrivs: true})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if err := b.Apply(ok, &recordingExecutor{}); err != nil {
		t.Fatalf("Apply returned error with no_new_privs set: %v", err)
	}
	noPrivs := &Ruleset{ABI: 3, PathRules: []PathRule{{Path: "/data", Access: AccessRead}}, Handled: AccessRead}
	if err := b.Apply(noPrivs, &recordingExecutor{}); err == nil {
		t.Fatalf("Apply must refuse a ruleset without no_new_privs")
	}
}

func TestNetRulesKeyedByPortAndProtocol(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddNetRule(8080, ProtoTCP, AccessRead); err != nil {
		t.Fatalf("AddNetRule returned error: %v", err)
	}
	if err := b.AddNetRule(9090, ProtoTCP, AccessRead); err != nil {
		t.Fatalf("AddNetRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	steps := mustPlan(t, b, rs)
	ports := map[uint16]bool{}
	for _, s := range steps {
		if s.Op == "add_net_rule" {
			ports[s.Port] = true
		}
	}
	if len(ports) != 2 || !ports[8080] || !ports[9090] {
		t.Fatalf("net rule steps keyed by %v, want both 8080 and 9090", ports)
	}
	if err := b.AddNetRule(0, ProtoTCP, AccessRead); err == nil {
		t.Fatalf("port 0 must be rejected")
	}
}

func TestSymlinkResolvedToRealPath(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "realdata")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	want, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	b := NewRulesetBuilder()
	if err := b.AddPathRule(link, AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	steps := mustPlan(t, b, rs)
	for _, s := range steps {
		if s.Op == "add_path_rule" {
			if s.Path != want {
				t.Fatalf("path rule recorded as %q, want resolved real path %q", s.Path, want)
			}
			return
		}
	}
	t.Fatalf("plan contains no add_path_rule step")
}

func TestDuplicatePathRulesDeduped(t *testing.T) {
	b := NewRulesetBuilder()
	for i := 0; i < 3; i++ {
		if err := b.AddPathRule("/data", AccessRead); err != nil {
			t.Fatalf("AddPathRule returned error: %v", err)
		}
	}
	rs := mustBuild(t, b)
	steps := mustPlan(t, b, rs)
	n := 0
	for _, s := range steps {
		if s.Op == "add_path_rule" && s.Path == "/data" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("duplicate rules produced %d steps, want 1", n)
	}
}

func TestConcurrentApplyRestrictsEveryCaller(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	ex := &countingExecutor{}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Apply(rs, ex); err != nil {
				t.Errorf("concurrent Apply returned error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ex.restricts != 5 {
		t.Fatalf("restrict_self ran %d times for 5 concurrent applies; every calling thread must be restricted", ex.restricts)
	}
}

func TestMountTableChangeRecomputesPlan(t *testing.T) {
	b := NewRulesetBuilder()
	b.SetMountTable(map[string]string{"/data": "/host/data"})
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	first := mustPlan(t, b, rs)
	if stepIndex(first, "add_path_rule") < 0 || first[stepIndex(first, "add_path_rule")].Path != "/host/data" {
		t.Fatalf("plan does not translate /data to /host/data: %+v", first)
	}
	b.SetMountTable(map[string]string{"/data": "/host/data2"})
	second := mustPlan(t, b, rs)
	found := false
	for _, s := range second {
		if s.Op == "add_path_rule" && s.Path == "/host/data2" {
			found = true
		}
		if s.Op == "add_path_rule" && s.Path == "/host/data" {
			t.Fatalf("stale mount translation /host/data still present after mount table change")
		}
	}
	if !found {
		t.Fatalf("plan was not recomputed after the mount point changed: %+v", second)
	}
}

func TestApplyFailureFailsClosed(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	if err := b.Apply(rs, &recordingExecutor{}); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if !b.Sandboxed() {
		t.Fatalf("Sandboxed() must be true after a successful apply")
	}
	if err := b.Apply(rs, &recordingExecutor{failOn: "restrict_self"}); err == nil {
		t.Fatalf("Apply must propagate executor failures")
	}
	if b.Sandboxed() {
		t.Fatalf("Sandboxed() must be false when applying the ruleset failed")
	}
}

func TestPlanRestrictsBeforeClosingRuleset(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("/data", AccessRead); err != nil {
		t.Fatalf("AddPathRule returned error: %v", err)
	}
	rs := mustBuild(t, b)
	steps := mustPlan(t, b, rs)
	if len(steps) == 0 {
		t.Fatalf("plan is empty")
	}
	last := len(steps) - 1
	if steps[last].Op != "close" {
		t.Fatalf("last plan step is %q, want close of the ruleset fd", steps[last].Op)
	}
	restrict := stepIndex(steps, "restrict_self")
	if restrict < 0 || restrict > last {
		t.Fatalf("restrict_self must run before the ruleset fd is closed")
	}
}

func TestRelativePathRuleRejected(t *testing.T) {
	b := NewRulesetBuilder()
	if err := b.AddPathRule("data/logs", AccessRead); err == nil {
		t.Fatalf("relative path rule must be rejected")
	}
	if err := b.AddPathRule("/data/logs", AccessRead); err != nil {
		t.Fatalf("absolute path rule must be accepted: %v", err)
	}
}
