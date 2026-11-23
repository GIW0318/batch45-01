package task

import (
	"strings"
	"testing"
	"time"

	impl "gsb/batch45-game-a-15"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func base() time.Time {
	return time.Date(2026, 7, 1, 20, 0, 0, 0, time.UTC)
}

func idle(player, round, detector string, at time.Time) impl.Signal {
	return impl.Signal{PlayerID: player, RoundID: round, DetectorID: detector, At: at}
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestIdleSignalsIncrementCounter(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	for i := 0; i < 3; i++ {
		must(t, g.Report(idle("p1", "r1", "d1", base().Add(time.Duration(i)*time.Second))))
	}
	if got := g.IdleCount("p1", "r1"); got != 3 {
		t.Fatalf("idle count = %d, want 3", got)
	}
}

func TestActiveSignalResetsIdleCounter(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.Report(idle("p1", "r1", "d1", base())))
	must(t, g.Report(idle("p1", "r1", "d1", base().Add(time.Second))))
	active := impl.Signal{PlayerID: "p1", RoundID: "r1", DetectorID: "d1", DamageDealt: 120, At: base().Add(2 * time.Second)}
	must(t, g.Report(active))
	if got := g.IdleCount("p1", "r1"); got != 0 {
		t.Fatalf("idle count = %d after a damage event, want 0", got)
	}
}

func TestHealerStandingStillIsNotAfk(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 1))
	for i := 0; i < 3; i++ {
		heal := impl.Signal{PlayerID: "healer", RoundID: "r1", DetectorID: "d1", HealingDone: 500, At: base().Add(time.Duration(i) * time.Second)}
		must(t, g.Report(heal))
	}
	if got := g.IdleCount("healer", "r1"); got != 0 {
		t.Fatalf("idle count = %d for a healing player, want 0", got)
	}
	afk, err := g.Evaluate("healer", "r1")
	must(t, err)
	if afk {
		t.Fatalf("a healer who stands still but heals must not be flagged")
	}
}

func TestVehicleRiderIsNotAfk(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 1))
	for i := 0; i < 2; i++ {
		sig := impl.Signal{PlayerID: "driver", RoundID: "r1", DetectorID: "d1", OnVehicle: true, At: base().Add(time.Duration(i) * time.Second)}
		must(t, g.Report(sig))
	}
	if got := g.IdleCount("driver", "r1"); got != 0 {
		t.Fatalf("idle count = %d while riding a vehicle, want 0", got)
	}
}

func TestSimultaneousDetectorsCountedOnce(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	same := base()
	for _, detector := range []string{"d1", "d2", "d3"} {
		must(t, g.Report(idle("p1", "r1", detector, same)))
	}
	if got := g.IdleCount("p1", "r1"); got != 1 {
		t.Fatalf("idle count = %d for one instant reported by 3 detectors, want 1", got)
	}
}

func TestThresholdVariesByBattlefieldType(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.StartRound("r2", "battleground"))
	must(t, g.SetIdleThreshold("arena", 2))
	must(t, g.SetIdleThreshold("battleground", 5))
	for i := 0; i < 3; i++ {
		must(t, g.Report(idle("p1", "r1", "d1", base().Add(time.Duration(i)*time.Second))))
		must(t, g.Report(idle("p1", "r2", "d1", base().Add(time.Duration(i)*time.Second))))
	}
	arenaAfk, err := g.Evaluate("p1", "r1")
	must(t, err)
	if !arenaAfk {
		t.Fatalf("3 idle signals must reach the arena threshold of 2")
	}
	bgAfk, err := g.Evaluate("p1", "r2")
	must(t, err)
	if bgAfk {
		t.Fatalf("3 idle signals must stay below the battleground threshold of 5")
	}
}

func TestEvaluateMarksAfkAtThreshold(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 2))
	must(t, g.Report(idle("p1", "r1", "d1", base())))
	must(t, g.Report(idle("p1", "r1", "d1", base().Add(time.Second))))
	afk, err := g.Evaluate("p1", "r1")
	must(t, err)
	if !afk {
		t.Fatalf("2 idle signals must mark the player AFK at threshold 2")
	}
}

func TestNewRoundStartsFreshCounter(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	for i := 0; i < 3; i++ {
		must(t, g.Report(idle("p1", "r1", "d1", base().Add(time.Duration(i)*time.Second))))
	}
	must(t, g.StartRound("r2", "arena"))
	if got := g.IdleCount("p1", "r2"); got != 0 {
		t.Fatalf("new round idle count = %d, want 0", got)
	}
	if got := g.IdleCount("p1", "r1"); got != 3 {
		t.Fatalf("old round idle count = %d, want the 3 recorded signals", got)
	}
}

