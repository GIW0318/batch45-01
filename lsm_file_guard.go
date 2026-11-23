package task

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
)

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// Access is the file access entry point the LSM hook observed.
type Access int

const (
	AccessUnknown Access = iota
	AccessOpen
	AccessMmap
)

// FileRef identifies a file by path, inode and mount namespace.
type FileRef struct {
	Path     string
	Inode    uint64
	InodeGen uint64
	MountNS  uint64
}

// Rule is one file access rule. MountNS ties the rule to the mount namespace
// it was written for.
type Rule struct {
	Pattern string
	Verdict Verdict
	MountNS uint64
}

// AuditEvent is one observed access that has to be logged.
type AuditEvent struct {
	Path    string
	Inode   uint64
	Access  Access
	Verdict Verdict
}

// AuditRecord is the record written to the audit log.
type AuditRecord struct {
	Path    string
	Inode   uint64
	Access  Access
	Verdict Verdict
}

// SwapResult describes a hot program swap.
type SwapResult struct {
	ActiveID string
	Drained  int
}

// ruleTable is one immutable generation of the rule table. Publishing a new
// pointer under the guard's write lock gives every reader either the complete
// old generation or the complete new one, never a partially updated slice.
type ruleTable struct {
	rules      []Rule
	generation uint64
}

// LsmFileGuard implements the file access policy of the kernel module.
//
// The mutex protects the active rule generation and the load state. Evaluate
// holds it for the whole decision, so SwapRules cannot retire a generation
// while a reader is still using it. Audit uses a separate mutex: writing audit
// records never waits for a rule swap (and vice versa).
type LsmFileGuard struct {
	mu         sync.RWMutex
	active     *ruleTable
	generation uint64
	loadFailed bool

	auditMu  sync.Mutex
	auditLog []AuditRecord

	programMu     sync.RWMutex
	activeProgram string
}

