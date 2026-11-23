package selftest

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	task "gsb/batch45-safety-b-14"
)

// Each MacsecGuard is one port. A CA is modeled by making every port join
// every reachable guard with the same participant view.
type port struct {
	sci  string
	prio int
	g    *task.MacsecGuard
}

func newPort(t *testing.T, sci string, prio int) *port {
	t.Helper()
	g, err := task.NewMacsecGuard(make([]byte, 32), task.SuiteGCM128)
	if err != nil {
		t.Fatalf("new guard %s: %v", sci, err)
	}
	return &port{sci: sci, prio: prio, g: g}
}

func (p *port) joinAll(now time.Time, others ...*port) {
	for _, q := range append(others, p) {
		_ = p.g.Join(task.Participant{
			SCI:      q.sci,
			Priority: q.prio,
			Suite:    task.SuiteGCM128,
			LastSeen: now,
		})
	}
}

// TestPartitionConvergence verifies that after a split-brain partition heals,
// both ends converge on the same key server (highest priority, then smallest
// SCI), that SAK replacement leaves only one AN usable, and that every
// installation is recorded in the audit trail.
func TestPartitionConvergence(t *testing.T) {
	now := time.Unix(1700000000, 0)

	a := newPort(t, "00:11:22:33:44:55:01", 100)
	b := newPort(t, "00:11:22:33:44:55:02", 200)

	// Before the partition both ports share the full participant view and
	// agree that B (higher priority) is the key server.
	a.joinAll(now, b)
	b.joinAll(now, a)

	winnerA, err := a.g.ElectKeyServer()
	if err != nil || winnerA != b.sci {
		t.Fatalf("pre-partition winner on A = %q, %v; want %s", winnerA, err, b.sci)
	}
	winnerB, err := b.g.ElectKeyServer()
	if err != nil || winnerB != b.sci {
		t.Fatalf("pre-partition winner on B = %q, %v; want %s", winnerB, err, b.sci)
	}

	// Partition: each side only sees itself and installs its own SAK. This
	// is the "two SAKs coexist" failure window.
	for _, p := range []*port{a, b} {
		if err := p.g.Leave(other(p, a, b).sci); err != nil {
			t.Fatalf("leave during partition: %v", err)
		}
		if w, err := p.g.ElectKeyServer(); err != nil || w != p.sci {
			t.Fatalf("partition winner = %q, %v; want self %s", w, err, p.sci)
		}
		if err := p.g.InstallSAK("partition-"+p.sci, make([]byte, 16), 0); err != nil {
			t.Fatalf("partition install: %v", err)
		}
	}

	// Heal: full view is re-exchanged. Both ends must converge on B.
	a.joinAll(now.Add(time.Minute), b)
	b.joinAll(now.Add(time.Minute), a)
	if w, err := a.g.ElectKeyServer(); err != nil || w != b.sci {
		t.Fatalf("post-heal winner on A = %q, %v; want %s", w, err, b.sci)
	}
	if w, err := b.g.ElectKeyServer(); err != nil || w != b.sci {
		t.Fatalf("post-heal winner on B = %q, %v; want %s", w, err, b.sci)
	}

	// B installs a fresh SAK on AN1; afterwards AN0 must be dead and the
	// per-SA PN counter must restart at 1.
	if err := b.g.InstallSAK("healed", make([]byte, 16), 1); err != nil {
		t.Fatalf("healed install: %v", err)
	}
	if _, err := b.g.NextPN(0); !errors.Is(err, task.ErrInactiveAN) {
		t.Fatalf("old AN still carries traffic: %v", err)
	}
	pn, err := b.g.NextPN(1)
	if err != nil || pn != 1 {
		t.Fatalf("new AN first PN = %d, %v; want 1, nil", pn, err)
	}

	trail := b.g.AuditTrail()
	if len(trail) != 2 {
		t.Fatalf("audit entries = %d, want 2 (partition + healed)", len(trail))
	}
	if trail[0].KeyID != "partition-"+b.sci || trail[1].KeyID != "healed" {
		t.Fatalf("audit trail = %+v", trail)
	}
	if !trail[1].Installed.After(trail[0].Installed) {
		t.Fatalf("audit timestamps not monotonic: %+v", trail)
	}
}

// TestTieConvergenceBySCI checks the equal-priority branch: after the
// partition heals both ends pick the smallest SCI.
func TestTieConvergenceBySCI(t *testing.T) {
	now := time.Unix(1700000000, 0)
	a := newPort(t, "00:11:22:33:44:55:0a", 100)
	b := newPort(t, "00:11:22:33:44:55:05", 100)
	a.joinAll(now, b)
	b.joinAll(now, a)

	for _, p := range []*port{a, b} {
		w, err := p.g.ElectKeyServer()
		if err != nil || w != b.sci {
			t.Fatalf("tie winner on %s = %q, %v; want smallest SCI %s", p.sci, w, err, b.sci)
		}
	}

	// The ICV derived from the installed SAK must verify on the receiver,
	// proving both ends use one key rather than two divergent SAKs.
	sak := []byte("0123456789abcdef")
	if err := b.g.InstallSAK("shared", sak, 0); err != nil {
		t.Fatalf("install: %v", err)
	}
	frame := []byte("converged-frame")
	sum := sha256.Sum256(append(append([]byte{}, sak...), frame...))
	if err := b.g.VerifyICV(0, frame, sum[:16]); err != nil {
		t.Fatalf("ICV verify after convergence: %v", err)
	}
}

// TestRestartAndAuditSurvival verifies CAK-loss semantics and that the
// installation trail survives the restart.
func TestRestartAndAuditSurvival(t *testing.T) {
	g := newPort(t, "00:11:22:33:44:55:01", 1).g
	if err := g.InstallSAK("k0", make([]byte, 16), 0); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := g.InstallSAK("k1", make([]byte, 16), 1); err != nil {
		t.Fatalf("install 2: %v", err)
	}

	if err := g.Restart(nil); !errors.Is(err, task.ErrCAKMissing) {
		t.Fatalf("restart without CAK: %v", err)
	}
	for _, an := range []int{0, 1} {
		if _, err := g.NextPN(an); !errors.Is(err, task.ErrCAKMissing) {
			t.Fatalf("AN %d usable after CAK loss: %v", an, err)
		}
		if err := g.VerifyICV(an, []byte("f"), make([]byte, 16)); !errors.Is(err, task.ErrCAKMissing) {
			t.Fatalf("AN %d ICV check after CAK loss: %v", an, err)
		}
	}
	if _, err := g.ActiveSAK(); !errors.Is(err, task.ErrCAKMissing) {
		t.Fatalf("active SAK after CAK loss: %v", err)
	}

	// The audit trail must remain queryable across the restart.
	if trail := g.AuditTrail(); len(trail) != 2 || trail[1].KeyID != "k1" {
		t.Fatalf("audit trail lost across restart: %+v", trail)
	}

	if err := g.Restart(make([]byte, 32)); err != nil {
		t.Fatalf("restart with CAK: %v", err)
	}
	sak, err := g.ActiveSAK()
	if err != nil || sak.KeyID != "k1" || sak.AN != 1 {
		t.Fatalf("active SAK after recovery = %+v, %v", sak, err)
	}
}

func other(target *port, all ...*port) *port {
	for _, p := range all {
		if p != target {
			return p
		}
	}
	return nil
}
