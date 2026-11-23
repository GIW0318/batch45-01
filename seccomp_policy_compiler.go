package task

import "errors"

var errNotImplemented = errors.New("not implemented")

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
type PolicyCompiler struct{}

// NewPolicyCompiler returns an empty compiler.
func NewPolicyCompiler() *PolicyCompiler {
	return &PolicyCompiler{}
}

// Compile compiles the policy with default options.
func (c *PolicyCompiler) Compile(p Policy) (*Program, error) {
	return nil, errNotImplemented
}

// CompileWithOptions compiles the policy with explicit options.
func (c *PolicyCompiler) CompileWithOptions(p Policy, opts Options) (*Program, error) {
	return nil, errNotImplemented
}

// OrderedRules returns the rules in evaluation order.
func (c *PolicyCompiler) OrderedRules(p Policy) ([]Rule, error) {
	return nil, errNotImplemented
}

// CompileRule compiles a single rule into instructions.
func (c *PolicyCompiler) CompileRule(r Rule) ([]Instruction, error) {
	return nil, errNotImplemented
}

// Load installs the program with the default loader.
func (c *PolicyCompiler) Load(prog *Program) error {
	return errNotImplemented
}

// LoadWith installs the program with the supplied loader.
func (c *PolicyCompiler) LoadWith(prog *Program, l Loader) error {
	return errNotImplemented
}

// ReloadWith atomically replaces the active program.
func (c *PolicyCompiler) ReloadWith(prog *Program, l Loader) error {
	return errNotImplemented
}

// Active returns the program currently installed.
func (c *PolicyCompiler) Active() *Program {
	return nil
}
