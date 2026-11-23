package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Achievement is the static definition of one achievement.
type Achievement struct {
	ID        string
	Name      string
	Hidden    bool
	Condition string
	Goal      int64
	StartAt   time.Time
	EndAt     time.Time
}

// Mail is one reward mail sent when an achievement unlocks.
type Mail struct {
	AccountID     string
	AchievementID string
	At            time.Time
}

// AchievementView is the public projection returned to clients.
type AchievementView struct {
	ID        string
	Name      string
	Value     int64
	Goal      int64
	Unlocked  bool
	Condition string
}

// AchievementGuard keeps achievement progress account wide and idempotent.
type AchievementGuard struct {
}

// NewAchievementGuard builds an empty achievement guard.
func NewAchievementGuard() *AchievementGuard {
	return &AchievementGuard{}
}

// Register stores an achievement definition.
func (g *AchievementGuard) Register(a Achievement) error {
	return errNotImplemented
}

// AddProgressAt adds progress at a known instant.
func (g *AchievementGuard) AddProgressAt(accountID, characterID, achievementID string, delta int64, at time.Time) error {
	return errNotImplemented
}

// AddProgress adds progress with the current time.
func (g *AchievementGuard) AddProgress(accountID, characterID, achievementID string, delta int64) error {
	return errNotImplemented
}

// Progress returns the account wide progress of an achievement.
func (g *AchievementGuard) Progress(accountID, characterID, achievementID string) int64 {
	return 0
}

// AccountProgress returns the progress stored for an account.
func (g *AchievementGuard) AccountProgress(accountID, achievementID string) int64 {
	return 0
}

// Unlocked reports whether an achievement is unlocked for an account.
func (g *AchievementGuard) Unlocked(accountID, achievementID string) bool {
	return false
}

// UnlockCount reports how many unlock events were recorded.
func (g *AchievementGuard) UnlockCount(accountID, achievementID string) int {
	return 0
}

// Unlock unlocks an achievement once, sending exactly one reward mail.
func (g *AchievementGuard) Unlock(accountID, achievementID string, at time.Time) error {
	return errNotImplemented
}

// Mails returns the reward mails of an account.
func (g *AchievementGuard) Mails(accountID string) []Mail {
	return nil
}

// MergeCharacters merges the progress of one character into another.
func (g *AchievementGuard) MergeCharacters(accountID, fromChar, toChar string) error {
	return errNotImplemented
}

// Views returns the client projection of every achievement of an account.
func (g *AchievementGuard) Views(accountID string) []AchievementView {
	return nil
}

// SyncRemote merges a progress value replicated from another server.
func (g *AchievementGuard) SyncRemote(accountID, achievementID string, value int64) error {
	return errNotImplemented
}

// InWindow reports whether an instant falls inside the achievement window.
func (g *AchievementGuard) InWindow(achievementID string, at time.Time) (bool, error) {
	return false, errNotImplemented
}

// RegisterSequence stores the numeric steps of a sequence achievement.
func (g *AchievementGuard) RegisterSequence(id string, steps []int64) error {
	return errNotImplemented
}

// SequenceSteps returns the steps in numeric order.
func (g *AchievementGuard) SequenceSteps(id string) []int64 {
	return nil
}

// TeamContribution credits a team kill to the member who made it.
func (g *AchievementGuard) TeamContribution(leaderAccount, memberAccount, achievementID string, delta int64) error {
	return errNotImplemented
}

// Reissue rebuilds every reward mail of an account ordered by unlock time.
func (g *AchievementGuard) Reissue(accountID string) ([]Mail, error) {
	return nil, errNotImplemented
}

// SetMailFault makes mail delivery fail so unlock transactions can be tested.
func (g *AchievementGuard) SetMailFault(fail bool) error {
	return errNotImplemented
}
