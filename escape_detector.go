package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sentinel errors used by the detector's parsing helpers.
var (
	ErrMalformedMountInfo   = errors.New("malformed mountinfo line")
	ErrInvalidSeccompStatus = errors.New("invalid seccomp status")
)

// SeccompMode is the seccomp mode reported by /proc/<pid>/status.
type SeccompMode int

const (
	SeccompUnset SeccompMode = iota
	SeccompDisabled
	SeccompStrict
	SeccompFilter
)

// NamespaceSet collects the namespace inode numbers of one process.
type NamespaceSet struct {
	Mnt  uint64
	Pid  uint64
	Net  uint64
	User uint64
}

// MountEntry is one parsed line of /proc/<pid>/mountinfo.
type MountEntry struct {
	Source  string
	Target  string
	FSType  string
	Options []string
}

// ProcessIdentity is the snapshot the detector reasons about.
type ProcessIdentity struct {
	PID        int
	PidNS      uint64
	Root       string
	Name       string
	ExePath    string
	ExeInode   uint64
	ExeDev     uint64
	ExeSHA256  string
	CgroupPath string
	CapEff     uint64
	Seccomp    SeccompMode
	Namespaces NamespaceSet
}

// Rule describes one escape detection rule.
type Rule struct {
	ID           string
	Severity     string
	ExeSHA256    []string
	ExeInodes    []uint64
	CgroupPrefix string
	PidNS        uint64
}

// Finding is one rule hit.
type Finding struct {
	RuleID   string
	Severity string
	PID      int
	Reason   string
}

// WhitelistEntry allows a privileged container to run on the host.
type WhitelistEntry struct {
	ImageRef        string
	AllowPrivileged bool
	Expires         time.Time
}

// EscapeDetector detects container escape attempts on a host.
type EscapeDetector struct {
	rules []Rule

	scanMu sync.RWMutex
	scans  map[string]scanEntry

	baseMu   sync.RWMutex
	baseline []ProcessIdentity

	// AlertWindow is how long an identical alert is suppressed after it
	// was first reported, so a container flapping in and out of the scan
	// set does not re-trigger the same finding.
	AlertWindow time.Duration
	alertMu     sync.Mutex
	alerts      map[alertKey]time.Time
}

type scanEntry struct {
	generation uint64
	findings   []Finding
}

type alertKey struct {
	ruleID string
	pid    int
	inode  uint64
}

// NewEscapeDetector builds a detector with the given rules.
func NewEscapeDetector(rules []Rule) *EscapeDetector {
	return &EscapeDetector{
		rules:       rules,
		scans:       make(map[string]scanEntry),
		alerts:      make(map[alertKey]time.Time),
		AlertWindow: 5 * time.Minute,
	}
}

