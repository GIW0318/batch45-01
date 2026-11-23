package task

import (
	"testing"

	. "gsb/batch45-safety-c-12"
)

func TestExpandAliasRecursesNestedAliases(t *testing.T) {
	g := &SudoersGuard{}
	aliases := map[string][]string{
		"WEB": {"APP"},
		"APP": {"alice", "bob"},
	}
	got, err := g.ExpandAlias("WEB", aliases)
	if err != nil {
		t.Fatalf("ExpandAlias: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("nested alias must expand transitively, got %v", got)
	}
}

func TestMatchCommandDeniesPrefixOnlyMatch(t *testing.T) {
	g := &SudoersGuard{}
	spec := CmdSpec{Path: "/usr/bin/vim"}
	got, err := g.MatchCommand("/usr/bin/vimdiff", spec)
	if err != nil {
		t.Fatalf("MatchCommand: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("/usr/bin/vimdiff must not inherit the /usr/bin/vim rule, got %v", got)
	}
}

func TestMatchCommandAllowsExactPath(t *testing.T) {
	g := &SudoersGuard{}
	spec := CmdSpec{Path: "/usr/bin/vim"}
	got, err := g.MatchCommand("/usr/bin/vim", spec)
	if err != nil {
		t.Fatalf("MatchCommand: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("exact path match, want allow, got %v", got)
	}
}

func TestMatchArgsDeniesInjectedExtraArgs(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.MatchArgs("status; rm -rf /", []string{"status"})
	if err != nil {
		t.Fatalf("MatchArgs: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("argument pattern must not swallow an injected command, got %v", got)
	}
}

func TestExpandGroupRecursesNestedGroups(t *testing.T) {
	g := &SudoersGuard{}
	db := map[string][]string{
		"ops":    {"devops", "carol"},
		"devops": {"alice", "bob"},
	}
	got, err := g.ExpandGroup("ops", db)
	if err != nil {
		t.Fatalf("ExpandGroup: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("nested group must expand to 3 members, got %v", got)
	}
}

func TestCheckHostDeniesOtherHost(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.CheckHost([]string{"web01", "web02"}, "db01")
	if err != nil {
		t.Fatalf("CheckHost: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("host not listed by the rule, want deny, got %v", got)
	}
}

func TestCheckHostAllowsListedHost(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.CheckHost([]string{"web01", "web02"}, "web02")
	if err != nil {
		t.Fatalf("CheckHost: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("host listed by the rule, want allow, got %v", got)
	}
}

func TestResolveNoPasswdUsesMatchedRuleOnly(t *testing.T) {
	g := &SudoersGuard{}
	rules := []Rule{
		{Users: []string{"bob"}, NoPasswd: true},
		{Users: []string{"alice"}, NoPasswd: false},
	}
	got, err := g.ResolveNoPasswd(rules, 1)
	if err != nil {
		t.Fatalf("ResolveNoPasswd: %v", err)
	}
	if got {
		t.Fatalf("NOPASSWD must not be inherited from rule 0, got %v", got)
	}
}

func TestResolveRunAsDeniesEmptyList(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.ResolveRunAs(nil)
	if err != nil {
		t.Fatalf("ResolveRunAs: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("empty runas list must not default to root, got %v", got)
	}
}

func TestResolveRunAsAllowsExplicitTarget(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.ResolveRunAs([]string{"postgres"})
	if err != nil {
		t.Fatalf("ResolveRunAs: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("explicit runas target, want allow, got %v", got)
	}
}

func TestValidateIncludeDeniesOutsideRoot(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.ValidateInclude("/etc/passwd", "/etc/sudoers.d")
	if err != nil {
		t.Fatalf("ValidateInclude: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("include outside the drop-in directory, want deny, got %v", got)
	}
}

func TestEvaluateAppliesNegatedRuleLast(t *testing.T) {
	g := &SudoersGuard{}
	rules := []Rule{
		{
			Users:    []string{"alice"},
			Hosts:    []string{"*"},
			RunAs:    []string{"root"},
			Commands: []CmdSpec{{Path: "/usr/bin/systemctl", Args: []string{"restart"}}},
		},
		{
			Users:    []string{"alice"},
			Hosts:    []string{"*"},
			RunAs:    []string{"root"},
			Commands: []CmdSpec{{Path: "/usr/bin/systemctl", Args: []string{"restart"}}},
			Negated:  true,
		},
	}
	req := Request{User: "alice", Host: "web01", RunAs: "root", Command: "/usr/bin/systemctl", Args: "restart"}
	got, err := g.Evaluate(rules, req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got.Verdict != VerdictDeny {
		t.Fatalf("last matching negated rule must win, got %v", got.Verdict)
	}
}

func TestSanitizeCommandRejectsShellMeta(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.SanitizeCommand("systemctl restart nginx; rm -rf /")
	if err != nil {
		t.Fatalf("SanitizeCommand: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("command with shell metacharacters, want deny, got %v", got)
	}
}

func TestInvalidateCacheClearsEntries(t *testing.T) {
	g := &SudoersGuard{}
	cache := map[string]Decision{"alice|/usr/bin/vim": {Verdict: VerdictAllow}}
	got, err := g.InvalidateCache(cache)
	if err != nil {
		t.Fatalf("InvalidateCache: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("policy change must invalidate every cached decision, got %d", len(got))
	}
}

func TestParsePolicyDeniesMalformedInput(t *testing.T) {
	g := &SudoersGuard{}
	got, err := g.ParsePolicy("alice ALL=(ALL")
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("malformed policy must deny, got %v", got)
	}
}
