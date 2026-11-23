package task

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Action is the action a seccomp filter returns when a rule matches.
type Action uint32

const (
	ActUnset Action = 0
	ActKill  Action = 1
	ActTrap  Action = 2
	ActErrno Action = 3
	ActAllow Action = 4
)

// Arch is a seccomp audit architecture value.
type Arch uint32

const (
	ArchX86_64 Arch = 0xC000003E
	ArchARM64  Arch = 0xC00000B7
)

// X32SyscallBit marks the x32 ABI syscall range.
const X32SyscallBit uint32 = 0x40000000

// Filter flags handed to the kernel when the program is installed.
const (
	FlagThreadSync uint32 = 1 << 0
	FlagNoNewPrivs uint32 = 1 << 1
)

// Userspace pointer bounds used for pointer argument validation.
const (
	MinUserPointer uint64 = 0x0000000000010000
	MaxUserPointer uint64 = 0x00007FFFFFFFFFFF
)

// BuiltinMaxInstructions is the kernel limit on filter length.
const BuiltinMaxInstructions = 4096

// ArgCondition constrains one syscall argument.
type ArgCondition struct {
	Index int
	Op    string
	Value uint64
	Mask  uint64
}

// Rule is a single policy rule for one syscall on one architecture.
type Rule struct {
	Syscall  string
	Arch     Arch
	Args     []ArgCondition
	Pointers []int
	Action   Action
	Errno    int
	Priority int
}

// Policy is the declarative input of the compiler.
type Policy struct {
	DefaultAction Action
	Rules         []Rule
	Archs         []Arch
}

// Instruction is one classic BPF instruction of the compiled program.
type Instruction struct {
	Op       string
	K        uint64
	Jt       int
	Jf       int
	ArgIndex int
	Mask     uint64
}

// Program is the compiled seccomp program.
type Program struct {
	DefaultAction Action
	Instructions  []Instruction
	Archs         []Arch
	Flags         uint32
}

// Options controls a compile run.
type Options struct {
	MaxInstructions int
	ThreadSync      bool
	NoNewPrivs      bool
	MultiThreaded   bool
}

// Loader installs a compiled program into the kernel.
type Loader func(*Program) error

// PolicyCompiler compiles syscall policies into seccomp programs.
type PolicyCompiler struct {
	mu     sync.RWMutex
	active *Program
}

// NewPolicyCompiler returns an empty compiler.
func NewPolicyCompiler() *PolicyCompiler {
	return &PolicyCompiler{}
}

// Compile compiles the policy with default options.
func (c *PolicyCompiler) Compile(p Policy) (*Program, error) {
	return c.CompileWithOptions(p, Options{})
}

// CompileWithOptions compiles the policy with explicit options.
func (c *PolicyCompiler) CompileWithOptions(p Policy, opts Options) (*Program, error) {
	if opts.MultiThreaded && !opts.ThreadSync {
		return nil, errors.New("multi threaded target requires the ThreadSync option")
	}
	max := opts.MaxInstructions
	if max <= 0 {
		max = BuiltinMaxInstructions
	}
	def := p.DefaultAction
	if def == ActUnset {
		def = ActKill
	}
	for _, a := range p.Archs {
		if err := checkArch(a); err != nil {
			return nil, err
		}
	}
	for _, r := range p.Rules {
		if r.Arch != 0 {
			if err := checkArch(r.Arch); err != nil {
				return nil, err
			}
		}
		if err := checkDangerousAllow(r); err != nil {
			return nil, err
		}
	}
	ordered, err := c.OrderedRules(p)
	if err != nil {
		return nil, err
	}
	ins := make([]Instruction, 0, len(ordered)+1)
	for _, r := range ordered {
		ri, err := c.CompileRule(r)
		if err != nil {
			return nil, err
		}
		ins = append(ins, ri...)
	}
	ins = append(ins, Instruction{Op: "ret", K: uint64(def)})
	if len(ins) > max {
		return nil, fmt.Errorf("program has %d instructions, limit is %d", len(ins), max)
	}
	var flags uint32
	if opts.ThreadSync {
		flags |= FlagThreadSync
	}
	if opts.NoNewPrivs {
		flags |= FlagNoNewPrivs
	}
	return &Program{
		DefaultAction: def,
		Instructions:  ins,
		Archs:         canonicalArchs(p.Archs),
		Flags:         flags,
	}, nil
}

// OrderedRules returns the rules in evaluation order.
func (c *PolicyCompiler) OrderedRules(p Policy) ([]Rule, error) {
	rules := append([]Rule(nil), p.Rules...)
	sort.Slice(rules, func(i, j int) bool { return ruleLess(rules[i], rules[j]) })
	return rules, nil
}

