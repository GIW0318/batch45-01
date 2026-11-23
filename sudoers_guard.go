package task

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// maxExpandDepth bounds alias and group expansion. A self-reference or cycle
// is reported as an error long before this limit, but the limit also caps
// pathological configurations so expansion can never spin unbounded.
const maxExpandDepth = 32

// shellMetaChars are characters that let a command or argument string escape
// into a shell. Any of them in a request is grounds for denial.
const shellMetaChars = ";|&$`<>(){}[]*?!#~\n\r"

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// CmdSpec is one command specification inside a sudoers rule. Args is a list
// of argument patterns that must match the request exactly.
type CmdSpec struct {
	Path string
	Args []string
}

// Rule is one sudoers rule.
type Rule struct {
	Users    []string
	Hosts    []string
	RunAs    []string
	Commands []CmdSpec
	NoPasswd bool
	Negated  bool
}

// Request is one sudo invocation to evaluate.
type Request struct {
	User    string
	Host    string
	RunAs   string
	Command string
	Args    string
}

// Decision is the outcome of evaluating a request against the rule set.
type Decision struct {
	Verdict   Verdict
	NoPasswd  bool
	RuleIndex int
}

// SudoersGuard parses and evaluates sudoers policy. It holds no state of its
// own.
type SudoersGuard struct{}

