package task

import (
	"errors"
	"strconv"
	"testing"

	. "gsb/batch45-safety-b-01"
)

func basePolicy() Policy {
	return Policy{
		Archs: []Arch{ArchX86_64},
		Rules: []Rule{{Syscall: "read", Action: ActAllow}},
	}
}

func TestDefaultActionUnsetBecomesKill(t *testing.T) {
	c := NewPolicyCompiler()
	prog, err := c.Compile(Policy{Rules: []Rule{{Syscall: "read", Action: ActAllow}}})
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	if prog.DefaultAction != ActKill {
		t.Fatalf("DefaultAction = %d, want ActKill (%d) when the policy leaves it unset", prog.DefaultAction, ActKill)
	}
}

func TestJumpOffsetsUseInstructionCounts(t *testing.T) {
	c := NewPolicyCompiler()
	rules := make([]Rule, 0, 300)
	for i := 0; i < 300; i++ {
		rules = append(rules, Rule{Syscall: "sys" + strconv.Itoa(i), Action: ActAllow})
	}
	prog, err := c.Compile(Policy{Archs: []Arch{ArchX86_64}, Rules: rules})
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	if len(prog.Instructions) < len(rules) {
		t.Fatalf("program has %d instructions for %d rules", len(prog.Instructions), len(rules))
	}
	for i, in := range prog.Instructions {
		for _, off := range []int{in.Jt, in.Jf} {
			if off < 0 {
				t.Fatalf("instruction %d has negative jump offset %d", i, off)
			}
			if i+1+off > len(prog.Instructions) {
				t.Fatalf("instruction %d jump offset %d lands at %d beyond program length %d: offsets look byte based",
					i, off, i+1+off, len(prog.Instructions))
			}
		}
	}
}

func TestSixtyFourBitArgSplitHighLow(t *testing.T) {
	c := NewPolicyCompiler()
	instrs, err := c.CompileRule(Rule{
		Syscall: "openat",
		Action:  ActAllow,
		Args:    []ArgCondition{{Index: 0, Op: "eq", Value: 0x0000000200000001}},
	})
	if err != nil {
		t.Fatalf("CompileRule returned error: %v", err)
	}
	var lowOK, highOK bool
	for _, in := range instrs {
		if in.ArgIndex != 0 {
			continue
		}
		if in.Mask == 0xFFFFFFFF && in.K == 0x1 {
			lowOK = true
		}
		if in.Mask == 0xFFFFFFFF00000000 && in.K == 0x0000000200000000 {
			highOK = true
		}
	}
	if !lowOK {
		t.Errorf("no low 32 bit comparison (mask 0xffffffff, k 0x1) for a 64 bit argument")
	}
	if !highOK {
		t.Errorf("no high 32 bit comparison (mask 0xffffffff00000000, k 0x200000000) for a 64 bit argument")
	}
}

