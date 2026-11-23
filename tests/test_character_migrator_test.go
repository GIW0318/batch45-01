package task

import (
	"errors"
	"testing"

	impl "gsb/batch45-game-a-24"
)

func newChar(id, account, name, server string) impl.Character {
	return impl.Character{
		ID:           id,
		AccountID:    account,
		Name:         name,
		ServerID:     server,
		Level:        42,
		GuildID:      "guild-7",
		GuildRank:    "officer",
		Items:        []string{"sword", "potion"},
		Mail:         []string{"mail-1"},
		Achievements: map[string]int{"boss": 3, "quest": 2},
	}
}

func setup(t *testing.T) *impl.CharacterMigrator {
	t.Helper()
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Alice", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.AddCharacter(newChar("c-t", "acc-t", "Alice", "s2")); err != nil {
		t.Fatalf("AddCharacter(error): %v", err)
	}
	return m
}

func TestNameConflictCaseInsensitive(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Alice", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.AddCharacter(newChar("c-t", "acc-t", "ALICE", "s2")); err != nil {
		t.Fatalf("AddCharacter(target) error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	_, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"})
	if !errors.Is(err, impl.ErrNameConflict) {
		t.Fatalf("case-insensitive conflict error = %v, want ErrNameConflict", err)
	}
}

func TestExactNameConflictRejected(t *testing.T) {
	m := setup(t)
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	if _, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"}); !errors.Is(err, impl.ErrNameConflict) {
		t.Fatalf("exact name conflict error = %v, want ErrNameConflict", err)
	}
}

func TestFailedMigrationKeepsSourceState(t *testing.T) {
	m := setup(t)
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	if _, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"}); err == nil {
		t.Fatalf("conflicting migration unexpectedly succeeded")
	}
	got, ok := m.Character("c-a")
	if !ok {
		t.Fatalf("source character disappeared after failed migration")
	}
	if got.ServerID != "s1" {
		t.Fatalf("source character server = %q, want s1", got.ServerID)
	}
	if len(got.Items) != 2 {
		t.Fatalf("source items = %v, want intact", got.Items)
	}
}

func TestMigrationMovesCharacterAndKeepsLevel(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Bob", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	res, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("Migrate error: %v", err)
	}
	if res.NewID == "" || res.NewID == "c-a" {
		t.Fatalf("migrated character id = %q, want a new id", res.NewID)
	}
	moved, ok := m.Character(res.NewID)
	if !ok {
		t.Fatalf("character missing under new id %q", res.NewID)
	}
	if moved.ServerID != "s2" {
		t.Fatalf("migrated character server = %q, want s2", moved.ServerID)
	}
	if moved.Level != 42 {
		t.Fatalf("migrated level = %d, want 42", moved.Level)
	}
	if _, ok := m.Character("c-a"); ok {
		t.Fatalf("old character id still registered after migration")
	}
}

func TestFriendsMigratedBothDirections(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Ann", "s1")); err != nil {
		t.Fatalf("AddCharacter(A) error: %v", err)
	}
	if err := m.AddCharacter(newChar("c-b", "acc-b", "Ben", "s1")); err != nil {
		t.Fatalf("AddCharacter(B) error: %v", err)
	}
	if err := m.AddFriend("c-a", "c-b"); err != nil {
		t.Fatalf("AddFriend error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	resA, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k-a", CharacterID: "c-a", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("migrate A error: %v", err)
	}
	resB, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k-b", CharacterID: "c-b", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("migrate B error: %v", err)
	}
	if !hasFriend(m.FriendsOf(resA.NewID), resB.NewID) {
		t.Fatalf("A friends %v missing B %q", m.FriendsOf(resA.NewID), resB.NewID)
	}
	if !hasFriend(m.FriendsOf(resB.NewID), resA.NewID) {
		t.Fatalf("B friends %v missing A %q", m.FriendsOf(resB.NewID), resA.NewID)
	}
}

func TestReferencesUseNewCharacterID(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Ann", "s1")); err != nil {
		t.Fatalf("AddCharacter(A) error: %v", err)
	}
	if err := m.AddCharacter(newChar("c-b", "acc-b", "Ben", "s1")); err != nil {
		t.Fatalf("AddCharacter(B) error: %v", err)
	}
	if err := m.AddFriend("c-a", "c-b"); err != nil {
		t.Fatalf("AddFriend error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	resB, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k-b", CharacterID: "c-b", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("migrate B error: %v", err)
	}
	friends := m.FriendsOf("c-a")
	if hasFriend(friends, "c-b") {
		t.Fatalf("friend list still references the old id: %v", friends)
	}
	if !hasFriend(friends, resB.NewID) {
		t.Fatalf("friend list %v missing the new id %q", friends, resB.NewID)
	}
}

