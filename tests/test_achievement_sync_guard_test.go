package task

import (
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-16"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func base() time.Time {
	return time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
}

func findView(views []impl.AchievementView, id string) (impl.AchievementView, bool) {
	for _, v := range views {
		if v.ID == id {
			return v, true
		}
	}
	return impl.AchievementView{}, false
}

func TestProgressIsAccountWide(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "kill", Name: "Killer", Goal: 10}))
	must(t, g.AddProgressAt("acc", "char1", "kill", 4, base()))
	must(t, g.AddProgressAt("acc", "char2", "kill", 5, base()))
	if got := g.Progress("acc", "char1", "kill"); got != 4 {
		t.Fatalf("char1 progress = %d, want 4", got)
	}
	if got := g.AccountProgress("acc", "kill"); got != 9 {
		t.Fatalf("account progress = %d, want the sum 9 across characters", got)
	}
}

func TestMergeCharactersAddsProgress(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "kill", Name: "Killer", Goal: 20}))
	must(t, g.AddProgressAt("acc", "charA", "kill", 3, base()))
	must(t, g.AddProgressAt("acc", "charB", "kill", 4, base()))
	must(t, g.MergeCharacters("acc", "charB", "charA"))
	if got := g.Progress("acc", "charA", "kill"); got != 7 {
		t.Fatalf("merged progress = %d, want the sum 7 instead of the max 4", got)
	}
	if got := g.Progress("acc", "charB", "kill"); got != 0 {
		t.Fatalf("source character still holds %d progress after merge, want 0", got)
	}
}

func TestHiddenAchievementHidesCondition(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "hidden-1", Name: "???", Hidden: true, Condition: "defeat the secret boss", Goal: 1}))
	must(t, g.Register(impl.Achievement{ID: "visible-1", Name: "Boar Hunter", Condition: "kill 10 boars", Goal: 10}))
	views := g.Views("acc")
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2", len(views))
	}
	hidden, ok := findView(views, "hidden-1")
	if !ok {
		t.Fatalf("hidden achievement missing from the projection")
	}
	if hidden.Condition != "" {
		t.Fatalf("hidden condition leaked as %q", hidden.Condition)
	}
	visible, ok := findView(views, "visible-1")
	if !ok {
		t.Fatalf("visible achievement missing from the projection")
	}
	if visible.Condition != "kill 10 boars" {
		t.Fatalf("visible condition = %q, want the real condition", visible.Condition)
	}
}

func TestConcurrentUnlockSendsSingleMail(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "grind", Name: "Grinder", Goal: 20}))
	const workers = 20
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = g.AddProgressAt("acc", "char1", "grind", 1, base().Add(time.Duration(i)*time.Second))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent progress %d failed: %v", i, err)
		}
	}
	if got := g.UnlockCount("acc", "grind"); got != 1 {
		t.Fatalf("unlock count = %d, want 1", got)
	}
	if got := len(g.Mails("acc")); got != 1 {
		t.Fatalf("mails = %d, want exactly 1 reward", got)
	}
	if got := g.AccountProgress("acc", "grind"); got != 20 {
		t.Fatalf("account progress = %d, want 20", got)
	}
}

func TestUnlockRollsBackWhenMailFails(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "mail", Name: "Mail", Goal: 5}))
	must(t, g.AddProgressAt("acc", "char1", "mail", 4, base()))
	must(t, g.SetMailFault(true))
	if err := g.AddProgressAt("acc", "char1", "mail", 1, base().Add(time.Second)); err == nil {
		t.Fatalf("unlock must fail when the reward mail fails")
	}
	if g.Unlocked("acc", "mail") {
		t.Fatalf("achievement stayed unlocked although the mail transaction failed")
	}
	if got := len(g.Mails("acc")); got != 0 {
		t.Fatalf("mails = %d after a failed transaction, want 0", got)
	}
	must(t, g.SetMailFault(false))
	must(t, g.Unlock("acc", "mail", base().Add(2*time.Second)))
	if !g.Unlocked("acc", "mail") {
		t.Fatalf("retry after the mail failure did not unlock")
	}
	if got := len(g.Mails("acc")); got != 1 {
		t.Fatalf("mails = %d after the retry, want 1", got)
	}
}

func TestNegativeProgressRejected(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "ach", Name: "A", Goal: 10}))
	must(t, g.AddProgressAt("acc", "char1", "ach", 5, base()))
	if err := g.AddProgressAt("acc", "char1", "ach", -2, base()); err == nil {
		t.Fatalf("negative progress must be rejected")
	}
	if got := g.AccountProgress("acc", "ach"); got != 5 {
		t.Fatalf("progress = %d after a rejected rollback, want 5", got)
	}
}

func TestRemoteSyncKeepsHigherValue(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "ach", Name: "A", Goal: 100}))
	must(t, g.AddProgressAt("acc", "char1", "ach", 10, base()))
	must(t, g.SyncRemote("acc", "ach", 25))
	if got := g.AccountProgress("acc", "ach"); got != 25 {
		t.Fatalf("progress = %d after syncing the higher remote value, want 25", got)
	}
	must(t, g.SyncRemote("acc", "ach", 5))
	if got := g.AccountProgress("acc", "ach"); got != 25 {
		t.Fatalf("progress = %d after a lower remote sync, want 25", got)
	}
}

