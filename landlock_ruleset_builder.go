package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Access is a Landlock access right bit set.
type Access uint32

const (
	AccessRead    Access = 1 << 0
	AccessWrite   Access = 1 << 1
	AccessExecute Access = 1 << 2
	AccessRemove  Access = 1 << 3
)

// Network protocols accepted by network rules.
const (
	ProtoTCP = 6
	ProtoUDP = 17
)

// PathRule grants access below a filesystem path.
type PathRule struct {
	Path   string
	Access Access
}

// NetRule grants access to one port of one protocol.
type NetRule struct {
	Port     uint16
	Protocol int
	Access   Access
}

// Ruleset is the built ruleset description.
type Ruleset struct {
	ABI        int
	PathRules  []PathRule
	NetRules   []NetRule
	Handled    Access
	NoNewPrivs bool
}

// BuildOptions controls ruleset construction.
type BuildOptions struct {
	RequiredABI int
	NoNewPrivs  bool
	MaxRules    int
}

// Step is one runtime operation of applying a ruleset.
type Step struct {
	Op     string
	Path   string
	Port   uint16
	Proto  int
	Access Access
	FD     int
}

// Executor performs a single runtime step.
type Executor interface {
	Exec(Step) error
}

// RulesetBuilder collects path and network rules.
type RulesetBuilder struct{}

// NewRulesetBuilder returns an empty builder.
func NewRulesetBuilder() *RulesetBuilder {
	return &RulesetBuilder{}
}

// AddPathRule adds a filesystem access rule.
func (b *RulesetBuilder) AddPathRule(path string, access Access) error {
	return errNotImplemented
}

// AddNetRule adds a network access rule.
func (b *RulesetBuilder) AddNetRule(port uint16, protocol int, access Access) error {
	return errNotImplemented
}

// SetMountTable sets the container to host path translation table.
func (b *RulesetBuilder) SetMountTable(table map[string]string) {}

// Build produces the ruleset description.
func (b *RulesetBuilder) Build(abi int, opts BuildOptions) (*Ruleset, error) {
	return nil, errNotImplemented
}

// HandledAccess reports the union of access rights requiring enforcement.
func (b *RulesetBuilder) HandledAccess() Access {
	return 0
}

// Match reports whether path with the requested access is covered by a rule.
func (b *RulesetBuilder) Match(path string, access Access) bool {
	return false
}

// Plan returns the runtime steps needed to apply the ruleset.
func (b *RulesetBuilder) Plan(rs *Ruleset) ([]Step, error) {
	return nil, errNotImplemented
}

// Apply executes the ruleset steps against ex.
func (b *RulesetBuilder) Apply(rs *Ruleset, ex Executor) error {
	return errNotImplemented
}

// Sandboxed reports whether the last Apply restricted the process.
func (b *RulesetBuilder) Sandboxed() bool {
	return false
}