// MatchProcess evaluates every rule against one process snapshot.
func (d *EscapeDetector) MatchProcess(p ProcessIdentity) ([]Finding, error) {
	var out []Finding
	for _, r := range d.rules {
		if r.PidNS != 0 && r.PidNS != p.PidNS {
			continue
		}
		if r.CgroupPrefix != "" {
			ok, err := d.RuleApplies(r, p.CgroupPath)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		matched := false
		reason := ""
		for _, h := range r.ExeSHA256 {
			if h != "" && h == p.ExeSHA256 {
				matched = true
				reason = "executable sha256 matches rule digest"
				break
			}
		}
		if !matched {
			for _, ino := range r.ExeInodes {
				if ino != 0 && ino == p.ExeInode {
					matched = true
					reason = "executable inode matches rule"
					break
				}
			}
		}
		if !matched && len(r.ExeSHA256) == 0 && len(r.ExeInodes) == 0 && r.CgroupPrefix != "" {
			matched = true
			reason = "cgroup path matches rule prefix"
		}
		if !matched {
			continue
		}
		out = append(out, Finding{
			RuleID:   r.ID,
			Severity: r.Severity,
			PID:      p.PID,
			Reason:   reason,
		})
	}
	return out, nil
}

// ProcValue resolves a /proc value, reporting whether the source is trustworthy.
func (d *EscapeDetector) ProcValue(path string, values map[string]string, masked []string) (string, bool, error) {
	for _, m := range masked {
		if m == path {
			return "", false, nil
		}
	}
	v, ok := values[path]
	if !ok {
		return "", false, nil
	}
	return v, true, nil
}

// capNames indexes capability names by bit number, 0..40.
var capNames = []string{
	"cap_chown",              // 0
	"cap_dac_override",       // 1
	"cap_dac_read_search",    // 2
	"cap_fowner",             // 3
	"cap_fsetid",             // 4
	"cap_kill",               // 5
	"cap_setgid",             // 6
	"cap_setuid",             // 7
	"cap_setpcap",            // 8
	"cap_linux_immutable",    // 9
	"cap_net_bind_service",   // 10
	"cap_net_broadcast",      // 11
	"cap_net_admin",          // 12
	"cap_net_raw",            // 13
	"cap_ipc_lock",           // 14
	"cap_ipc_owner",          // 15
	"cap_sys_module",         // 16
	"cap_sys_rawio",          // 17
	"cap_sys_chroot",         // 18
	"cap_sys_ptrace",         // 19
	"cap_sys_pacct",          // 20
	"cap_sys_admin",          // 21
	"cap_sys_boot",           // 22
	"cap_sys_nice",           // 23
	"cap_sys_resource",       // 24
	"cap_sys_time",           // 25
	"cap_sys_tty_config",     // 26
	"cap_mknod",              // 27
	"cap_lease",              // 28
	"cap_audit_write",        // 29
	"cap_audit_control",      // 30
	"cap_setfcap",            // 31
	"cap_mac_override",       // 32
	"cap_mac_admin",          // 33
	"cap_syslog",             // 34
	"cap_wake_alarm",         // 35
	"cap_block_suspend",      // 36
	"cap_audit_read",         // 37
	"cap_perfmon",            // 38
	"cap_bpf",                // 39
	"cap_checkpoint_restore", // 40
}

// ParseCapabilities expands a 64 bit effective capability bitmap into names.
func (d *EscapeDetector) ParseCapabilities(eff uint64) []string {
	names := []string{}
	for bit := 0; bit < 64; bit++ {
		if eff&(uint64(1)<<uint(bit)) == 0 {
			continue
		}
		if bit < len(capNames) {
			names = append(names, capNames[bit])
		} else {
			names = append(names, fmt.Sprintf("cap_unknown_%d", bit))
		}
	}
	return names
}

// NamespaceEscape lists the namespaces that differ between two processes.
func (d *EscapeDetector) NamespaceEscape(a, b NamespaceSet) ([]string, error) {
	diff := []string{}
	if a.Mnt != b.Mnt {
		diff = append(diff, "mnt")
	}
	if a.Pid != b.Pid {
		diff = append(diff, "pid")
	}
	if a.Net != b.Net {
		diff = append(diff, "net")
	}
	if a.User != b.User {
		diff = append(diff, "user")
	}
	return diff, nil
}

// unescapeOctal decodes the \ooo octal escapes used by mountinfo.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			v := (s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')
			b.WriteByte(v)
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// ParseMountInfo decodes mountinfo lines, honouring octal escapes.
func (d *EscapeDetector) ParseMountInfo(lines []string) ([]MountEntry, error) {
	entries := make([]MountEntry, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return nil, fmt.Errorf("%w: %q", ErrMalformedMountInfo, line)
		}
		entries = append(entries, MountEntry{
			Source:  unescapeOctal(fields[0]),
			Target:  unescapeOctal(fields[1]),
			FSType:  unescapeOctal(fields[2]),
			Options: strings.Split(fields[3], ","),
		})
	}
	return entries, nil
}

// PutScan stores a scan result under a generation number.
func (d *EscapeDetector) PutScan(containerID string, generation uint64, findings []Finding) error {
	cp := make([]Finding, len(findings))
	copy(cp, findings)
	d.scanMu.Lock()
	d.scans[containerID] = scanEntry{generation: generation, findings: cp}
	d.scanMu.Unlock()
	return nil
}

// GetScan loads a scan result, but only for the requested generation.
func (d *EscapeDetector) GetScan(containerID string, generation uint64) ([]Finding, bool) {
	d.scanMu.RLock()
	entry, ok := d.scans[containerID]
	d.scanMu.RUnlock()
	if !ok || entry.generation != generation {
		return nil, false
	}
	cp := make([]Finding, len(entry.findings))
	copy(cp, entry.findings)
	return cp, true
}

// InvalidateScan drops every cached scan of one container.
func (d *EscapeDetector) InvalidateScan(containerID string) {
	d.scanMu.Lock()
	delete(d.scans, containerID)
	d.scanMu.Unlock()
}

// Authorize decides whether a privileged container is allowed by the whitelist.
func (d *EscapeDetector) Authorize(entry WhitelistEntry, imageRef string, privileged bool, now time.Time) (bool, error) {
	if entry.ImageRef == "" || strings.ContainsAny(entry.ImageRef, "*?") {
		return false, nil
	}
	if !entry.Expires.IsZero() && !now.Before(entry.Expires) {
		return false, nil
	}
	if imageRef != entry.ImageRef {
		return false, nil
	}
	if privileged && !entry.AllowPrivileged {
		return false, nil
	}
	return true, nil
}

