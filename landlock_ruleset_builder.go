package task

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Access is a Landlock access right bit set.
type Access uint32

const (
	AccessRead    Access = 1 << 0
	AccessWrite   Access = 1 << 1
	AccessExecute Access = 1 << 2
	AccessRemove  Access = 1 << 3

	allAccess = AccessRead | AccessWrite | AccessExecute | AccessRemove
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

type netRuleKey struct {
	port     uint16
	protocol int
}

// RulesetBuilder collects path and network rules.
type RulesetBuilder struct {
	mu        sync.RWMutex
	pathRules map[string]Access
	netRules  map[netRuleKey]Access
	mounts    map[string]string
	sandboxed bool
}

// NewRulesetBuilder returns an empty builder.
func NewRulesetBuilder() *RulesetBuilder {
	return &RulesetBuilder{
		pathRules: make(map[string]Access),
		netRules:  make(map[netRuleKey]Access),
		mounts:    make(map[string]string),
	}
}

// AddPathRule adds a filesystem access rule.
func (b *RulesetBuilder) AddPathRule(path string, access Access) error {
	if b == nil {
		return errors.New("landlock: nil ruleset builder")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("landlock: path rule %q must be absolute", path)
	}
	if err := validateAccess(access); err != nil {
		return err
	}
	resolved, err := resolveRealPath(path)
	if err != nil {
		return fmt.Errorf("landlock: resolve path %q: %w", path, err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pathRules == nil {
		b.pathRules = make(map[string]Access)
	}
	b.pathRules[resolved] |= access
	return nil
}

// AddNetRule adds a network access rule.
func (b *RulesetBuilder) AddNetRule(port uint16, protocol int, access Access) error {
	if b == nil {
		return errors.New("landlock: nil ruleset builder")
	}
	if port == 0 {
		return errors.New("landlock: network rule port must not be zero")
	}
	if protocol != ProtoTCP && protocol != ProtoUDP {
		return fmt.Errorf("landlock: unsupported network protocol %d", protocol)
	}
	if err := validateAccess(access); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.netRules == nil {
		b.netRules = make(map[netRuleKey]Access)
	}
	b.netRules[netRuleKey{port: port, protocol: protocol}] |= access
	return nil
}

// SetMountTable sets the container to host path translation table.
func (b *RulesetBuilder) SetMountTable(table map[string]string) {
	if b == nil {
		return
	}
	copied := make(map[string]string, len(table))
	keys := make([]string, 0, len(table))
	for from := range table {
		keys = append(keys, from)
	}
	sort.Strings(keys)
	for _, from := range keys {
		to := table[from]
		if !filepath.IsAbs(from) || !filepath.IsAbs(to) {
			continue
		}
		copied[filepath.Clean(from)] = filepath.Clean(to)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.mounts = copied
}

// Build produces the ruleset description.
func (b *RulesetBuilder) Build(abi int, opts BuildOptions) (*Ruleset, error) {
	if b == nil {
		return nil, errors.New("landlock: nil ruleset builder")
	}
	if abi < 0 {
		return nil, fmt.Errorf("landlock: invalid ABI %d", abi)
	}
	if opts.RequiredABI < 0 {
		return nil, fmt.Errorf("landlock: invalid required ABI %d", opts.RequiredABI)
	}
	if abi < opts.RequiredABI {
		return nil, fmt.Errorf("landlock: ABI %d is below required ABI %d", abi, opts.RequiredABI)
	}

	b.mu.RLock()
	paths := sortedPathRules(b.pathRules)
	nets := sortedNetRules(b.netRules)
	b.mu.RUnlock()

	if opts.MaxRules > 0 && len(paths)+len(nets) > opts.MaxRules {
		return nil, fmt.Errorf("landlock: %d rules exceed limit %d", len(paths)+len(nets), opts.MaxRules)
	}

	return &Ruleset{
		ABI:        abi,
		PathRules:  paths,
		NetRules:   nets,
		Handled:    unionAccess(paths, nets),
		NoNewPrivs: opts.NoNewPrivs,
	}, nil
}

// HandledAccess reports the union of access rights requiring enforcement.
func (b *RulesetBuilder) HandledAccess() Access {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	var handled Access
	for _, access := range b.pathRules {
		handled |= access
	}
	for _, access := range b.netRules {
		handled |= access
	}
	return handled
}

// Match reports whether path with the requested access is covered by a rule.
func (b *RulesetBuilder) Match(path string, access Access) bool {
	if b == nil || !filepath.IsAbs(path) {
		return false
	}
	resolved, err := resolveRealPath(path)
	if err != nil {
		return false
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	var granted Access
	covered := false
	for rulePath, ruleAccess := range b.pathRules {
		if pathWithin(rulePath, resolved) {
			granted |= ruleAccess
			covered = true
		}
	}
	return covered && granted&access == access
}

// Plan returns the runtime steps needed to apply the ruleset.
func (b *RulesetBuilder) Plan(rs *Ruleset) ([]Step, error) {
	if b == nil {
		return nil, errors.New("landlock: nil ruleset builder")
	}
	if rs == nil {
		return nil, errors.New("landlock: nil ruleset")
	}

	paths, err := normalizedPathRules(rs.PathRules)
	if err != nil {
		return nil, err
	}
	nets, err := normalizedNetRules(rs.NetRules)
	if err != nil {
		return nil, err
	}

	b.mu.RLock()
	mounts := make(map[string]string, len(b.mounts))
	for from, to := range b.mounts {
		mounts[from] = to
	}
	b.mu.RUnlock()

	translated := make(map[string]Access, len(paths))
	for _, rule := range paths {
		planPath := translateMountPath(rule.Path, mounts)
		translated[planPath] |= rule.Access
	}
	paths = sortedPathRules(translated)

	steps := make([]Step, 0, len(paths)+len(nets)+4)
	steps = append(steps, Step{Op: "create_ruleset", FD: rulesetFD})
	for _, rule := range paths {
		steps = append(steps, Step{
			Op:     "add_path_rule",
			Path:   rule.Path,
			Access: rule.Access,
			FD:     rulesetFD,
		})
	}
	for _, rule := range nets {
		steps = append(steps, Step{
			Op:     "add_net_rule",
			Port:   rule.Port,
			Proto:  rule.Protocol,
			Access: rule.Access,
			FD:     rulesetFD,
		})
	}
	if rs.NoNewPrivs {
		steps = append(steps, Step{Op: "no_new_privs"})
	}
	steps = append(steps,
		Step{Op: "restrict_self", FD: rulesetFD},
		Step{Op: "close", FD: rulesetFD},
	)
	return steps, nil
}

// Apply executes the ruleset steps against ex.
func (b *RulesetBuilder) Apply(rs *Ruleset, ex Executor) error {
	if b == nil {
		return errors.New("landlock: nil ruleset builder")
	}
	b.setSandboxed(false)

	if rs == nil {
		return errors.New("landlock: nil ruleset")
	}
	if !rs.NoNewPrivs {
		return errors.New("landlock: refusing to apply without no_new_privs")
	}
	if ex == nil {
		return errors.New("landlock: nil executor")
	}

	steps, err := b.Plan(rs)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if err := ex.Exec(step); err != nil {
			b.setSandboxed(false)
			return fmt.Errorf("landlock: %s: %w", step.Op, err)
		}
	}

	b.setSandboxed(true)
	return nil
}

// Sandboxed reports whether the last Apply restricted the process.
func (b *RulesetBuilder) Sandboxed() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sandboxed
}

const rulesetFD = 3

func (b *RulesetBuilder) setSandboxed(value bool) {
	b.mu.Lock()
	b.sandboxed = value
	b.mu.Unlock()
}

func validateAccess(access Access) error {
	if access == 0 {
		return errors.New("landlock: access must not be empty")
	}
	if access&^allAccess != 0 {
		return fmt.Errorf("landlock: unsupported access bits %#x", uint32(access&^allAccess))
	}
	return nil
}

func resolveRealPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return "", err
	}
	resolvedParent, err := resolveRealPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(cleaned)), nil
}

