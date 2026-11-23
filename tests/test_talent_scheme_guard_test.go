package task

import (
	"testing"
	"time"

	impl "gsb/batch45-game-a-17"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func base() time.Time {
	return time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestActivateResetsSkillCooldowns(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "Burst"))
	must(t, g.SetSkillCooldown("a", "burst", base().Add(time.Hour)))
	must(t, g.Activate("a", base()))
	if got := g.SkillCooldownRemaining("a", "burst", base()); got != 0 {
		t.Fatalf("skill cooldown after activation = %v, want 0", got)
	}
}

func TestOnlyOneSchemeActive(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "A"))
	must(t, g.CreateScheme("b", "B"))
	must(t, g.Activate("a", base()))
	must(t, g.Activate("b", base().Add(time.Second)))
	active := g.ActiveSchemes()
	if len(active) != 1 || active[0] != "b" {
		t.Fatalf("active schemes = %v, want only b", active)
	}
}

func TestSwitchCooldownEnforced(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.SetSwitchCooldown(time.Minute))
	must(t, g.CreateScheme("a", "A"))
	must(t, g.CreateScheme("b", "B"))
	must(t, g.Activate("a", base()))
	if err := g.Activate("b", base().Add(10*time.Second)); err == nil {
		t.Fatalf("switching inside the cooldown must fail")
	}
	active := g.ActiveSchemes()
	if len(active) != 1 || active[0] != "a" {
		t.Fatalf("active schemes = %v, want a to stay active", active)
	}
}

func TestSwitchAfterCooldownAllowed(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.SetSwitchCooldown(time.Minute))
	must(t, g.CreateScheme("a", "A"))
	must(t, g.CreateScheme("b", "B"))
	must(t, g.Activate("a", base()))
	must(t, g.Activate("b", base().Add(61*time.Second)))
	active := g.ActiveSchemes()
	if len(active) != 1 || active[0] != "b" {
		t.Fatalf("active schemes = %v, want b after the cooldown", active)
	}
}

func TestRespecCostUsesHistoricalMaxLevel(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetLevel(60))
	high, err := g.RespecCost()
	must(t, err)
	if high <= 0 {
		t.Fatalf("respec cost at level 60 = %d, want a positive price", high)
	}
	must(t, g.SetLevel(20))
	low, err := g.RespecCost()
	must(t, err)
	if low != high {
		t.Fatalf("respec cost at level 20 = %d, want the historical max level price %d", low, high)
	}
}

func TestAllocateRejectsNegativePoints(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "A"))
	if _, err := g.Allocate("a", "fire", -1); err == nil {
		t.Fatalf("allocating a negative delta must fail")
	}
	if got := g.Points("a", "fire"); got != 0 {
		t.Fatalf("fire points = %d after a rejected decrement, want 0", got)
	}
}

func TestAllocateRejectsOverspend(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "A"))
	if _, err := g.Allocate("a", "fire", 6); err == nil {
		t.Fatalf("allocating more than the budget must fail")
	}
	if got := g.Points("a", "fire"); got != 0 {
		t.Fatalf("fire points = %d, want 0", got)
	}
	if got := g.RemainingPoints("a"); got != 5 {
		t.Fatalf("remaining points = %d, want 5", got)
	}
}

func TestUnequipRemovesEquipmentPoints(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "A"))
	if _, err := g.Allocate("a", "fire", 5); err != nil {
		must(t, err)
	}
	if got := g.RemainingPoints("a"); got != 0 {
		t.Fatalf("remaining points after spending the budget = %d, want 0", got)
	}
	must(t, g.EquipTalentPoints(5))
	if got := g.RemainingPoints("a"); got != 5 {
		t.Fatalf("remaining points with equipment = %d, want 5", got)
	}
	must(t, g.UnequipTalentPoints(5))
	if got := g.EquipmentPoints(); got != 0 {
		t.Fatalf("equipment points = %d after unequip, want 0", got)
	}
	if got := g.RemainingPoints("a"); got != 0 {
		t.Fatalf("remaining points = %d after unequip, want 0", got)
	}
}

func TestImportLegacyKeepsPoints(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(10))
	must(t, g.CreateScheme("a", "A"))
	must(t, g.ImportLegacy("a", map[string]int{"fire": 4}))
	if got := g.Points("a", "fire"); got != 4 {
		t.Fatalf("legacy fire points = %d, want 4 instead of zero", got)
	}
	if got := g.SpentPoints("a"); got != 4 {
		t.Fatalf("spent points = %d, want 4", got)
	}
}

func TestConcurrentSaveRejectsStaleVersion(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "A"))
	must(t, g.Save("a", 0, base()))
	if err := g.Save("a", 0, base().Add(time.Second)); err == nil {
		t.Fatalf("a stale editor must not overwrite the newer save")
	}
	must(t, g.Save("a", 1, base().Add(2*time.Second)))
}

func TestResetRefundsTalentBooks(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "A"))
	books, err := g.Reset("acc")
	must(t, err)
	if books <= 0 {
		t.Fatalf("refunded books = %d, want a positive refund", books)
	}
	if got := g.TalentBooks("acc"); got != books {
		t.Fatalf("talent books = %d, want the refunded %d", got, books)
	}
}

func TestSeasonCompensationIncludesOfflinePlayers(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	all := []string{"online-1", "offline-1", "offline-2"}
	got, err := g.SeasonCompensate([]string{"online-1"}, all)
	must(t, err)
	if len(got) != len(all) {
		t.Fatalf("compensated players = %v, want all %v", got, all)
	}
	for _, player := range all {
		if !hasString(got, player) {
			t.Fatalf("player %s missed the season compensation: %v", player, got)
		}
	}
}

func TestPreviewMatchesSavedPoints(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(10))
	must(t, g.CreateScheme("a", "A"))
	preview, err := g.Preview("a", "fire", 3)
	must(t, err)
	if preview != 3 {
		t.Fatalf("preview = %d, want 3", preview)
	}
	if got := g.Points("a", "fire"); got != 0 {
		t.Fatalf("preview mutated the scheme: points = %d, want 0", got)
	}
	if _, err := g.Allocate("a", "fire", 3); err != nil {
		must(t, err)
	}
	if got := g.Points("a", "fire"); got != preview {
		t.Fatalf("saved points = %d, want the previewed %d", got, preview)
	}
}

func TestPointsConserved(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(10))
	must(t, g.CreateScheme("a", "A"))
	if _, err := g.Allocate("a", "fire", 6); err != nil {
		must(t, err)
	}
	spent := g.SpentPoints("a")
	remaining := g.RemainingPoints("a")
	if spent != 6 {
		t.Fatalf("spent = %d, want 6", spent)
	}
	if spent+remaining != g.TotalPoints() {
		t.Fatalf("spent %d + remaining %d != total %d", spent, remaining, g.TotalPoints())
	}
}

func TestAllocateAcrossTalentsCannotExceedTotal(t *testing.T) {
	g := impl.NewTalentSchemeGuard()
	must(t, g.SetTotalPoints(5))
	must(t, g.CreateScheme("a", "A"))
	if _, err := g.Allocate("a", "fire", 3); err != nil {
		must(t, err)
	}
	if _, err := g.Allocate("a", "ice", 3); err == nil {
		t.Fatalf("spending 3+3 over a budget of 5 must fail")
	}
	if got := g.Points("a", "ice"); got != 0 {
		t.Fatalf("ice points = %d, want 0", got)
	}
}