// CompileRule compiles a single rule into instructions.
func (c *PolicyCompiler) CompileRule(r Rule) ([]Instruction, error) {
	if err := checkDangerousAllow(r); err != nil {
		return nil, err
	}
	conds := make([]Instruction, 0, 2*len(r.Pointers)+2*len(r.Args))
	for _, idx := range r.Pointers {
		if idx < 0 || idx > 5 {
			return nil, fmt.Errorf("pointer argument index %d out of range", idx)
		}
		conds = append(conds,
			Instruction{Op: "jge", K: MinUserPointer, ArgIndex: idx, Mask: ^uint64(0)},
			Instruction{Op: "jle", K: MaxUserPointer, ArgIndex: idx, Mask: ^uint64(0)},
		)
	}
	for _, a := range r.Args {
		if a.Index < 0 || a.Index > 5 {
			return nil, fmt.Errorf("argument index %d out of range", a.Index)
		}
		if a.Op == "mask" {
			conds = append(conds, Instruction{
				Op:       "jset",
				K:        a.Value & a.Mask,
				ArgIndex: a.Index,
				Mask:     a.Mask,
			})
			continue
		}
		var op string
		switch a.Op {
		case "eq":
			op = "jeq"
		case "ne":
			op = "jne"
		case "gt":
			op = "jgt"
		case "ge":
			op = "jge"
		case "lt":
			op = "jlt"
		case "le":
			op = "jle"
		default:
			return nil, fmt.Errorf("unsupported argument op %q", a.Op)
		}
		conds = append(conds,
			Instruction{Op: op, K: a.Value & 0xFFFFFFFF, ArgIndex: a.Index, Mask: 0xFFFFFFFF},
			Instruction{Op: op, K: a.Value & 0xFFFFFFFF00000000, ArgIndex: a.Index, Mask: 0xFFFFFFFF00000000},
		)
	}
	action := r.Action
	if action == ActUnset {
		action = ActKill
	}
	k := uint64(action)
	if action == ActErrno {
		k |= uint64(r.Errno&0xFFFF) << 16
	}
	out := make([]Instruction, 0, len(conds)+1)
	for i, cond := range conds {
		cond.Jt = 0
		cond.Jf = len(conds) - i
		out = append(out, cond)
	}
	out = append(out, Instruction{Op: "ret", K: k})
	return out, nil
}

// checkArch rejects unknown architectures and the x32 ABI.
func checkArch(a Arch) error {
	switch a {
	case ArchX86_64, ArchARM64:
		return nil
	}
	if uint32(a)&X32SyscallBit != 0 && uint32(a)&0xFFFF == 0x3E {
		return fmt.Errorf("x32 architecture %#x is not supported", uint32(a))
	}
	return fmt.Errorf("unknown architecture %#x", uint32(a))
}

// checkDangerousAllow rejects rules that allow ptrace or bpf.
func checkDangerousAllow(r Rule) error {
	if r.Action != ActAllow {
		return nil
	}
	switch r.Syscall {
	case "ptrace", "bpf":
		return fmt.Errorf("rule allows %s: refusing to compile the policy", r.Syscall)
	}
	return nil
}

// canonicalArchs returns a sorted, deduplicated copy of archs so the
// compiled program does not depend on input ordering.
func canonicalArchs(archs []Arch) []Arch {
	out := append([]Arch(nil), archs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 0
	for _, a := range out {
		if n == 0 || out[n-1] != a {
			out[n] = a
			n++
		}
	}
	return out[:n]
}

// ruleLess is a total order over rules: grouped by syscall, priority
// descending within a group, with full-content tie breakers so the result
// is independent of input order and map iteration.
func ruleLess(a, b Rule) bool {
	if a.Syscall != b.Syscall {
		return a.Syscall < b.Syscall
	}
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if a.Arch != b.Arch {
		return a.Arch < b.Arch
	}
	if a.Action != b.Action {
		return a.Action < b.Action
	}
	if a.Errno != b.Errno {
		return a.Errno < b.Errno
	}
	if d := compareArgConditions(a.Args, b.Args); d != 0 {
		return d < 0
	}
	return compareInts(a.Pointers, b.Pointers) < 0
}

func compareArgConditions(a, b []ArgCondition) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	for i := range a {
		if a[i].Index != b[i].Index {
			return a[i].Index - b[i].Index
		}
		if a[i].Op != b[i].Op {
			if a[i].Op < b[i].Op {
				return -1
			}
			return 1
		}
		if a[i].Value != b[i].Value {
			if a[i].Value < b[i].Value {
				return -1
			}
			return 1
		}
		if a[i].Mask != b[i].Mask {
			if a[i].Mask < b[i].Mask {
				return -1
			}
			return 1
		}
	}
	return 0
}

func compareInts(a, b []int) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

// defaultLoader validates a program before it is installed.
func defaultLoader(p *Program) error {
	if p == nil {
		return errors.New("nil program")
	}
	if len(p.Instructions) > BuiltinMaxInstructions {
		return fmt.Errorf("program has %d instructions, kernel limit is %d",
			len(p.Instructions), BuiltinMaxInstructions)
	}
	for i, in := range p.Instructions {
		if in.Jt < 0 || in.Jf < 0 {
			return fmt.Errorf("instruction %d has a negative jump offset", i)
		}
		if i+1+in.Jt > len(p.Instructions) || i+1+in.Jf > len(p.Instructions) {
			return fmt.Errorf("instruction %d jumps out of the program", i)
		}
	}
	return nil
}

// Load installs the program with the default loader.
func (c *PolicyCompiler) Load(prog *Program) error {
	return c.LoadWith(prog, defaultLoader)
}

// LoadWith installs the program with the supplied loader.
func (c *PolicyCompiler) LoadWith(prog *Program, l Loader) error {
	if prog == nil {
		return errors.New("nil program")
	}
	if l == nil {
		l = defaultLoader
	}
	if err := l(prog); err != nil {
		return err
	}
	c.mu.Lock()
	c.active = prog
	c.mu.Unlock()
	return nil
}

// ReloadWith atomically replaces the active program.
func (c *PolicyCompiler) ReloadWith(prog *Program, l Loader) error {
	return c.LoadWith(prog, l)
}

// Active returns the program currently installed.
func (c *PolicyCompiler) Active() *Program {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}
