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

// LsmFileGuard implements the file access policy of the kernel module. It
// holds no state of its own.
type LsmFileGuard struct{}

// Evaluate applies the rule set to one access. Both the open and the mmap
// entry points must be covered by the same rules; checking only one of them
// leaves a bypass.
func (g *LsmFileGuard) Evaluate(ref FileRef, access Access, rules []Rule) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// RebindMountNS returns a copy of rules bound to the given mount namespace,
// as required after the mount namespace changes.
func (g *LsmFileGuard) RebindMountNS(rules []Rule, ns uint64) ([]Rule, error) {
	return nil, errors.New("not implemented")
}

// RemoveInodeRules drops the entry for inode from table after a rename, so the
// old inode does not keep its rule.
func (g *LsmFileGuard) RemoveInodeRules(table map[uint64]string, inode uint64) (map[uint64]string, error) {
	return nil, errors.New("not implemented")
}

// CheckInodeGeneration verifies that the rule was written for the current
// generation of the inode; a recycled inode belongs to a new file and must not
// inherit the old rule.
func (g *LsmFileGuard) CheckInodeGeneration(ruleGen, inodeGen uint64) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// SwapRules replaces the whole rule table in one step and returns the new
// table, which must contain exactly the next generation of rules.
func (g *LsmFileGuard) SwapRules(current, next []Rule) ([]Rule, error) {
	return nil, errors.New("not implemented")
}

// ExitCode maps a verdict to the hook return code. Allow is zero and deny is a
// non-zero code, and the mapping is never ambiguous.
func (g *LsmFileGuard) ExitCode(v Verdict) (int, error) {
	return 0, errors.New("not implemented")
}

// LoadPolicy decides the policy state after a load attempt. A failed load must
// deny everything instead of running without rules.
func (g *LsmFileGuard) LoadPolicy(loadOK bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// BTFOffset returns the BTF field offset for the given kernel version. The
// offsets differ between kernel versions: 5.15 has inode at 0 and dev at 8,
// 6.1 has inode at 8 and dev at 16.
func (g *LsmFileGuard) BTFOffset(kernel, field string) (int, error) {
	return 0, errors.New("not implemented")
}

// CheckProcExclusion verifies that a rule pattern does not swallow /proc,
// which must stay outside broad wildcard rules.
func (g *LsmFileGuard) CheckProcExclusion(pattern, path string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// Audit builds the audit record; the inode has to be part of it, a path alone
// is not enough to identify a file.
func (g *LsmFileGuard) Audit(ev AuditEvent) (AuditRecord, error) {
	return AuditRecord{}, errors.New("not implemented")
}

// SwapProgram activates newID and reports how many requests the old program
// still has to drain. The old program must stop serving new requests.
func (g *LsmFileGuard) SwapProgram(oldID, newID string) (SwapResult, error) {
	return SwapResult{}, errors.New("not implemented")
}

// CheckMountPropagation denies paths that a container mount propagation
// carried into the host mount namespace.
func (g *LsmFileGuard) CheckMountPropagation(path string, propagated bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}