func TestRemoteSyncAddsIdleCounts(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.MergeRemote("r1", "p1", "remote-detector", 2, base()))
	if got := g.IdleCount("p1", "r1"); got != 2 {
		t.Fatalf("idle count = %d after remote sync, want 2", got)
	}
}

func TestPenaltyReducesScoreImmediately(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 2))
	must(t, g.SetStartScore("p1", 100))
	must(t, g.Report(idle("p1", "r1", "d1", base())))
	must(t, g.Report(idle("p1", "r1", "d1", base().Add(time.Second))))
	penalty, err := g.ApplyPenalty("p1", "r1")
	must(t, err)
	if penalty <= 0 {
		t.Fatalf("penalty = %d, want a positive deduction", penalty)
	}
	if got := g.Score("p1"); got != 100-penalty {
		t.Fatalf("score = %d, want 100-%d applied before settlement", got, penalty)
	}
}

func TestAppealBlocksPenalty(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 1))
	must(t, g.SetStartScore("p1", 100))
	must(t, g.Report(idle("p1", "r1", "d1", base())))
	must(t, g.FileAppeal("p1", "r1"))
	if _, err := g.ApplyPenalty("p1", "r1"); err == nil {
		t.Fatalf("penalty must be frozen while an appeal is open")
	}
	if got := g.Score("p1"); got != 100 {
		t.Fatalf("score = %d during appeal, want 100", got)
	}
}

func TestAuditTrailRecordsPenalty(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 1))
	must(t, g.SetStartScore("p1", 100))
	must(t, g.Report(idle("p1", "r1", "d1", base())))
	_, err := g.ApplyPenalty("p1", "r1")
	must(t, err)
	log := strings.Join(g.AuditTrail("p1"), "\n")
	if log == "" {
		t.Fatalf("audit trail is empty after a penalty")
	}
	if !strings.Contains(log, "penalty") {
		t.Fatalf("audit trail %q does not record the penalty", log)
	}
}

func TestSnapshotRestorePreservesCounters(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	for i := 0; i < 3; i++ {
		must(t, g.Report(idle("p1", "r1", "d1", base().Add(time.Duration(i)*time.Second))))
	}
	data, err := g.Snapshot()
	must(t, err)
	restored := impl.NewAfkGuard()
	must(t, restored.Restore(data))
	if got := restored.IdleCount("p1", "r1"); got != 3 {
		t.Fatalf("restored idle count = %d, want 3", got)
	}
}

func TestTeamAfkPenalizesEveryMember(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 2))
	must(t, g.RegisterTeam("team-1", []string{"p1", "p2", "p3"}))
	for _, player := range []string{"p1", "p2", "p3"} {
		must(t, g.SetStartScore(player, 100))
		for i := 0; i < 3; i++ {
			must(t, g.Report(idle(player, "r1", "d1", base().Add(time.Duration(i)*time.Second))))
		}
	}
	penalized, err := g.PenalizeTeam("team-1", "r1")
	must(t, err)
	if len(penalized) != 3 {
		t.Fatalf("penalized players = %v, want all 3 team members", penalized)
	}
	for _, player := range []string{"p1", "p2", "p3"} {
		if !hasString(penalized, player) {
			t.Fatalf("team member %s was not penalized: %v", player, penalized)
		}
		if got := g.Score(player); got >= 100 {
			t.Fatalf("%s score = %d, want a penalty applied to every member", player, got)
		}
	}
}

func TestPenaltyAppliedOnlyOnce(t *testing.T) {
	g := impl.NewAfkGuard()
	must(t, g.StartRound("r1", "arena"))
	must(t, g.SetIdleThreshold("arena", 1))
	must(t, g.SetStartScore("p1", 100))
	must(t, g.Report(idle("p1", "r1", "d1", base())))
	first, err := g.ApplyPenalty("p1", "r1")
	must(t, err)
	if first <= 0 {
		t.Fatalf("first penalty = %d, want a positive amount", first)
	}
	second, err := g.ApplyPenalty("p1", "r1")
	must(t, err)
	if second != 0 {
		t.Fatalf("second penalty = %d, want 0", second)
	}
	if got := g.Score("p1"); got != 100-first {
		t.Fatalf("score = %d, want the single deduction 100-%d", got, first)
	}
}
