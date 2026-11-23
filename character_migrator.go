package task

import "errors"

var (
	ErrNotImplemented     = errors.New("not implemented")
	ErrNameConflict       = errors.New("character name conflict on target server")
	ErrLoginNotBlocked    = errors.New("logins must be blocked before migrating")
	ErrUnknownCharacter   = errors.New("unknown character")
	ErrNoMigration        = errors.New("no migration to roll back")
	ErrDuplicateCharacter = errors.New("character already registered")
)

// Character is one game character.
type Character struct {
	ID           string
	AccountID    string
	Name         string
	ServerID     string
	Level        int
	GuildID      string
	GuildRank    string
	Items        []string
	Mail         []string
	Achievements map[string]int
}

// MigrationRequest asks for one character migration.
type MigrationRequest struct {
	IdempotencyKey string
	CharacterID    string
	TargetServer   string
}

// MigrationResult is the outcome of one migration.
type MigrationResult struct {
	CharacterID  string
	NewID        string
	TargetServer string
	Friends      int
	Replayed     bool
}

// Entry is one leaderboard record.
type Entry struct {
	CharacterID string
	Name        string
	Score       int
}

// CharacterMigrator migrates characters between merged servers.
type CharacterMigrator struct{}

// NewCharacterMigrator returns an empty migrator.
func NewCharacterMigrator() *CharacterMigrator { return &CharacterMigrator{} }

// AddCharacter registers a character on its source server.
func (m *CharacterMigrator) AddCharacter(c Character) error { return ErrNotImplemented }

// Character returns a character by id.
func (m *CharacterMigrator) Character(id string) (Character, bool) { return Character{}, false }

// AddFriend creates a bidirectional friend relation.
func (m *CharacterMigrator) AddFriend(a, b string) error { return ErrNotImplemented }

// FriendsOf lists the friend ids of a character.
func (m *CharacterMigrator) FriendsOf(id string) []string { return nil }

// LockLogins blocks logins on a server for the duration of a migration.
func (m *CharacterMigrator) LockLogins(serverID string) error { return ErrNotImplemented }

// UnlockLogins reopens logins on a server.
func (m *CharacterMigrator) UnlockLogins(serverID string) error { return ErrNotImplemented }

// LoginsBlocked reports whether logins are blocked on a server.
func (m *CharacterMigrator) LoginsBlocked(serverID string) bool { return false }

// Migrate moves one character to the target server atomically.
func (m *CharacterMigrator) Migrate(req MigrationRequest) (MigrationResult, error) {
	return MigrationResult{}, ErrNotImplemented
}

// Rollback undoes the migration identified by the idempotency key.
func (m *CharacterMigrator) Rollback(key string) error { return ErrNotImplemented }

// GuildRankOf reports the guild rank of a character.
func (m *CharacterMigrator) GuildRankOf(id string) string { return "" }

// Leaderboard returns the leaderboard of a server without duplicates.
func (m *CharacterMigrator) Leaderboard(serverID string) []Entry { return nil }

// SetScore records the leaderboard score of a character.
func (m *CharacterMigrator) SetScore(id string, score int) error { return ErrNotImplemented }

// SetProgress seeds the account level achievement progress.
func (m *CharacterMigrator) SetProgress(accountID string, progress map[string]int) error {
	return ErrNotImplemented
}

// Progress returns the merged achievement progress of an account.
func (m *CharacterMigrator) Progress(accountID string) map[string]int { return nil }
