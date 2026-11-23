package task

import (
	"testing"

	impl "gsb/batch45-game-a-05"
)

func TestLockKeyIncludesDifficultyAndSeason(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	got := g.LockKey("char-1", "raid-9", "heroic", "s3")
	if got != "char-1:raid-9:heroic:s3" {
		t.Fatalf("lock key = %q, want char-1:raid-9:heroic:s3", got)
	}
	if other := g.LockKey("char-1", "raid-9", "normal", "s3"); other == got {
		t.Fatalf("different difficulty must produce a different lock key")
	}
}

func TestCrossServerCharacterMappingIsStable(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	first := g.MapCharacter("cross:char-7")
	if first != "char-7" {
		t.Fatalf("cross server char mapped to %q, want char-7", first)
	}
	if second := g.MapCharacter("cross:char-7"); second != first {
		t.Fatalf("mapping is not stable: %q then %q", first, second)
	}
}

func TestLockInMemoryIsVisible(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.MarkLocked(impl.Lockout{CharID: "char-1", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000})
	if !g.Locked("char-1", "raid-9", "heroic", "s3") {
		t.Fatalf("locked character was not reported as locked")
	}
	if g.Locked("char-1", "raid-9", "normal", "s3") {
		t.Fatalf("lockout of heroic must not lock the normal difficulty")
	}
}

func TestPersistReturnsEveryLock(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.MarkLocked(impl.Lockout{CharID: "char-1", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000})
	g.MarkLocked(impl.Lockout{CharID: "char-2", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000})
	if got := len(g.Persist()); got != 2 {
		t.Fatalf("persisted %d lockouts, want 2", got)
	}
}

func TestRestoreReloadsLocksAfterRestart(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.Restore(map[string]impl.Lockout{
		"char-1:raid-9:heroic:s3": {CharID: "char-1", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000},
	})
	if !g.Locked("char-1", "raid-9", "heroic", "s3") {
		t.Fatalf("lockout lost after restore from storage")
	}
}

func TestFirstClearIsSeparateFromNormalReward(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	if !g.MarkFirstClear("char-1", "raid-9") {
		t.Fatalf("first clear must be available the first time")
	}
	if g.MarkFirstClear("char-1", "raid-9") {
		t.Fatalf("first clear flag was already consumed, second grant must be refused")
	}
}

func TestClaimFirstClearIsAtomic(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	claimed, err := g.ClaimFirstClear("char-1", "raid-9")
	if err != nil {
		t.Fatalf("claiming first clear failed: %v", err)
	}
	if !claimed {
		t.Fatalf("first claim must succeed")
	}
	again, err := g.ClaimFirstClear("char-1", "raid-9")
	if err != nil {
		t.Fatalf("second claim returned error: %v", err)
	}
	if again {
		t.Fatalf("first clear bonus must only be granted once per week")
	}
}

func TestCanEnterPartyRequiresAllMembersUnlocked(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.MarkLocked(impl.Lockout{CharID: "char-2", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000})
	ok, err := g.CanEnterParty([]string{"char-1", "char-2"}, "raid-9", "heroic", "s3")
	if err != nil {
		t.Fatalf("party check returned error: %v", err)
	}
	if ok {
		t.Fatalf("party with one locked member must be refused")
	}
	ok, err = g.CanEnterParty([]string{"char-1"}, "raid-9", "heroic", "s3")
	if err != nil {
		t.Fatalf("party check returned error: %v", err)
	}
	if !ok {
		t.Fatalf("party without lockouts must be allowed")
	}
}

func TestNextResetFollowsServerTimezone(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	if got := g.NextReset(1700000000, 8); got != 1700064000 {
		t.Fatalf("next reset = %d, want 1700064000 (next local midnight at utc+8)", got)
	}
}

func TestClaimInstanceIsExclusive(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	first, err := g.ClaimInstance([]string{"char-1", "char-2"})
	if err != nil {
		t.Fatalf("claiming instance failed: %v", err)
	}
	if first == "" {
		t.Fatalf("claimed instance id must not be empty")
	}
	second, err := g.ClaimInstance([]string{"char-1", "char-3"})
	if err != nil {
		t.Fatalf("second claim returned error: %v", err)
	}
	if second == first {
		t.Fatalf("a character in a running instance must not get a second instance")
	}
}

func TestAuditResetRecordsActor(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	entry := g.AuditReset("gm-alice", "char-1")
	if entry.Actor != "gm-alice" {
		t.Fatalf("audit actor = %q, want gm-alice", entry.Actor)
	}
	if entry.CharID != "char-1" {
		t.Fatalf("audit character = %q, want char-1", entry.CharID)
	}
}

func TestTransferCDMovesLockouts(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.MarkLocked(impl.Lockout{CharID: "char-old", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000})
	if moved := g.TransferCD("char-old", "char-new"); moved != 1 {
		t.Fatalf("moved %d lockouts, want 1", moved)
	}
	if !g.Locked("char-new", "raid-9", "heroic", "s3") {
		t.Fatalf("lockout did not follow the character transfer")
	}
}

func TestPurgeExpiredLockouts(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.MarkLocked(impl.Lockout{CharID: "char-1", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 100})
	g.MarkLocked(impl.Lockout{CharID: "char-2", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 900})
	if got := g.PurgeExpired(500); got != 1 {
		t.Fatalf("purged %d lockouts, want 1 expired lockout", got)
	}
}

func TestLeavingRaidKeepsLockout(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.MarkLocked(impl.Lockout{CharID: "char-1", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000})
	g.Leave("char-1")
	if !g.Locked("char-1", "raid-9", "heroic", "s3") {
		t.Fatalf("leaving the raid must not clear the lockout")
	}
}

func TestExpiryIsPersistedWithLock(t *testing.T) {
	g := impl.NewRaidLockoutGuard()
	g.MarkLocked(impl.Lockout{CharID: "char-1", RaidID: "raid-9", Difficulty: "heroic", Season: "s3", ExpiresAt: 1700064000})
	if got := g.ExpiresAt("char-1", "raid-9", "heroic", "s3"); got != 1700064000 {
		t.Fatalf("stored expiry = %d, want 1700064000", got)
	}
}
