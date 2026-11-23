package task

import (
	"testing"

	impl "gsb/batch45-game-a-07"
)

func TestPoolKeyIsPerAccountAndPool(t *testing.T) {
	p := impl.NewPityLedger(90)
	if got := p.PoolKey("acct-1", "limited"); got != "acct-1:limited" {
		t.Fatalf("pool key = %q, want acct-1:limited", got)
	}
}

func TestPityCountsPerPoolNotPerAccount(t *testing.T) {
	p := impl.NewPityLedger(90)
	if got := p.OnDraw("acct-1", "limited", 5); got != 5 {
		t.Fatalf("pity after 5 draws = %d, want 5", got)
	}
	if got := p.Pity("acct-1", "standard"); got != 0 {
		t.Fatalf("standard pool pity = %d, want an independent 0", got)
	}
	if got := p.Pity("acct-1", "limited"); got != 5 {
		t.Fatalf("limited pool pity = %d, want 5", got)
	}
}

func TestSingleDrawIncrementsByOne(t *testing.T) {
	p := impl.NewPityLedger(90)
	if got := p.OnDraw("acct-1", "limited", 1); got != 1 {
		t.Fatalf("pity after a single draw = %d, want 1", got)
	}
}

func TestTenPullIncrementsByTen(t *testing.T) {
	p := impl.NewPityLedger(90)
	if got := p.OnDraw("acct-1", "limited", 10); got != 10 {
		t.Fatalf("pity after a ten pull = %d, want 10", got)
	}
}

func TestPityTriggersAtThreshold(t *testing.T) {
	p := impl.NewPityLedger(90)
	p.OnDraw("acct-1", "limited", 90)
	if !p.Triggered("acct-1", "limited") {
		t.Fatalf("pity must trigger once the threshold of 90 draws is reached")
	}
}

func TestHitResetsCounter(t *testing.T) {
	p := impl.NewPityLedger(90)
	p.OnDraw("acct-1", "limited", 30)
	if got := p.OnHit("acct-1", "limited"); got != 30 {
		t.Fatalf("hit consumed counter %d, want the 30 draws banked before the hit", got)
	}
	if got := p.Pity("acct-1", "limited"); got != 0 {
		t.Fatalf("pity after a hit = %d, want 0", got)
	}
	p.OnDraw("acct-1", "limited", 3)
	if got := p.Pity("acct-1", "limited"); got != 3 {
		t.Fatalf("pity after 3 more draws = %d, want 3", got)
	}
}

func TestCarryOverKeepsCounterAcrossVersion(t *testing.T) {
	p := impl.NewPityLedger(90)
	p.OnDraw("acct-1", "limited", 40)
	if got := p.CarryOver("acct-1", "limited", "v2"); got != 40 {
		t.Fatalf("carried over pity = %d, want the 40 draws of the old version", got)
	}
}

func TestRateUpEndReturnsBankedCounter(t *testing.T) {
	p := impl.NewPityLedger(90)
	p.OnDraw("acct-1", "limited", 12)
	if got := p.RateUpEnd("acct-1", "limited"); got != 12 {
		t.Fatalf("banked pity after rate up = %d, want 12", got)
	}
}

func TestConcurrentDrawSerialisesCounter(t *testing.T) {
	p := impl.NewPityLedger(90)
	if got := p.ConcurrentDraw("acct-1", "limited", 8); got != 8 {
		t.Fatalf("counter after 8 concurrent draws = %d, want 8", got)
	}
}

func TestSeedIsReproducible(t *testing.T) {
	p := impl.NewPityLedger(90)
	first := p.Seed("session-a")
	if first == 0 {
		t.Fatalf("draw seed must not be zero")
	}
	if second := p.Seed("session-a"); second != first {
		t.Fatalf("draw seed not reproducible: %d vs %d", first, second)
	}
}

func TestSnapshotPersistsCounters(t *testing.T) {
	p := impl.NewPityLedger(90)
	p.OnDraw("acct-1", "limited", 7)
	snap := p.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d entries, want 1", len(snap))
	}
}

func TestRestoreReloadsCounters(t *testing.T) {
	p := impl.NewPityLedger(90)
	p.Restore(map[string]int{"acct-1:limited": 7})
	if got := p.Pity("acct-1", "limited"); got != 7 {
		t.Fatalf("pity after restore = %d, want 7", got)
	}
}

func TestDeviceGuardRejectsSecondDevice(t *testing.T) {
	p := impl.NewPityLedger(90)
	if !p.DeviceGuard("phone-1") {
		t.Fatalf("first device must be accepted")
	}
	if p.DeviceGuard("tablet-2") {
		t.Fatalf("a second device must not double count the pity")
	}
}

func TestReloadKeepsCounterConsistent(t *testing.T) {
	p := impl.NewPityLedger(90)
	p.OnDraw("acct-1", "limited", 50)
	if got := p.Reload("acct-1", "limited", 100); got != 50 {
		t.Fatalf("counter after hot reload = %d, want the unchanged 50", got)
	}
	if got := p.Threshold(); got != 100 {
		t.Fatalf("threshold after hot reload = %d, want 100", got)
	}
}

func TestPityRewardNeverReplacedByNormalDrop(t *testing.T) {
	p := impl.NewPityLedger(90)
	if got := p.ResolveReward(true, "common-sword"); got != "pity_reward" {
		t.Fatalf("reward = %q, want pity_reward when pity triggered", got)
	}
	if got := p.ResolveReward(false, "common-sword"); got != "common-sword" {
		t.Fatalf("reward = %q, want the normal drop when pity did not trigger", got)
	}
}