// ParseSeccompStatus parses the Seccomp field of /proc/<pid>/status.
func (d *EscapeDetector) ParseSeccompStatus(raw string) (SeccompMode, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, ":"); i >= 0 {
		if strings.TrimSpace(s[:i]) != "Seccomp" {
			return SeccompUnset, fmt.Errorf("%w: %q", ErrInvalidSeccompStatus, raw)
		}
		s = strings.TrimSpace(s[i+1:])
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return SeccompUnset, fmt.Errorf("%w: %q", ErrInvalidSeccompStatus, raw)
	}
	switch n {
	case 0:
		return SeccompDisabled, nil
	case 1:
		return SeccompStrict, nil
	case 2:
		return SeccompFilter, nil
	}
	return SeccompUnset, fmt.Errorf("%w: %q", ErrInvalidSeccompStatus, raw)
}

// RuleApplies reports whether a rule's cgroup prefix covers a cgroup path.
func (d *EscapeDetector) RuleApplies(rule Rule, cgroup string) (bool, error) {
	prefix := strings.Trim(rule.CgroupPrefix, "/")
	if prefix == "" {
		return true, nil
	}
	path := strings.Trim(cgroup, "/")
	if path == prefix {
		return true, nil
	}
	return strings.HasPrefix(path, prefix+"/"), nil
}

// SaveBaseline serialises the trusted process baseline.
func (d *EscapeDetector) SaveBaseline(procs []ProcessIdentity) ([]byte, error) {
	return json.Marshal(procs)
}

// LoadBaseline restores a previously saved baseline.
func (d *EscapeDetector) LoadBaseline(data []byte) error {
	var procs []ProcessIdentity
	if err := json.Unmarshal(data, &procs); err != nil {
		return err
	}
	d.baseMu.Lock()
	d.baseline = procs
	d.baseMu.Unlock()
	return nil
}

// baselineKey identifies a process across restarts, where PIDs change.
type baselineKey struct {
	pidNS uint64
	name  string
	inode uint64
}

// BaselineDrift compares the current process set with the stored baseline.
func (d *EscapeDetector) BaselineDrift(procs []ProcessIdentity) ([]Finding, error) {
	d.baseMu.RLock()
	base := make([]ProcessIdentity, len(d.baseline))
	copy(base, d.baseline)
	d.baseMu.RUnlock()

	keyOf := func(p ProcessIdentity) baselineKey {
		return baselineKey{pidNS: p.PidNS, name: p.Name, inode: p.ExeInode}
	}
	inBase := make(map[baselineKey]bool, len(base))
	for _, p := range base {
		inBase[keyOf(p)] = true
	}
	inNow := make(map[baselineKey]bool, len(procs))
	var out []Finding
	for _, p := range procs {
		k := keyOf(p)
		inNow[k] = true
		if !inBase[k] {
			out = append(out, Finding{
				RuleID:   "baseline.new-process",
				Severity: "high",
				PID:      p.PID,
				Reason:   fmt.Sprintf("process %q (inode %d) not present in baseline", p.Name, p.ExeInode),
			})
		}
	}
	for _, p := range base {
		if !inNow[keyOf(p)] {
			out = append(out, Finding{
				RuleID:   "baseline.missing-process",
				Severity: "medium",
				PID:      p.PID,
				Reason:   fmt.Sprintf("baseline process %q (inode %d) no longer running", p.Name, p.ExeInode),
			})
		}
	}
	return out, nil
}

// FilterAlerts drops findings identical to ones reported within AlertWindow,
// so a container that leaves and re-enters the scan set inside the window
// only raises each alert once. Findings that pass are recorded at now.
func (d *EscapeDetector) FilterAlerts(findings []Finding, idents []ProcessIdentity, now time.Time) []Finding {
	inodeOf := make(map[int]uint64, len(idents))
	for _, p := range idents {
		inodeOf[p.PID] = p.ExeInode
	}
	d.alertMu.Lock()
	defer d.alertMu.Unlock()
	var out []Finding
	for _, f := range findings {
		k := alertKey{ruleID: f.RuleID, pid: f.PID, inode: inodeOf[f.PID]}
		if last, ok := d.alerts[k]; ok && now.Sub(last) < d.AlertWindow {
			continue
		}
		d.alerts[k] = now
		out = append(out, f)
	}
	return out
}

// Scan matches every process against all rules and suppresses duplicate
// alerts inside the alert window. It is safe for concurrent use.
func (d *EscapeDetector) Scan(procs []ProcessIdentity, now time.Time) ([]Finding, error) {
	var all []Finding
	for _, p := range procs {
		fs, err := d.MatchProcess(p)
		if err != nil {
			return nil, err
		}
		all = append(all, fs...)
	}
	return d.FilterAlerts(all, procs, now), nil
}