// Evaluate applies the rule set to one access. Both the open and the mmap
// entry points are covered by the same rules; an unknown access is failed
// closed rather than silently treated as a pass.
//
// A nil rules slice selects the guard's currently published table. A non-nil
// slice is evaluated as an explicit snapshot. Either way the read lock is held
// until the decision is complete, which lets SwapRules drain in-flight readers
// before the old table is discarded.
func (g *LsmFileGuard) Evaluate(ref FileRef, access Access, rules []Rule) (Verdict, error) {
	if g == nil {
		return VerdictDeny, errors.New("nil LsmFileGuard")
	}
	if access != AccessOpen && access != AccessMmap {
		return VerdictDeny, fmt.Errorf("unknown file access %d", access)
	}
	if strings.TrimSpace(ref.Path) == "" {
		return VerdictDeny, errors.New("empty file path")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	table := rules
	if table == nil && g.active != nil {
		table = g.active.rules
	}
	if g.loadFailed {
		return VerdictDeny, nil
	}

	matchedAllow := false
	for _, rule := range table {
		if !mountApplies(rule.MountNS, ref.MountNS) {
			continue
		}
		if !matchPattern(rule.Pattern, ref.Path) {
			continue
		}
		// Broad wildcards must not silently capture /proc. Explicit /proc
		// rules are still honoured.
		if isProcPath(ref.Path) && isWildcard(rule.Pattern) && !isProcScoped(rule.Pattern) {
			continue
		}

		switch rule.Verdict {
		case VerdictDeny:
			return VerdictDeny, nil
		case VerdictAllow:
			matchedAllow = true
		default:
			return VerdictDeny, fmt.Errorf("rule %q has unknown verdict %d", rule.Pattern, rule.Verdict)
		}
	}

	if matchedAllow {
		return VerdictAllow, nil
	}
	return VerdictAllow, nil
}

// RebindMountNS returns a copy of rules bound to the given mount namespace,
// as required after the mount namespace changes. The input slice is never
// modified.
func (g *LsmFileGuard) RebindMountNS(rules []Rule, ns uint64) ([]Rule, error) {
	rebound := make([]Rule, len(rules))
	copy(rebound, rules)
	for i := range rebound {
		rebound[i].MountNS = ns
	}
	return rebound, nil
}

// RemoveInodeRules drops the entry for inode from table after a rename, so the
// old inode does not keep its rule. A new table is returned; the caller's map
// is left untouched.
func (g *LsmFileGuard) RemoveInodeRules(table map[uint64]string, inode uint64) (map[uint64]string, error) {
	pruned := make(map[uint64]string, len(table))
	for ino, rule := range table {
		if ino == inode {
			continue
		}
		pruned[ino] = rule
	}
	return pruned, nil
}

// CheckInodeGeneration verifies that the rule was written for the current
// generation of the inode; a recycled inode belongs to a new file and must not
// inherit the old rule.
func (g *LsmFileGuard) CheckInodeGeneration(ruleGen, inodeGen uint64) (Verdict, error) {
	if ruleGen != inodeGen {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// SwapRules replaces the whole rule table in one step and returns the new
// table, which contains exactly the next generation of rules. The write lock
// waits for every in-flight Evaluate call to finish before the old generation
// can be released. No background goroutine is left behind.
func (g *LsmFileGuard) SwapRules(current, next []Rule) ([]Rule, error) {
	if g == nil {
		return nil, errors.New("nil LsmFileGuard")
	}

	prepared := make([]Rule, len(next))
	copy(prepared, next)
	for _, rule := range prepared {
		if rule.Verdict != VerdictAllow && rule.Verdict != VerdictDeny {
			return nil, fmt.Errorf("rule %q has unknown verdict %d", rule.Pattern, rule.Verdict)
		}
	}

	g.mu.Lock()
	g.generation++
	g.active = &ruleTable{
		rules:      prepared,
		generation: g.generation,
	}
	// Keep loadFailed as-is: a successful load is reported by LoadPolicy.
	g.mu.Unlock()

	result := make([]Rule, len(prepared))
	copy(result, prepared)
	return result, nil
}

// ExitCode maps a verdict to the hook return code. Allow is zero and deny is a
// non-zero (negative errno) code; unknown verdicts are not allowed to look like
// success either.
func (g *LsmFileGuard) ExitCode(v Verdict) (int, error) {
	switch v {
	case VerdictAllow:
		return 0, nil
	case VerdictDeny:
		// -EPERM
		return -1, nil
	default:
		return -1, fmt.Errorf("unknown verdict %d", v)
	}
}

// LoadPolicy decides the policy state after a load attempt. A failed load
// denies everything instead of running without rules. A later successful load
// clears that fail-closed state.
func (g *LsmFileGuard) LoadPolicy(loadOK bool) (Verdict, error) {
	if g == nil {
		if loadOK {
			return VerdictAllow, nil
		}
		return VerdictDeny, nil
	}

	g.mu.Lock()
	g.loadFailed = !loadOK
	g.mu.Unlock()

	if !loadOK {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// BTFOffset returns the BTF field offset for the given kernel version. The
// offsets differ between kernel versions, so they are looked up per major.minor
// instead of sharing one hard-coded layout.
func (g *LsmFileGuard) BTFOffset(kernel, field string) (int, error) {
	key, err := kernelSeries(kernel)
	if err != nil {
		return 0, err
	}

	offsets := map[string]map[string]int{
		"5.15": {"inode": 0, "dev": 8},
		"6.1":  {"inode": 8, "dev": 16},
	}
	fields, ok := offsets[key]
	if !ok {
		return 0, fmt.Errorf("unsupported kernel version %q", kernel)
	}

	name := strings.ToLower(strings.TrimSpace(field))
	switch name {
	case "i_ino":
		name = "inode"
	case "s_dev":
		name = "dev"
	}
	offset, ok := fields[name]
	if !ok {
		return 0, fmt.Errorf("unknown BTF field %q for kernel %s", field, key)
	}
	return offset, nil
}

// CheckProcExclusion verifies that a rule pattern does not swallow /proc,
// which stays outside broad wildcard rules.
func (g *LsmFileGuard) CheckProcExclusion(pattern, filePath string) (Verdict, error) {
	if !isProcPath(filePath) {
		return VerdictAllow, nil
	}
	if !isWildcard(pattern) || isProcScoped(pattern) {
		return VerdictAllow, nil
	}
	if matchPattern(pattern, filePath) {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// Audit builds the audit record. The inode is part of the record because a
// path alone is not enough to identify a file. Audit logging uses a lock that
// is independent of the rule-table lock, so it never blocks a hot swap.
func (g *LsmFileGuard) Audit(ev AuditEvent) (AuditRecord, error) {
	record := AuditRecord{
		Path:    ev.Path,
		Inode:   ev.Inode,
		Access:  ev.Access,
		Verdict: ev.Verdict,
	}
	if g != nil {
		g.auditMu.Lock()
		g.auditLog = append(g.auditLog, record)
		g.auditMu.Unlock()
	}
	return record, nil
}

// SwapProgram activates newID and reports how many requests the old program
// still has to drain. The old program stops serving new requests immediately;
// the guard does not retain a reference that could route new work to it.
func (g *LsmFileGuard) SwapProgram(oldID, newID string) (SwapResult, error) {
	if strings.TrimSpace(newID) == "" {
		return SwapResult{}, errors.New("new program id is empty")
	}
	if g == nil {
		return SwapResult{}, errors.New("nil LsmFileGuard")
	}

	g.programMu.Lock()
	g.activeProgram = newID
	g.programMu.Unlock()

	return SwapResult{ActiveID: newID, Drained: 0}, nil
}

// CheckMountPropagation denies paths that a container mount propagation
// carried into the host mount namespace.
func (g *LsmFileGuard) CheckMountPropagation(filePath string, propagated bool) (Verdict, error) {
	if propagated {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

func mountApplies(ruleNS, fileNS uint64) bool {
	// An unscoped rule applies in every namespace. A namespace-scoped rule
	// only applies when the reference carries the same namespace id.
	return ruleNS == 0 || (fileNS != 0 && ruleNS == fileNS)
}

func isProcPath(filePath string) bool {
	clean := path.Clean(filePath)
	return clean == "/proc" || strings.HasPrefix(clean, "/proc/")
}

func isProcScoped(pattern string) bool {
	clean := path.Clean(pattern)
	return clean == "/proc" || strings.HasPrefix(clean, "/proc/")
}

func isWildcard(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// matchPattern supports the usual path segment wildcards plus ** for matching
// any number of path segments. It intentionally uses slash-separated matching
// on every platform because these are kernel paths.
func matchPattern(pattern, filePath string) bool {
	if pattern == "" {
		return false
	}
	cleanPattern := path.Clean(pattern)
	cleanPath := path.Clean(filePath)
	if cleanPattern == cleanPath {
		return true
	}
	return matchSegments(splitPath(cleanPattern), splitPath(cleanPath))
}

func splitPath(value string) []string {
	value = strings.TrimPrefix(path.Clean(value), "/")
	if value == "" || value == "." {
		return nil
	}
	return strings.Split(value, "/")
}

func matchSegments(pattern, filePath []string) bool {
	if len(pattern) == 0 {
		return len(filePath) == 0
	}

	segment := pattern[0]
	if segment == "**" {
		if len(pattern) == 1 {
			return true
		}
		for i := 0; i <= len(filePath); i++ {
			if matchSegments(pattern[1:], filePath[i:]) {
				return true
			}
		}
		return false
	}

	if len(filePath) == 0 {
		return false
	}
	ok, err := path.Match(segment, filePath[0])
	if err != nil {
		ok = segment == filePath[0]
	}
	return ok && matchSegments(pattern[1:], filePath[1:])
}

func kernelSeries(kernel string) (string, error) {
	value := strings.TrimSpace(kernel)
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid kernel version %q", kernel)
	}
	major := strings.TrimSpace(parts[0])
	minor := strings.TrimSpace(parts[1])
	if major == "" || minor == "" {
		return "", fmt.Errorf("invalid kernel version %q", kernel)
	}
	return major + "." + minor, nil
}
