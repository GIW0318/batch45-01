package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Lockout is one raid lockout record.
type Lockout struct {
	CharID     string
	RaidID     string
	Difficulty string
	Season     string
	ExpiresAt  int64
}

// AuditEntry records a gm raid lockout reset.
type AuditEntry struct {
	Actor  string
	CharID string
	Reason string
}

// RaidLockoutGuard keeps raid lockouts correct across servers and restarts.
type RaidLockoutGuard struct {
	raidID string
}

// NewRaidLockoutGuard builds an empty lockout guard.
func NewRaidLockoutGuard() *RaidLockoutGuard {
	return &RaidLockoutGuard{}
}

// LockKey is the composite lockout key: character, raid, difficulty and season.
func (r *RaidLockoutGuard) LockKey(charID, raidID, difficulty, season string) string {
	return ""
}

// MapCharacter resolves a cross server character id to the local character id.
func (r *RaidLockoutGuard) MapCharacter(crossID string) string {
	return ""
}

// MarkLocked stores a lockout record.
func (r *RaidLockoutGuard) MarkLocked(l Lockout) {
}

// Locked reports whether a character is locked out of a raid.
func (r *RaidLockoutGuard) Locked(charID, raidID, difficulty, season string) bool {
	return false
}

// Persist returns the lockout records that must be written to storage.
func (r *RaidLockoutGuard) Persist() map[string]Lockout {
	return nil
}

// Restore reloads lockout records after a restart.
func (r *RaidLockoutGuard) Restore(locks map[string]Lockout) {
}

// MarkFirstClear marks the first clear bonus as claimed, returning true when it was free.
func (r *RaidLockoutGuard) MarkFirstClear(charID, raidID string) bool {
	return false
}

// ClaimFirstClear atomically claims the weekly first clear bonus.
func (r *RaidLockoutGuard) ClaimFirstClear(charID, raidID string) (bool, error) {
	return false, errNotImplemented
}

// CanEnterParty checks that every party member may enter the raid.
func (r *RaidLockoutGuard) CanEnterParty(charIDs []string, raidID, difficulty, season string) (bool, error) {
	return false, errNotImplemented
}

// NextReset is the next raid reset moment in unix seconds for the given utc offset.
func (r *RaidLockoutGuard) NextReset(now int64, utcOffsetHours int) int64 {
	return 0
}

// ClaimInstance creates a raid instance once for a party.
func (r *RaidLockoutGuard) ClaimInstance(charIDs []string) (string, error) {
	return "", errNotImplemented
}

// AuditReset records a gm reset of a character lockout.
func (r *RaidLockoutGuard) AuditReset(actor, charID string) AuditEntry {
	return AuditEntry{}
}

// TransferCD moves lockouts from one character to another and reports how many moved.
func (r *RaidLockoutGuard) TransferCD(from, to string) int {
	return 0
}

// PurgeExpired removes expired lockouts and reports how many were removed.
func (r *RaidLockoutGuard) PurgeExpired(now int64) int {
	return 0
}

// ExpiresAt is the stored expiry of a lockout.
func (r *RaidLockoutGuard) ExpiresAt(charID, raidID, difficulty, season string) int64 {
	return 0
}

// Leave records that a character left the raid, which must keep the lockout.
func (r *RaidLockoutGuard) Leave(charID string) {
}
