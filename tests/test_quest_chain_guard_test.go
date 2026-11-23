package task

import (
	"testing"

	impl "gsb/batch45-game-a-06"
)

func TestUnlockableChecksWholePrereqChain(t *testing.T) {
	g := impl.NewQuestChainGuard()
	q := impl.Quest{ID: "q3", Prereqs: []string{"q1", "q2"}}
	if g.Unlockable([]string{"q1"}, q) {
		t.Fatalf("quest must stay locked while q2 is still missing")
	}
	if !g.Unlockable([]string{"q1", "q2"}, q) {
		t.Fatalf("quest must unlock once every prerequisite is completed")
	}
}

func TestPrereqChainReturnsEveryStep(t *testing.T) {
	g := impl.NewQuestChainGuard()
	q := impl.Quest{ID: "q4", Prereqs: []string{"q1", "q2", "q3"}}
	got := g.PrereqChain(q)
	if len(got) != 3 {
		t.Fatalf("prereq chain has %d entries, want all 3 steps", len(got))
	}
}

func TestSwitchBranchClearsOtherBranchProgress(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if got := g.SwitchBranch("player-1", "branch-b"); got != 2 {
		t.Fatalf("cleared %d progress nodes on branch switch, want the 2 nodes of the old branch", got)
	}
}

func TestGiveItemAcceptsPartialFeedOnFullBag(t *testing.T) {
	g := impl.NewQuestChainGuard()
	given, err := g.GiveItem(2, 5)
	if err != nil {
		t.Fatalf("partial hand-over must not fail: %v", err)
	}
	if given != 2 {
		t.Fatalf("handed over %d items, want the 2 free bag slots", given)
	}
}

func TestAbandonRefundsConsumedItems(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if got := g.AbandonRefund(5); got != 5 {
		t.Fatalf("refunded %d items, want all 5 consumed items back", got)
	}
}

func TestCrossMapProgressSumsAllMaps(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if got := g.SyncProgress([]int{3, 4, 2}); got != 9 {
		t.Fatalf("synced progress = %d, want 9 collected across maps", got)
	}
}

func TestLegacyQuestIDMigrated(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if got := g.MigrateID("q-legacy-3"); got != "q-3" {
		t.Fatalf("legacy quest id mapped to %q, want q-3", got)
	}
}

func TestClaimOnceRejectsConcurrentDoubleAccept(t *testing.T) {
	g := impl.NewQuestChainGuard()
	claimed, err := g.ClaimOnce("player-1", "q7")
	if err != nil {
		t.Fatalf("auto accept failed: %v", err)
	}
	if !claimed {
		t.Fatalf("first auto accept must succeed")
	}
	again, err := g.ClaimOnce("player-1", "q7")
	if err != nil {
		t.Fatalf("second auto accept returned error: %v", err)
	}
	if again {
		t.Fatalf("a quest must not be accepted twice")
	}
}

func TestAdvanceRejectsBackwardsProgress(t *testing.T) {
	g := impl.NewQuestChainGuard()
	got, err := g.Advance("player-1", "q7", 5)
	if err != nil {
		t.Fatalf("advancing quest failed: %v", err)
	}
	if got != 5 {
		t.Fatalf("advanced to %d, want 5", got)
	}
	if _, err := g.Advance("player-1", "q7", 3); err == nil {
		t.Fatalf("backwards progress must be rejected")
	}
}

func TestDailyAndMainQuestUseSeparateCounters(t *testing.T) {
	g := impl.NewQuestChainGuard()
	daily := g.CounterKey(impl.Quest{ID: "kill-10", Daily: true})
	main := g.CounterKey(impl.Quest{ID: "kill-10", Daily: false})
	if daily == "" || main == "" {
		t.Fatalf("counter keys must not be empty, got %q and %q", daily, main)
	}
	if daily == main {
		t.Fatalf("daily and main quests must not share the counter key %q", daily)
	}
}

func TestCompleteEmitsEventOnlyAfterCommit(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if !g.Complete(true) {
		t.Fatalf("completion event must be emitted after a successful commit")
	}
	if g.Complete(false) {
		t.Fatalf("completion event must not be emitted when the commit failed")
	}
}

func TestOfflineCatchupCountsMissedEvents(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if got := g.OfflineCatchup(4, 1700000000); got != 4 {
		t.Fatalf("offline catchup counted %d events, want 4", got)
	}
}

func TestRewardFallsBackToMailOnFullBag(t *testing.T) {
	g := impl.NewQuestChainGuard()
	mailed, err := g.RewardDelivery(0, 3)
	if err != nil {
		t.Fatalf("reward delivery failed: %v", err)
	}
	if !mailed {
		t.Fatalf("reward must be mailed when the bag has no free slot")
	}
}

func TestProgressIsTrackedPerQuest(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if _, err := g.Advance("player-1", "q7", 2); err != nil {
		t.Fatalf("advance failed: %v", err)
	}
	if got := g.Progress("player-1", "q7"); got != 2 {
		t.Fatalf("stored progress = %d, want 2", got)
	}
}

func TestResetPlayerClearsOnlyThatPlayer(t *testing.T) {
	g := impl.NewQuestChainGuard()
	if _, err := g.Advance("player-1", "q7", 2); err != nil {
		t.Fatalf("advance player-1 failed: %v", err)
	}
	if _, err := g.Advance("player-2", "q7", 5); err != nil {
		t.Fatalf("advance player-2 failed: %v", err)
	}
	g.ResetPlayer("player-1")
	if got := g.Progress("player-1", "q7"); got != 0 {
		t.Fatalf("reset player still has progress %d", got)
	}
	if got := g.Progress("player-2", "q7"); got != 5 {
		t.Fatalf("other player progress = %d, want the untouched 5", got)
	}
}
