package task

import "errors"

var (
	ErrNotImplemented    = errors.New("not implemented")
	ErrExpRollback       = errors.New("experience rollback rejected")
	ErrLevelLocked       = errors.New("level not unlocked")
	ErrTrackNotPurchased = errors.New("paid track not purchased")
	ErrSeasonClosed      = errors.New("season already closed")
	ErrUnknownAccount    = errors.New("unknown account")
)

// Track identifies a reward track of the battle pass.
type Track int

const (
	// TrackFree is the reward track available to every player.
	TrackFree Track = iota
	// TrackPaid is the reward track unlocked by purchasing the pass.
	TrackPaid
)

// RewardGrant describes the outcome of one reward claim.
type RewardGrant struct {
	Level          int
	Track          Track
	MailID         string
	AlreadyClaimed bool
}

// ArchiveRecord is the archived progress of one closed season.
type ArchiveRecord struct {
	Season    string
	AccountID string
	Exp       int64
	Level     int
}

// BattlePassLedger keeps season pass progress and reward claims.
type BattlePassLedger struct{}

// NewBattlePassLedger returns a ledger for the given season and level cap.
func NewBattlePassLedger(season string, maxLevel int) *BattlePassLedger {
	return &BattlePassLedger{}
}

// Season reports the active season identifier.
func (b *BattlePassLedger) Season() string { return "" }

// MaxLevel reports the level cap of the pass.
func (b *BattlePassLedger) MaxLevel() int { return 0 }

// BindRole binds a game role to an account so both roles share progress.
func (b *BattlePassLedger) BindRole(accountID, roleID string) error { return ErrNotImplemented }

// AddExp adds experience for a role and returns the account total.
func (b *BattlePassLedger) AddExp(accountID, roleID string, amount int64) (int64, error) {
	return 0, ErrNotImplemented
}

// Exp reports the account experience, capped at the level cap.
func (b *BattlePassLedger) Exp(accountID string) int64 { return 0 }

// Level reports the account level derived from experience.
func (b *BattlePassLedger) Level(accountID string) int { return 0 }

// Purchase unlocks the paid reward track for the account.
func (b *BattlePassLedger) Purchase(accountID string) error { return ErrNotImplemented }

// Purchased reports whether the paid track is unlocked.
func (b *BattlePassLedger) Purchased(accountID string) bool { return false }

// Claim grants one level reward on one track.
func (b *BattlePassLedger) Claim(accountID string, level int, track Track) (RewardGrant, error) {
	return RewardGrant{}, ErrNotImplemented
}

// GrantCount reports how many times a reward was granted.
func (b *BattlePassLedger) GrantCount(accountID string, level int, track Track) int { return 0 }

// MailCount reports how many reward mails were sent to the account.
func (b *BattlePassLedger) MailCount(accountID string) int { return 0 }

// ApplyCompensation grants missed level rewards in ascending level order.
func (b *BattlePassLedger) ApplyCompensation(accountID string, levels []int) ([]int, error) {
	return nil, ErrNotImplemented
}

// SwitchSeason archives the account progress and starts a new season.
func (b *BattlePassLedger) SwitchSeason(season, accountID string) (ArchiveRecord, error) {
	return ArchiveRecord{}, ErrNotImplemented
}

// Close closes the current season so that no further claims are accepted.
func (b *BattlePassLedger) Close() error { return ErrNotImplemented }

// SyncExp merges the account experience from another shard.
func (b *BattlePassLedger) SyncExp(accountID string, exp int64) error { return ErrNotImplemented }

// Snapshot serializes the ledger so that claims survive a restart.
func (b *BattlePassLedger) Snapshot() ([]byte, error) { return nil, ErrNotImplemented }

// Restore rebuilds the ledger from a snapshot.
func (b *BattlePassLedger) Restore(data []byte) error { return ErrNotImplemented }
