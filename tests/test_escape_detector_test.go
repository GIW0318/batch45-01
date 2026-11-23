package task

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	task "gsb/batch45-safety-b-21"
)

func capNames(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestMatchProcessDetectsRenamedBinary(t *testing.T) {
	d := task.NewEscapeDetector([]task.Rule{{
		ID:        "escape.known-tool",
		Severity:  "critical",
		ExeInodes: []uint64{4242},
	}})
	p := task.ProcessIdentity{
		PID:       8811,
		PidNS:     4026532000,
		Root:      "/proc/8811/root",
		Name:      "kworker",
		ExePath:   "/tmp/.cache/sysd",
		ExeInode:  4242,
		ExeDev:    2049,
		ExeSHA256: strings.Repeat("a", 64),
	}
	found, err := d.MatchProcess(p)
	if err != nil {
		t.Fatalf("MatchProcess returned unexpected error: %v", err)
	}
	if len(found) != 1 || found[0].RuleID != "escape.known-tool" {
		t.Fatalf("a renamed binary must still match by inode, got %+v", found)
	}
}

func TestMatchProcessIgnoresPathOnlyMatch(t *testing.T) {
	d := task.NewEscapeDetector([]task.Rule{{
		ID:        "escape.known-tool",
		ExeInodes: []uint64{4242},
	}})
	p := task.ProcessIdentity{
		PID:       12,
		PidNS:     1,
		Name:      "sshd",
		ExePath:   "/usr/sbin/sshd",
		ExeInode:  777,
		ExeSHA256: strings.Repeat("b", 64),
	}
	found, err := d.MatchProcess(p)
	if err != nil {
		t.Fatalf("MatchProcess returned unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("a matching path with a different inode and hash must not match, got %+v", found)
	}
}

func TestMatchProcessSeparatesHostAndContainerSameName(t *testing.T) {
	digest := strings.Repeat("c", 64)
	d := task.NewEscapeDetector([]task.Rule{{
		ID:        "escape.container-digest",
		ExeSHA256: []string{digest},
		PidNS:     4026532000,
	}})
	host := task.ProcessIdentity{
		PID:       100,
		PidNS:     4026531836,
		Root:      "/",
		Name:      "bash",
		ExeSHA256: digest,
	}
	got, err := d.MatchProcess(host)
	if err != nil {
		t.Fatalf("MatchProcess(host) returned unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("host process with the same name and digest must not match a container rule, got %+v", got)
	}
	ctr := task.ProcessIdentity{
		PID:       100,
		PidNS:     4026532000,
		Root:      "/var/lib/docker/overlay2/abc/merged",
		Name:      "bash",
		ExeSHA256: digest,
	}
	got, err = d.MatchProcess(ctr)
	if err != nil {
		t.Fatalf("MatchProcess(container) returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("container process in the rule's pid namespace must match, got %+v", got)
	}
}

func TestProcValueMarksMaskedPathUntrusted(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	values := map[string]string{"/proc/1/status": "CapEff:\t0000000000000003\n"}
	masked := []string{"/proc/1/status"}
	val, trusted, err := d.ProcValue("/proc/1/status", values, masked)
	if err != nil {
		t.Fatalf("ProcValue returned unexpected error: %v", err)
	}
	if trusted {
		t.Fatalf("a masked proc path must not be trusted")
	}
	if val != "" {
		t.Fatalf("a masked proc path must not yield a value, got %q", val)
	}
}

func TestBaselineDriftDetectedAfterRestart(t *testing.T) {
	baseline := []task.ProcessIdentity{
		{PID: 1, PidNS: 1, Root: "/", Name: "init", ExeInode: 10},
		{PID: 42, PidNS: 1, Root: "/", Name: "cron", ExeInode: 20},
	}
	d := task.NewEscapeDetector(nil)
	data, err := d.SaveBaseline(baseline)
	if err != nil {
		t.Fatalf("SaveBaseline returned unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("SaveBaseline returned an empty payload")
	}
	restarted := task.NewEscapeDetector(nil)
	if err := restarted.LoadBaseline(data); err != nil {
		t.Fatalf("LoadBaseline returned unexpected error: %v", err)
	}
	drift, err := restarted.BaselineDrift([]task.ProcessIdentity{
		{PID: 1, PidNS: 1, Root: "/", Name: "init", ExeInode: 10},
		{PID: 43, PidNS: 1, Root: "/", Name: "kworker", ExeInode: 30},
	})
	if err != nil {
		t.Fatalf("BaselineDrift returned unexpected error: %v", err)
	}
	if len(drift) == 0 {
		t.Fatalf("a new process after restart must produce baseline drift")
	}
}

func TestParseCapabilitiesHighWord(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	names := d.ParseCapabilities(1<<40 | 1<<33)
	if !capNames(names, "cap_checkpoint_restore") {
		t.Fatalf("bit 40 must decode to cap_checkpoint_restore, got %v", names)
	}
	if !capNames(names, "cap_mac_admin") {
		t.Fatalf("bit 33 must decode to cap_mac_admin, got %v", names)
	}
}

func TestParseCapabilitiesSortedByBit(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	names := d.ParseCapabilities(1<<2 | 1<<33 | 1<<21 | 1<<21)
	want := []string{"cap_dac_read_search", "cap_sys_admin", "cap_mac_admin"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("capabilities must be sorted by bit index without duplicates, got %v want %v", names, want)
	}
}

func TestNamespaceEscapeReportsUserNamespace(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	a := task.NamespaceSet{Mnt: 1, Pid: 2, Net: 3, User: 100}
	b := task.NamespaceSet{Mnt: 1, Pid: 2, Net: 3, User: 200}
	diff, err := d.NamespaceEscape(a, b)
	if err != nil {
		t.Fatalf("NamespaceEscape returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(diff, []string{"user"}) {
		t.Fatalf("only the user namespace differs, got %v", diff)
	}
}

func TestParseMountInfoOctalEscapes(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	lines := []string{
		`/dev/sda1 /mnt/with\040space ext4 rw,relatime 0 0`,
		`tmpfs /mnt/back\134slash tmpfs rw 0 0`,
	}
	entries, err := d.ParseMountInfo(lines)
	if err != nil {
		t.Fatalf("ParseMountInfo returned unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Target != "/mnt/with space" {
		t.Fatalf("\\040 must decode to a space, got %q", entries[0].Target)
	}
	if entries[1].Target != `/mnt/back\slash` {
		t.Fatalf("\\134 must decode to a backslash, got %q", entries[1].Target)
	}
}

func TestParseMountInfoMalformedLine(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	_, err := d.ParseMountInfo([]string{"bogus"})
	if !errors.Is(err, task.ErrMalformedMountInfo) {
		t.Fatalf("a short mountinfo line must fail with ErrMalformedMountInfo, got %v", err)
	}
}

func TestGetScanMissesStaleGeneration(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	if err := d.PutScan("ctr-1", 7, []task.Finding{{RuleID: "r1", PID: 5}}); err != nil {
		t.Fatalf("PutScan returned unexpected error: %v", err)
	}
	hit, ok := d.GetScan("ctr-1", 7)
	if !ok || len(hit) != 1 {
		t.Fatalf("result for the current generation must be returned, ok=%v got=%+v", ok, hit)
	}
	stale, staleOK := d.GetScan("ctr-1", 8)
	if staleOK || len(stale) != 0 {
		t.Fatalf("result of an older generation must not be returned, ok=%v got=%+v", staleOK, stale)
	}
}

func TestInvalidateScanClearsEntry(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	if err := d.PutScan("ctr-2", 3, []task.Finding{{RuleID: "r2", PID: 9}}); err != nil {
		t.Fatalf("PutScan returned unexpected error: %v", err)
	}
	d.InvalidateScan("ctr-2")
	if _, ok := d.GetScan("ctr-2", 3); ok {
		t.Fatalf("invalidated scan must not be served from cache")
	}
}

func TestAuthorizeRejectsWildcardImage(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	entry := task.WhitelistEntry{ImageRef: "*", AllowPrivileged: true}
	allowed, err := d.Authorize(entry, "registry.local/kube-proxy:v1", true, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("Authorize returned unexpected error: %v", err)
	}
	if allowed {
		t.Fatalf("a wildcard whitelist entry must not authorize anything")
	}
}

func TestParseSeccompStatusFilterMode(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	mode, err := d.ParseSeccompStatus("Seccomp:\t2\n")
	if err != nil {
		t.Fatalf("ParseSeccompStatus returned unexpected error: %v", err)
	}
	if mode != task.SeccompFilter {
		t.Fatalf("Seccomp 2 must map to SeccompFilter, got %v", mode)
	}
}

func TestRuleAppliesCgroupPrefixBoundary(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	rule := task.Rule{ID: "escape.cgroup", CgroupPrefix: "/kubepods/burstable"}
	near, err := d.RuleApplies(rule, "/kubepods/burstable2/pod123/ctr")
	if err != nil {
		t.Fatalf("RuleApplies returned unexpected error: %v", err)
	}
	if near {
		t.Fatalf("a sibling cgroup sharing the textual prefix must not match")
	}
	inside, err := d.RuleApplies(rule, "/kubepods/burstable/pod123/ctr")
	if err != nil {
		t.Fatalf("RuleApplies returned unexpected error: %v", err)
	}
	if !inside {
		t.Fatalf("a real child cgroup must match the prefix rule")
	}
}
