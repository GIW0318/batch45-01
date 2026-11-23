package task

import (
	"testing"

	impl "gsb/batch45-game-a-04"
)

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestEnqueueRejectsDuplicateGuild(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	g := impl.Guild{ID: "red", Power: 1000, Server: "s1"}
	if err := m.Enqueue(g); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	if err := m.Enqueue(g); err != nil {
		t.Fatalf("duplicate enqueue should be ignored, got error: %v", err)
	}
	if got := m.PoolSize(); got != 1 {
		t.Fatalf("pool size = %d, want 1 after enqueuing the same guild twice", got)
	}
}

func TestPairUpAcceptsComparablePower(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	pair, err := m.PairUp(
		impl.Guild{ID: "red", Power: 1000},
		impl.Guild{ID: "blue", Power: 1100},
	)
	if err != nil {
		t.Fatalf("pairing guilds with 1.1x power difference failed: %v", err)
	}
	if pair.A != "red" || pair.B != "blue" {
		t.Fatalf("pair = %+v, want red vs blue", pair)
	}
}

func TestPowerRatioRejectsTripleDifference(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	got := m.PowerRatio(impl.Guild{Power: 1000}, impl.Guild{Power: 3000})
	if !almostEqual(got, 3.0) {
		t.Fatalf("power ratio of 1000 vs 3000 = %v, want 3", got)
	}
}

func TestPercentilePowerUsesTopDecile(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	guilds := make([]impl.Guild, 0, 10)
	for i := 1; i <= 10; i++ {
		guilds = append(guilds, impl.Guild{ID: "g", Power: i * 100})
	}
	if got := m.PercentilePower(guilds, 90); got != 900 {
		t.Fatalf("p90 power = %d, want 900 (percentile, not the average 550)", got)
	}
}

func TestLossStreakProtectionRaisesPower(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	if got := m.LossProtection(1000, 3); got != 1150 {
		t.Fatalf("protected power = %d, want 1150 for a 3 loss streak", got)
	}
}

func TestMatchedGuildsLeaveThePool(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	if err := m.Enqueue(impl.Guild{ID: "red", Power: 1000}); err != nil {
		t.Fatalf("enqueue red failed: %v", err)
	}
	if err := m.Enqueue(impl.Guild{ID: "blue", Power: 1050}); err != nil {
		t.Fatalf("enqueue blue failed: %v", err)
	}
	if _, err := m.PairUp(impl.Guild{ID: "red", Power: 1000}, impl.Guild{ID: "blue", Power: 1050}); err != nil {
		t.Fatalf("pairing failed: %v", err)
	}
	if got := m.PoolSize(); got != 0 {
		t.Fatalf("pool size after matching = %d, want 0", got)
	}
}

func TestCrossServerGuildsGroupedByLatency(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	got := m.CrossServerGroups([]impl.Guild{
		{ID: "a", Server: "cn-east"},
		{ID: "b", Server: "cn-east"},
		{ID: "c", Server: "eu-west"},
		{ID: "d", Server: "us-west"},
	})
	if got != 3 {
		t.Fatalf("latency groups = %d, want 3 distinct servers", got)
	}
}

func TestTimeoutRelaxationIsPartial(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	got := m.TimeoutRatio(120)
	if !almostEqual(got, 1.2) {
		t.Fatalf("timeout ratio after 120s = %v, want a partial relaxation of 1.2x", got)
	}
	if got >= 10 {
		t.Fatalf("timeout must not drop every condition, got ratio %v", got)
	}
}

func TestSmurfGuildIsBlocked(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	if !m.SmurfBlocked(85, 40) {
		t.Fatalf("an 85%% win rate guild with 40 battles must be treated as a smurf")
	}
	if m.SmurfBlocked(50, 40) {
		t.Fatalf("a 50%% win rate guild must not be treated as a smurf")
	}
}

func TestSeedIsStableAndNonZero(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	first := m.Seed("red|blue")
	if first == 0 {
		t.Fatalf("pair seed must not be zero")
	}
	if second := m.Seed("red|blue"); second != first {
		t.Fatalf("pair seed is not reproducible: %d vs %d", first, second)
	}
}

func TestLockPairRejectsConcurrentDuplicate(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	if !m.LockPair("red", "blue") {
		t.Fatalf("first lock of a pair must succeed")
	}
	if m.LockPair("blue", "red") {
		t.Fatalf("the same pair must not be locked twice")
	}
}

func TestSeasonDecayReducesScore(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	if got := m.Decay(2000, 3); got != 1700 {
		t.Fatalf("score after 3 weeks of decay = %d, want 1700", got)
	}
}

func TestRejectPenaltyAccumulates(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	if got := m.RejectPenalty(2); got != 20 {
		t.Fatalf("penalty after 2 rejects = %d, want 20", got)
	}
}

func TestCanPairFairGuilds(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.3)
	if !m.CanPair(impl.Guild{Power: 1000}, impl.Guild{Power: 1100}) {
		t.Fatalf("guilds within the 1.3 ratio must be pairable")
	}
	if m.CanPair(impl.Guild{Power: 1000}, impl.Guild{Power: 3000}) {
		t.Fatalf("guilds three times apart must not be pairable")
	}
}

func TestMaxRatioHonoursConfiguration(t *testing.T) {
	m := impl.NewGuildWarMatcher(1.25)
	if got := m.MaxRatio(); !almostEqual(got, 1.25) {
		t.Fatalf("max ratio = %v, want the configured 1.25", got)
	}
}
