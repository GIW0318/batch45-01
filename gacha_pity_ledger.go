package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// PityLedger tracks gacha pity counters per account and pool.
type PityLedger struct {
	threshold int
}

// NewPityLedger builds a ledger with the pity threshold of the current version.
func NewPityLedger(threshold int) *PityLedger {
	return &PityLedger{threshold: threshold}
}

// PoolKey is the ledger key of one account in one card pool.
func (p *PityLedger) PoolKey(accountID, poolID string) string {
	return ""
}

// OnDraw counts draws and returns the new pity counter.
func (p *PityLedger) OnDraw(accountID, poolID string, count int) int {
	return 0
}

// Pity is the current pity counter of an account in a pool.
func (p *PityLedger) Pity(accountID, poolID string) int {
	return 0
}

// Threshold is the configured pity threshold.
func (p *PityLedger) Threshold() int {
	return 0
}

// Triggered reports whether the pity counter reached the threshold.
func (p *PityLedger) Triggered(accountID, poolID string) bool {
	return false
}

// OnHit consumes a pity hit and returns the counter that was reset.
func (p *PityLedger) OnHit(accountID, poolID string) int {
	return 0
}

// CarryOver returns the pity counter inherited by a new pool version.
func (p *PityLedger) CarryOver(accountID, poolID, version string) int {
	return 0
}

// RateUpEnd returns the banked counter after a rate up period ends.
func (p *PityLedger) RateUpEnd(accountID, poolID string) int {
	return 0
}

// ConcurrentDraw serialises several devices drawing on the same pool.
func (p *PityLedger) ConcurrentDraw(accountID, poolID string, workers int) int {
	return 0
}

// Seed derives a reproducible draw seed from a session id.
func (p *PityLedger) Seed(sessionID string) uint64 {
	return 0
}

// Snapshot exports the pity counters for persistence.
func (p *PityLedger) Snapshot() map[string]int {
	return nil
}

// Restore reloads pity counters from storage.
func (p *PityLedger) Restore(state map[string]int) {
}

// DeviceGuard accepts only the first draw device of an account.
func (p *PityLedger) DeviceGuard(deviceID string) bool {
	return false
}

// Reload applies a hot updated pity threshold and returns the kept counter.
func (p *PityLedger) Reload(accountID, poolID string, newThreshold int) int {
	return 0
}

// ResolveReward returns the pity reward when pity triggered, never a normal drop.
func (p *PityLedger) ResolveReward(pityTriggered bool, normalDrop string) string {
	return ""
}
