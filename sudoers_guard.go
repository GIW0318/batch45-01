package task

import "errors"

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
	return nil, errors.New("not implemented")
}

// MatchCommand matches a command against a CmdSpec. The full path must match
// exactly; matching by prefix would let /usr/bin/vimdiff inherit a rule for
// /usr/bin/vim.
func (g *SudoersGuard) MatchCommand(path string, spec CmdSpec) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// MatchArgs matches the request's argument string against the argument
// patterns. Arguments must match token for token; appended commands or shell
// metacharacters are not accepted.
func (g *SudoersGuard) MatchArgs(args string, pattern []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// ExpandGroup resolves a user group recursively, including groups that are
// members of other groups.
func (g *SudoersGuard) ExpandGroup(group string, db map[string][]string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// CheckHost requires the request host to be listed by the rule.
func (g *SudoersGuard) CheckHost(hosts []string, host string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// ResolveNoPasswd returns the NOPASSWD tag of the rule that actually matched
// (rules[matchIndex]); tags must not be inherited from another rule.
func (g *SudoersGuard) ResolveNoPasswd(rules []Rule, matchIndex int) (bool, error) {
	return false, errors.New("not implemented")
}

// ResolveRunAs validates the runas list. An empty runas list must not default
// to root.
func (g *SudoersGuard) ResolveRunAs(runAs []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// ValidateInclude restricts included files to the sudoers drop-in directory.
func (g *SudoersGuard) ValidateInclude(path, root string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// Evaluate applies the rule set to a request. The last matching rule wins,
// which gives negated rules their veto power.
func (g *SudoersGuard) Evaluate(rules []Rule, req Request) (Decision, error) {
	return Decision{}, errors.New("not implemented")
}

// SanitizeCommand refuses commands carrying shell metacharacters, which could
// be used to escape into a shell.
func (g *SudoersGuard) SanitizeCommand(cmd string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// InvalidateCache drops every cached decision after a policy change.
func (g *SudoersGuard) InvalidateCache(cache map[string]Decision) (map[string]Decision, error) {
	return nil, errors.New("not implemented")
}

// ParsePolicy returns the verdict for a policy text. A policy that fails to
// parse must deny instead of falling back to allow.
func (g *SudoersGuard) ParsePolicy(text string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}
