package task

import (
	"strings"
	"sync"
	"testing"

	. "gsb/batch45-safety-c-15"
)

func TestCheckConnVersionDeniesStaleConnection(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.CheckConnVersion(1, 2)
	if err != nil {
		t.Fatalf("CheckConnVersion: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("connection on the old password version, want deny, got %v", got)
	}
}

func TestCheckConnVersionAllowsCurrentConnection(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.CheckConnVersion(2, 2)
	if err != nil {
		t.Fatalf("CheckConnVersion: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("connection on the active version, want allow, got %v", got)
	}
}

func TestRetireOldSecretDeniesBeforeDrain(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.RetireOldSecret(false)
	if err != nil {
		t.Fatalf("RetireOldSecret: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("old password must stay until the drain finishes, got %v", got)
	}
}

func TestRetireOldSecretAllowsAfterDrain(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.RetireOldSecret(true)
	if err != nil {
		t.Fatalf("RetireOldSecret: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("drain finished, old password may be retired, got %v", got)
	}
}

func TestRebuildDSNUsesRotatedPassword(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.RebuildDSN("postgres://app@db:5432/prod", "app", "pw-v2")
	if err != nil {
		t.Fatalf("RebuildDSN: %v", err)
	}
	if !strings.Contains(got, "pw-v2") {
		t.Fatalf("reconnect DSN must carry the rotated password, got %q", got)
	}
}

func TestConcurrentRotateKeepsSingleVersion(t *testing.T) {
	r := &Rotator{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Rotate(1, "pw-v1")
		}()
	}
	wg.Wait()
	if r.Active.Version != 1 || r.Version != 1 {
		t.Fatalf("concurrent rotation must converge on one version, got active=%d version=%d", r.Active.Version, r.Version)
	}
}

func TestCheckReplicaConsistencyDeniesDivergent(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.CheckReplicaConsistency([]int{2, 2, 1})
	if err != nil {
		t.Fatalf("CheckReplicaConsistency: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("replicas disagree on the password version, want deny, got %v", got)
	}
}

func TestCheckReplicaConsistencyAllowsUniform(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.CheckReplicaConsistency([]int{2, 2, 2})
	if err != nil {
		t.Fatalf("CheckReplicaConsistency: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("all replicas on version 2, want allow, got %v", got)
	}
}

func TestRollbackRestoresPreviousVersion(t *testing.T) {
	g := &SecretRotationPool{}
	previous := Secret{Version: 1, Value: "pw-v1"}
	current := Secret{Version: 2, Value: "pw-v2"}
	got, err := g.Rollback(previous, current, false)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("failed validation must roll back to version 1, got %d", got.Version)
	}
}

func TestLockSecretMarksLocked(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.LockSecret(Secret{Version: 1, Value: "pw-v1"}, true)
	if err != nil {
		t.Fatalf("LockSecret: %v", err)
	}
	if !got.Locked {
		t.Fatalf("secret must be marked as locked in memory")
	}
}

func TestRedactDSNRemovesPassword(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.RedactDSN("postgres://app:pw-v2@db:5432/prod")
	if err != nil {
		t.Fatalf("RedactDSN: %v", err)
	}
	if strings.Contains(got, "pw-v2") {
		t.Fatalf("DSN logged with the password: %q", got)
	}
}

func TestApplyPropagationDelayDeniesBeforeAllAcks(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.ApplyPropagationDelay(2, 3)
	if err != nil {
		t.Fatalf("ApplyPropagationDelay: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("not every replica acknowledged the new password, want deny, got %v", got)
	}
}

func TestDrainPoolKeepsOnlyNewVersionConns(t *testing.T) {
	g := &SecretRotationPool{}
	conns := []Conn{
		{ID: 1, SecretVersion: 1},
		{ID: 2, SecretVersion: 2},
		{ID: 3, SecretVersion: 2},
	}
	got, err := g.DrainPool(conns, 2)
	if err != nil {
		t.Fatalf("DrainPool: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("only connections on the active version survive the drain, got %d", len(got))
	}
}

func TestValidateNewSecretDeniesInvalid(t *testing.T) {
	g := &SecretRotationPool{}
	got, err := g.ValidateNewSecret(false)
	if err != nil {
		t.Fatalf("ValidateNewSecret: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("new password failed validation, want deny, got %v", got)
	}
}

func TestLoadPersistedPicksHighestVersion(t *testing.T) {
	g := &SecretRotationPool{}
	saved := []Secret{
		{Version: 3, Value: "pw-v3"},
		{Version: 1, Value: "pw-v1"},
		{Version: 2, Value: "pw-v2"},
	}
	got, err := g.LoadPersisted(saved)
	if err != nil {
		t.Fatalf("LoadPersisted: %v", err)
	}
	if got.Version != 3 {
		t.Fatalf("restart must load the newest password version, got %d", got.Version)
	}
}
