package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Quest is one node of a quest chain.
type Quest struct {
	ID      string
	Prereqs []string
	Branch  string
	Realm   string
	Daily   bool
	Steps   int
}

// QuestChainGuard keeps quest chains consistent across versions and maps.
type QuestChainGuard struct {
}

// NewQuestChainGuard builds an empty quest chain guard.
func NewQuestChainGuard() *QuestChainGuard {
	return &QuestChainGuard{}
}

// Unlockable reports whether every prerequisite of a quest is completed.
func (g *QuestChainGuard) Unlockable(completed []string, q Quest) bool {
	return false
}

// PrereqChain returns the full prerequisite chain of a quest, not only step one.
func (g *QuestChainGuard) PrereqChain(q Quest) []string {
	return nil
}

// SwitchBranch moves a player onto a branch and clears the other branch progress.
func (g *QuestChainGuard) SwitchBranch(player, branch string) int {
	return 0
}

// GiveItem hands quest items to a player without swallowing items on a full bag.
func (g *QuestChainGuard) GiveItem(bagFree, count int) (int, error) {
	return 0, errNotImplemented
}

// AbandonRefund returns the consumed items when a quest is abandoned.
func (g *QuestChainGuard) AbandonRefund(consumed int) int {
	return 0
}

// SyncProgress sums objective counters collected on different maps.
func (g *QuestChainGuard) SyncProgress(counts []int) int {
	return 0
}

// MigrateID maps a removed quest id onto its replacement id.
func (g *QuestChainGuard) MigrateID(oldID string) string {
	return ""
}

// ClaimOnce accepts a quest at most once, even under concurrent auto accept.
func (g *QuestChainGuard) ClaimOnce(player, questID string) (bool, error) {
	return false, errNotImplemented
}

// Advance moves a quest forward, rejecting any backwards progress.
func (g *QuestChainGuard) Advance(player, questID string, to int) (int, error) {
	return 0, errNotImplemented
}

// CounterKey is the storage key of the counter used by a quest.
func (g *QuestChainGuard) CounterKey(q Quest) string {
	return ""
}

// Complete reports whether the completion event may be emitted after the commit.
func (g *QuestChainGuard) Complete(commitOK bool) bool {
	return false
}

// OfflineCatchup counts the quest events a returning offline player missed.
func (g *QuestChainGuard) OfflineCatchup(events int, since int64) int {
	return 0
}

// RewardDelivery delivers a quest reward, falling back to mail on a full bag.
func (g *QuestChainGuard) RewardDelivery(bagFree, rewardSlots int) (bool, error) {
	return false, errNotImplemented
}

// Progress is the stored progress of one quest for one player.
func (g *QuestChainGuard) Progress(player, questID string) int {
	return 0
}

// ResetPlayer clears the quest progress of a single player.
func (g *QuestChainGuard) ResetPlayer(player string) {
}