func TestRulePriorityOrdering(t *testing.T) {
	c := NewPolicyCompiler()
	rules, err := c.OrderedRules(Policy{Rules: []Rule{
		{Syscall: "openat", Priority: 10, Action: ActAllow},
		{Syscall: "openat", Priority: 100, Action: ActErrno, Errno: 1},
		{Syscall: "read", Priority: 5, Action: ActAllow},
	}})
	if err != nil {
		t.Fatalf("OrderedRules returned error: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("OrderedRules returned %d rules, want 3", len(rules))
	}
	high, low := -1, -1
	for i, r := range rules {
		if r.Syscall == "openat" && r.Priority == 100 {
			high = i
		}
		if r.Syscall == "openat" && r.Priority == 10 {
			low = i
		}
	}
	if high == -1 || low == -1 {
		t.Fatalf("rules for openat were dropped: %+v", rules)
	}
	if high > low {
		t.Fatalf("priority 10 rule is evaluated before priority 100 rule, lower priority must not win")
	}
	seen := map[string]bool{}
	prev := ""
	for _, r := range rules {
		if r.Syscall != prev {
			if seen[r.Syscall] {
				t.Fatalf("rules for syscall %s are not grouped together", r.Syscall)
			}
			seen[r.Syscall] = true
			prev = r.Syscall
		}
	}
}

func TestUnknownArchRejected(t *testing.T) {
	c := NewPolicyCompiler()
	if _, err := c.Compile(basePolicy()); err != nil {
		t.Fatalf("supported architecture must compile: %v", err)
	}
	bad := Policy{Archs: []Arch{Arch(0x12345678)}, Rules: []Rule{{Syscall: "read", Action: ActAllow}}}
	if _, err := c.Compile(bad); err == nil {
		t.Fatalf("unknown architecture must be rejected")
	}
}

func TestX32ArchRejected(t *testing.T) {
	c := NewPolicyCompiler()
	if _, err := c.Compile(basePolicy()); err != nil {
		t.Fatalf("supported architecture must compile: %v", err)
	}
	x32 := Policy{Archs: []Arch{ArchX86_64 | Arch(X32SyscallBit)}, Rules: []Rule{{Syscall: "read", Action: ActAllow}}}
	if _, err := c.Compile(x32); err == nil {
		t.Fatalf("x32 architecture value must be rejected")
	}
}

func TestPtraceRuleRejected(t *testing.T) {
	c := NewPolicyCompiler()
	if _, err := c.Compile(basePolicy()); err != nil {
		t.Fatalf("policy with read rule must compile: %v", err)
	}
	bad := Policy{Rules: []Rule{{Syscall: "ptrace", Action: ActAllow}}}
	if _, err := c.Compile(bad); err == nil {
		t.Fatalf("policy that allows ptrace must be rejected")
	}
}

func TestBpfRuleRejected(t *testing.T) {
	c := NewPolicyCompiler()
	if _, err := c.Compile(basePolicy()); err != nil {
		t.Fatalf("policy with read rule must compile: %v", err)
	}
	bad := Policy{Rules: []Rule{{Syscall: "bpf", Action: ActAllow}}}
	if _, err := c.Compile(bad); err == nil {
		t.Fatalf("policy that allows bpf must be rejected")
	}
}

func TestFilterLengthLimitEnforced(t *testing.T) {
	c := NewPolicyCompiler()
	rules := make([]Rule, 0, 100)
	for i := 0; i < 100; i++ {
		rules = append(rules, Rule{Syscall: "sys" + strconv.Itoa(i), Action: ActAllow})
	}
	p := Policy{Archs: []Arch{ArchX86_64}, Rules: rules}
	if _, err := c.CompileWithOptions(p, Options{}); err != nil {
		t.Fatalf("policy must compile with the default instruction limit: %v", err)
	}
	if _, err := c.CompileWithOptions(p, Options{MaxInstructions: 16}); err == nil {
		t.Fatalf("program longer than MaxInstructions must be rejected")
	}
}

func TestThreadSyncRequiredForMultiThreaded(t *testing.T) {
	c := NewPolicyCompiler()
	p := basePolicy()
	if _, err := c.CompileWithOptions(p, Options{MultiThreaded: true}); err == nil {
		t.Fatalf("multi threaded target without ThreadSync must be rejected")
	}
	if _, err := c.CompileWithOptions(p, Options{MultiThreaded: true, ThreadSync: true}); err != nil {
		t.Fatalf("ThreadSync opt-in must compile: %v", err)
	}
}

func TestThreadSyncFlagEmitted(t *testing.T) {
	c := NewPolicyCompiler()
	prog, err := c.CompileWithOptions(basePolicy(), Options{ThreadSync: true})
	if err != nil {
		t.Fatalf("CompileWithOptions returned error: %v", err)
	}
	if prog.Flags&FlagThreadSync == 0 {
		t.Fatalf("program flags %#x do not contain FlagThreadSync", prog.Flags)
	}
}

func TestNoNewPrivsFlagEmitted(t *testing.T) {
	c := NewPolicyCompiler()
	prog, err := c.CompileWithOptions(basePolicy(), Options{NoNewPrivs: true})
	if err != nil {
		t.Fatalf("CompileWithOptions returned error: %v", err)
	}
	if prog.Flags&FlagNoNewPrivs == 0 {
		t.Fatalf("program flags %#x do not contain FlagNoNewPrivs", prog.Flags)
	}
}

func TestPointerArgRangeChecked(t *testing.T) {
	c := NewPolicyCompiler()
	instrs, err := c.CompileRule(Rule{
		Syscall:  "read",
		Action:   ActAllow,
		Pointers: []int{1},
	})
	if err != nil {
		t.Fatalf("CompileRule returned error: %v", err)
	}
	var lowerOK, upperOK bool
	for _, in := range instrs {
		if in.ArgIndex != 1 {
			continue
		}
		if in.K == MaxUserPointer {
			upperOK = true
		}
		if in.K == MinUserPointer {
			lowerOK = true
		}
	}
	if !upperOK {
		t.Errorf("no upper bound check against MaxUserPointer (%#x) for pointer argument", MaxUserPointer)
	}
	if !lowerOK {
		t.Errorf("no lower bound check against MinUserPointer (%#x) for pointer argument", MinUserPointer)
	}
}

func TestLoadFailureFailsClosed(t *testing.T) {
	c := NewPolicyCompiler()
	first := &Program{DefaultAction: ActKill}
	if err := c.LoadWith(first, func(*Program) error { return nil }); err != nil {
		t.Fatalf("LoadWith returned error for a healthy loader: %v", err)
	}
	if c.Active() != first {
		t.Fatalf("Active() does not report the loaded program")
	}
	second := &Program{DefaultAction: ActAllow}
	if err := c.LoadWith(second, func(*Program) error { return errors.New("kernel rejected program") }); err == nil {
		t.Fatalf("LoadWith must return the loader error")
	}
	if c.Active() != first {
		t.Fatalf("failed load replaced or cleared the active filter; must keep enforcing the previous program")
	}
}

func TestHotReloadAtomicReplace(t *testing.T) {
	c := NewPolicyCompiler()
	old := &Program{DefaultAction: ActKill}
	if err := c.LoadWith(old, func(*Program) error { return nil }); err != nil {
		t.Fatalf("initial LoadWith returned error: %v", err)
	}
	fresh := &Program{DefaultAction: ActTrap}
	if err := c.ReloadWith(fresh, func(*Program) error { return errors.New("reload rejected") }); err == nil {
		t.Fatalf("ReloadWith must propagate loader failure")
	}
	if c.Active() != old {
		t.Fatalf("failed reload did not keep the old program active")
	}
	if err := c.ReloadWith(fresh, func(*Program) error { return nil }); err != nil {
		t.Fatalf("successful ReloadWith returned error: %v", err)
	}
	if c.Active() != fresh {
		t.Fatalf("Active() does not report the reloaded program")
	}
}