func TestTimeWindowEvaluatedInUTC(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{
		ID:      "limited",
		Name:    "Limited",
		Goal:    1,
		StartAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndAt:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}))
	inside := time.Date(2025, 12, 31, 20, 0, 0, 0, time.FixedZone("EST", -5*3600))
	ok, err := g.InWindow("limited", inside)
	must(t, err)
	if !ok {
		t.Fatalf("instant %v is inside the UTC window but was rejected", inside.UTC())
	}
	outside := time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC)
	ok, err = g.InWindow("limited", outside)
	must(t, err)
	if ok {
		t.Fatalf("instant %v is outside the UTC window but was accepted", outside)
	}
}

func TestSequenceStepsSortedNumerically(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.RegisterSequence("chapter", []int64{10, 2, 1}))
	steps := g.SequenceSteps("chapter")
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(steps))
	}
	for i, want := range []int64{1, 2, 10} {
		if steps[i] != want {
			t.Fatalf("step %d = %d, want numeric order %d", i, steps[i], want)
		}
	}
}

func TestTeamKillCreditedToMember(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "team-kills", Name: "Team Kills", Goal: 10}))
	must(t, g.TeamContribution("leader", "member", "team-kills", 5))
	if got := g.AccountProgress("member", "team-kills"); got != 5 {
		t.Fatalf("member progress = %d, want 5", got)
	}
	if got := g.AccountProgress("leader", "team-kills"); got != 0 {
		t.Fatalf("leader progress = %d, want 0 because the kill belonged to the member", got)
	}
}

func TestReissueOrderedByUnlockTime(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "ach-a", Name: "A", Goal: 1}))
	must(t, g.Register(impl.Achievement{ID: "ach-b", Name: "B", Goal: 1}))
	must(t, g.AddProgressAt("acc", "char1", "ach-a", 1, base().Add(2*time.Minute)))
	must(t, g.AddProgressAt("acc", "char1", "ach-b", 1, base().Add(1*time.Minute)))
	mails, err := g.Reissue("acc")
	must(t, err)
	if len(mails) != 2 {
		t.Fatalf("reissued mails = %d, want 2", len(mails))
	}
	if mails[0].AchievementID != "ach-b" {
		t.Fatalf("first reissued mail = %s, want the earliest unlock ach-b", mails[0].AchievementID)
	}
	if mails[0].At.After(mails[1].At) {
		t.Fatalf("reissued mails are not ordered by unlock time: %v", mails)
	}
}

func TestViewsReflectLatestProgress(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "ach", Name: "A", Goal: 10}))
	must(t, g.AddProgressAt("acc", "char1", "ach", 5, base()))
	v, ok := findView(g.Views("acc"), "ach")
	if !ok {
		t.Fatalf("achievement missing from the first projection")
	}
	if v.Value != 5 {
		t.Fatalf("projected value = %d, want 5", v.Value)
	}
	must(t, g.AddProgressAt("acc", "char1", "ach", 3, base().Add(time.Second)))
	v, ok = findView(g.Views("acc"), "ach")
	if !ok {
		t.Fatalf("achievement missing from the refreshed projection")
	}
	if v.Value != 8 {
		t.Fatalf("projected value = %d after more progress, want 8 from the invalidated cache", v.Value)
	}
}

func TestAutoUnlockAtGoal(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "ach", Name: "A", Goal: 3}))
	must(t, g.AddProgressAt("acc", "char1", "ach", 3, base()))
	if !g.Unlocked("acc", "ach") {
		t.Fatalf("reaching the goal did not unlock the achievement")
	}
	if got := g.UnlockCount("acc", "ach"); got != 1 {
		t.Fatalf("unlock count = %d, want 1", got)
	}
	mails := g.Mails("acc")
	if len(mails) != 1 || mails[0].AchievementID != "ach" {
		t.Fatalf("mails = %v, want one reward for ach", mails)
	}
}

func TestDuplicateUnlockNoExtraMail(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "ach", Name: "A", Goal: 1}))
	must(t, g.AddProgressAt("acc", "char1", "ach", 1, base()))
	if err := g.Unlock("acc", "ach", base().Add(time.Second)); err == nil {
		t.Fatalf("unlocking an already unlocked achievement must fail")
	}
	if got := g.UnlockCount("acc", "ach"); got != 1 {
		t.Fatalf("unlock count = %d, want 1", got)
	}
	if got := len(g.Mails("acc")); got != 1 {
		t.Fatalf("mails = %d after a duplicate unlock, want 1", got)
	}
}

func TestUnlockRequiresGoal(t *testing.T) {
	g := impl.NewAchievementGuard()
	must(t, g.Register(impl.Achievement{ID: "ach", Name: "A", Goal: 10}))
	must(t, g.AddProgressAt("acc", "char1", "ach", 5, base()))
	if err := g.Unlock("acc", "ach", base()); err == nil {
		t.Fatalf("unlocking below the goal must fail")
	}
	if g.Unlocked("acc", "ach") {
		t.Fatalf("achievement unlocked below its goal")
	}
	must(t, g.AddProgressAt("acc", "char1", "ach", 5, base().Add(time.Second)))
	must(t, g.Unlock("acc", "ach", base().Add(2*time.Second)))
	if !g.Unlocked("acc", "ach") {
		t.Fatalf("unlock at the goal failed")
	}
}