func pathWithin(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	if filepath.IsAbs(relative) || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sortedPathRules(rules map[string]Access) []PathRule {
	paths := make([]string, 0, len(rules))
	for path := range rules {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	result := make([]PathRule, 0, len(paths))
	for _, path := range paths {
		result = append(result, PathRule{Path: path, Access: rules[path]})
	}
	return result
}

func sortedNetRules(rules map[netRuleKey]Access) []NetRule {
	result := make([]NetRule, 0, len(rules))
	for key, access := range rules {
		result = append(result, NetRule{
			Port:     key.port,
			Protocol: key.protocol,
			Access:   access,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Port != result[j].Port {
			return result[i].Port < result[j].Port
		}
		return result[i].Protocol < result[j].Protocol
	})
	return result
}

func normalizedPathRules(rules []PathRule) ([]PathRule, error) {
	merged := make(map[string]Access, len(rules))
	for _, rule := range rules {
		if !filepath.IsAbs(rule.Path) {
			return nil, fmt.Errorf("landlock: path rule %q must be absolute", rule.Path)
		}
		merged[filepath.Clean(rule.Path)] |= rule.Access
	}
	return sortedPathRules(merged), nil
}

func normalizedNetRules(rules []NetRule) ([]NetRule, error) {
	merged := make(map[netRuleKey]Access, len(rules))
	for _, rule := range rules {
		if rule.Port == 0 {
			return nil, errors.New("landlock: network rule port must not be zero")
		}
		if rule.Protocol != ProtoTCP && rule.Protocol != ProtoUDP {
			return nil, fmt.Errorf("landlock: unsupported network protocol %d", rule.Protocol)
		}
		merged[netRuleKey{port: rule.Port, protocol: rule.Protocol}] |= rule.Access
	}
	return sortedNetRules(merged), nil
}

func unionAccess(paths []PathRule, nets []NetRule) Access {
	var handled Access
	for _, rule := range paths {
		handled |= rule.Access
	}
	for _, rule := range nets {
		handled |= rule.Access
	}
	return handled
}

func translateMountPath(path string, mounts map[string]string) string {
	bestFrom := ""
	bestTo := ""
	for from, to := range mounts {
		if !pathWithin(from, path) {
			continue
		}
		if len(from) > len(bestFrom) || (len(from) == len(bestFrom) && from < bestFrom) {
			bestFrom = from
			bestTo = to
		}
	}
	if bestFrom == "" {
		return path
	}

	relative, err := filepath.Rel(bestFrom, path)
	if err != nil {
		return path
	}
	if relative == "." {
		return filepath.Clean(bestTo)
	}
	return filepath.Join(bestTo, relative)
}