func TestGuildRankPreserved(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Cara", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	res, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("Migrate error: %v", err)
	}
	if got := m.GuildRankOf(res.NewID); got != "officer" {
		t.Fatalf("guild rank = %q, want officer", got)
	}
}

func TestItemsAndMailAttachmentsKept(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Dara", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	res, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("Migrate error: %v", err)
	}
	moved, ok := m.Character(res.NewID)
	if !ok {
		t.Fatalf("migrated character missing")
	}
	if len(moved.Items) != 2 {
		t.Fatalf("bag attachments = %v, want 2 items", moved.Items)
	}
	if len(moved.Mail) != 1 {
		t.Fatalf("mail attachments = %v, want 1 mail", moved.Mail)
	}
}

func TestAchievementProgressMerged(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Eve", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.SetProgress("acc-a", map[string]int{"boss": 5}); err != nil {
		t.Fatalf("SetProgress error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	if _, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"}); err != nil {
		t.Fatalf("Migrate error: %v", err)
	}
	progress := m.Progress("acc-a")
	if got := progress["boss"]; got != 5 {
		t.Fatalf("boss progress = %d, want max 5", got)
	}
	if got := progress["quest"]; got != 2 {
		t.Fatalf("quest progress = %d, want 2 (branch must be merged)", got)
	}
}

func TestLeaderboardNoDuplicates(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Finn", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.SetScore("c-a", 900); err != nil {
		t.Fatalf("SetScore error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	res, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("Migrate error: %v", err)
	}
	entries := m.Leaderboard("s2")
	count := 0
	for _, e := range entries {
		if e.Name == "Finn" || e.CharacterID == res.NewID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("leaderboard contains %d entries for the migrated character, want 1", count)
	}
}

func TestIdempotencyKeyReplay(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Gus", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	req := impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"}
	first, err := m.Migrate(req)
	if err != nil {
		t.Fatalf("first migrate error: %v", err)
	}
	second, err := m.Migrate(req)
	if err != nil {
		t.Fatalf("replayed migrate error: %v", err)
	}
	if !second.Replayed {
		t.Fatalf("second migrate with same key was not flagged replayed: %+v", second)
	}
	if second.NewID != first.NewID {
		t.Fatalf("replay produced new id %q, want %q", second.NewID, first.NewID)
	}
	if _, ok := m.Character(first.NewID); !ok {
		t.Fatalf("migrated character missing after replay")
	}
}

func TestMigrationRequiresLoginBlock(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Hal", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if _, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"}); !errors.Is(err, impl.ErrLoginNotBlocked) {
		t.Fatalf("migrate without login block error = %v, want ErrLoginNotBlocked", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	if !m.LoginsBlocked("s2") {
		t.Fatalf("LoginsBlocked(s2) = false after LockLogins")
	}
	if _, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"}); err != nil {
		t.Fatalf("migrate with login block error: %v", err)
	}
}

func TestRollbackRestoresSource(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Ivy", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	res, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"})
	if err != nil {
		t.Fatalf("Migrate error: %v", err)
	}
	if err := m.Rollback("k1"); err != nil {
		t.Fatalf("Rollback error: %v", err)
	}
	if _, ok := m.Character(res.NewID); ok {
		t.Fatalf("target character still present after rollback")
	}
	back, ok := m.Character("c-a")
	if !ok {
		t.Fatalf("source character not restored")
	}
	if back.ServerID != "s1" {
		t.Fatalf("restored server = %q, want s1", back.ServerID)
	}
	if len(back.Items) != 2 {
		t.Fatalf("restored items = %v, want intact", back.Items)
	}
}

func TestRollbackTwiceFails(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.AddCharacter(newChar("c-a", "acc-a", "Jon", "s1")); err != nil {
		t.Fatalf("AddCharacter error: %v", err)
	}
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	if _, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "c-a", TargetServer: "s2"}); err != nil {
		t.Fatalf("Migrate error: %v", err)
	}
	if err := m.Rollback("k1"); err != nil {
		t.Fatalf("first rollback error: %v", err)
	}
	if err := m.Rollback("k1"); !errors.Is(err, impl.ErrNoMigration) {
		t.Fatalf("second rollback error = %v, want ErrNoMigration", err)
	}
}

func TestUnknownCharacterMigrationErrors(t *testing.T) {
	m := impl.NewCharacterMigrator()
	if err := m.LockLogins("s2"); err != nil {
		t.Fatalf("LockLogins error: %v", err)
	}
	if _, err := m.Migrate(impl.MigrationRequest{IdempotencyKey: "k1", CharacterID: "missing", TargetServer: "s2"}); !errors.Is(err, impl.ErrUnknownCharacter) {
		t.Fatalf("unknown character error = %v, want ErrUnknownCharacter", err)
	}
}

func hasFriend(list []string, id string) bool {
	for _, f := range list {
		if f == id {
			return true
		}
	}
	return false
}