// ExpandAlias resolves a user/host/command alias transitively. Aliases may
// reference other aliases, so one level of expansion is not enough.
func (g *SudoersGuard) ExpandAlias(name string, aliases map[string][]string) ([]string, error) {
	if _, ok := aliases[name]; !ok {
		return nil, fmt.Errorf("alias %q is not defined", name)
	}
	var out []string
	seen := make(map[string]bool)
	err := expandRef(name, aliases, map[string]bool{name: true}, 0, seen, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MatchCommand matches a command against a CmdSpec. The full path must match
// exactly; matching by prefix would let /usr/bin/vimdiff inherit a rule for
// /usr/bin/vim.
func (g *SudoersGuard) MatchCommand(path string, spec CmdSpec) (Verdict, error) {
	if spec.Path == "" || path == "" {
		return VerdictDeny, nil
	}
	if strings.ContainsAny(spec.Path, "*?") {
		ok, err := pathMatch(spec.Path, path)
		if err != nil {
			return VerdictDeny, err
		}
		if ok {
			return VerdictAllow, nil
		}
		return VerdictDeny, nil
	}
	if path == spec.Path {
		return VerdictAllow, nil
	}
	return VerdictDeny, nil
}

// MatchArgs matches the request's argument string against the argument
// patterns. Arguments must match token for token; appended commands or shell
// metacharacters are not accepted.
func (g *SudoersGuard) MatchArgs(args string, pattern []string) (Verdict, error) {
	if strings.ContainsAny(args, shellMetaChars) {
		return VerdictDeny, nil
	}
	tokens := strings.Fields(args)
	if len(tokens) != len(pattern) {
		return VerdictDeny, nil
	}
	for i, tok := range tokens {
		if tok != pattern[i] {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// ExpandGroup resolves a user group recursively, including groups that are
// members of other groups.
func (g *SudoersGuard) ExpandGroup(group string, db map[string][]string) ([]string, error) {
	name := strings.TrimPrefix(group, "%")
	if _, ok := db[name]; !ok {
		return nil, fmt.Errorf("group %q is not defined", name)
	}
	var out []string
	seen := make(map[string]bool)
	err := expandRef(name, db, map[string]bool{name: true}, 0, seen, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CheckHost requires the request host to be listed by the rule.
func (g *SudoersGuard) CheckHost(hosts []string, host string) (Verdict, error) {
	if host == "" {
		return VerdictDeny, nil
	}
	for _, h := range hosts {
		if h == "*" || h == host {
			return VerdictAllow, nil
		}
	}
	return VerdictDeny, nil
}

// ResolveNoPasswd returns the NOPASSWD tag of the rule that actually matched
// (rules[matchIndex]); tags must not be inherited from another rule.
func (g *SudoersGuard) ResolveNoPasswd(rules []Rule, matchIndex int) (bool, error) {
	if matchIndex < 0 || matchIndex >= len(rules) {
		return false, fmt.Errorf("match index %d out of range for %d rules", matchIndex, len(rules))
	}
	return rules[matchIndex].NoPasswd, nil
}

// ResolveRunAs validates the runas list. An empty runas list must not default
// to root.
func (g *SudoersGuard) ResolveRunAs(runAs []string) (Verdict, error) {
	if len(runAs) == 0 {
		return VerdictDeny, nil
	}
	for _, target := range runAs {
		if strings.TrimSpace(target) == "" {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// ValidateInclude restricts included files to the sudoers drop-in directory.
func (g *SudoersGuard) ValidateInclude(path, root string) (Verdict, error) {
	if path == "" || root == "" {
		return VerdictDeny, nil
	}
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return VerdictDeny, nil
	}
	if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
		return VerdictAllow, nil
	}
	return VerdictDeny, nil
}

// Evaluate applies the rule set to a request. The last matching rule wins,
// which gives negated rules their veto power.
func (g *SudoersGuard) Evaluate(rules []Rule, req Request) (Decision, error) {
	if v, err := g.SanitizeCommand(req.Command); err != nil {
		return Decision{}, err
	} else if v != VerdictAllow {
		return Decision{Verdict: VerdictDeny, RuleIndex: -1}, nil
	}

	matchIndex := -1
	negated := false
	for i, rule := range rules {
		if !listMatches(rule.Users, req.User) {
			continue
		}
		hostOK, err := g.CheckHost(rule.Hosts, req.Host)
		if err != nil {
			return Decision{}, err
		}
		if hostOK != VerdictAllow {
			continue
		}
		if !listMatches(rule.RunAs, req.RunAs) {
			continue
		}
		cmdOK := false
		for _, spec := range rule.Commands {
			cv, err := g.MatchCommand(req.Command, spec)
			if err != nil {
				return Decision{}, err
			}
			if cv != VerdictAllow {
				continue
			}
			av, err := g.MatchArgs(req.Args, spec.Args)
			if err != nil {
				return Decision{}, err
			}
			if av == VerdictAllow {
				cmdOK = true
				break
			}
		}
		if !cmdOK {
			continue
		}
		matchIndex = i
		negated = rule.Negated
	}

	if matchIndex < 0 || negated {
		return Decision{Verdict: VerdictDeny, RuleIndex: matchIndex}, nil
	}
	noPasswd, err := g.ResolveNoPasswd(rules, matchIndex)
	if err != nil {
		return Decision{}, err
	}
	return Decision{Verdict: VerdictAllow, NoPasswd: noPasswd, RuleIndex: matchIndex}, nil
}

// SanitizeCommand refuses commands carrying shell metacharacters, which could
// be used to escape into a shell.
func (g *SudoersGuard) SanitizeCommand(cmd string) (Verdict, error) {
	if cmd == "" {
		return VerdictDeny, nil
	}
	if strings.ContainsAny(cmd, shellMetaChars) {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// InvalidateCache drops every cached decision after a policy change.
func (g *SudoersGuard) InvalidateCache(cache map[string]Decision) (map[string]Decision, error) {
	return make(map[string]Decision, len(cache)), nil
}

// ParsePolicy returns the verdict for a policy text. A policy that fails to
// parse must deny instead of falling back to allow.
func (g *SudoersGuard) ParsePolicy(text string) (Verdict, error) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := validatePolicyLine(line); err != nil {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// validatePolicyLine checks one sudoers-style rule line for the structure
// "users host=(runas) [tags:] commands" or "users host=commands".
func validatePolicyLine(line string) error {
	if strings.Count(line, "(") != strings.Count(line, ")") {
		return errors.New("unbalanced runas parentheses")
	}
	eq := strings.Index(line, "=")
	if eq <= 0 {
		return errors.New("rule is missing host= separator")
	}
	head := strings.Fields(line[:eq])
	if len(head) < 2 {
		return errors.New("rule needs a user list and a host list before =")
	}
	rest := strings.TrimSpace(line[eq+1:])
	if strings.HasPrefix(rest, "(") {
		close := strings.Index(rest, ")")
		if close < 0 {
			return errors.New("runas list is not closed")
		}
		if strings.TrimSpace(rest[1:close]) == "" {
			return errors.New("runas list is empty")
		}
		rest = strings.TrimSpace(rest[close+1:])
	}
	rest = strings.TrimPrefix(rest, "NOPASSWD:")
	rest = strings.TrimPrefix(rest, "PASSWD:")
	if strings.TrimSpace(rest) == "" {
		return errors.New("rule has no command list")
	}
	return nil
}

// expandRef recursively expands name against db. Members that are themselves
// keys in db are expanded in place; anything else is a leaf. active tracks the
// current expansion chain so self-references and cycles fail fast, and depth
// caps the recursion at maxExpandDepth.
func expandRef(name string, db map[string][]string, active map[string]bool, depth int, seen map[string]bool, out *[]string) error {
	if depth > maxExpandDepth {
		return fmt.Errorf("expansion of %q exceeds depth limit %d", name, maxExpandDepth)
	}
	for _, member := range db[name] {
		key := strings.TrimPrefix(member, "%")
		if _, isRef := db[key]; isRef {
			if active[key] {
				return fmt.Errorf("cycle detected expanding %q via %q", name, key)
			}
			active[key] = true
			if err := expandRef(key, db, active, depth+1, seen, out); err != nil {
				return err
			}
			delete(active, key)
			continue
		}
		if !seen[member] {
			seen[member] = true
			*out = append(*out, member)
		}
	}
	return nil
}

// listMatches reports whether value is listed, with "*" matching anything.
func listMatches(list []string, value string) bool {
	if value == "" {
		return false
	}
	for _, item := range list {
		if item == "*" || item == value {
			return true
		}
	}
	return false
}

func pathMatch(pattern, name string) (bool, error) {
	return path.Match(pattern, name)
}
