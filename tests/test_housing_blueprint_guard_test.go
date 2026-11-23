package task

import (
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-19"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func base() time.Time {
	return time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
}

func furn(id, owner string) impl.Placement {
	return impl.Placement{FurnitureID: id, OwnerID: owner, X: 1, Y: 2, Rotation: 0, Durability: 100, Terrain: "grass"}
}

func TestBlueprintRecordsOwnership(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	placements := []impl.Placement{{FurnitureID: "chair", OwnerID: "alice", X: 1, Y: 2, Terrain: "grass"}}
	must(t, g.CreateBlueprint("bp1", "alice", placements, base()))
	bp, ok := g.Blueprint("bp1")
	if !ok {
		t.Fatalf("blueprint was not stored")
	}
	if bp.OwnerID != "alice" {
		t.Fatalf("blueprint owner = %q, want alice", bp.OwnerID)
	}
	if len(bp.Placements) != 1 || bp.Placements[0].OwnerID != "alice" {
		t.Fatalf("furniture ownership lost: %+v", bp.Placements[0])
	}
}

func TestLimitedFurnitureNotDuplicated(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	must(t, g.SetLimitedFurniture([]string{"throne"}))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("throne", "alice")}, base()))
	if _, err := g.Apply("bp1", "alice", "grass", base()); err != nil {
		must(t, err)
	}
	if _, err := g.Apply("bp1", "alice", "grass", base().Add(time.Second)); err == nil {
		t.Fatalf("a limited furniture item must not be duplicated")
	}
	if got := len(g.Placed("alice")); got != 1 {
		t.Fatalf("placed furniture = %d, want 1 throne", got)
	}
}

func TestConcurrentApplyPlacesOnce(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	placements := []impl.Placement{furn("bed", "alice"), furn("desk", "alice")}
	must(t, g.CreateBlueprint("bp1", "alice", placements, base()))
	const workers = 8
	var wg sync.WaitGroup
	success := 0
	var mu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := g.Apply("bp1", "alice", "grass", base()); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success < 1 {
		t.Fatalf("no concurrent apply succeeded")
	}
	if got := g.ApplyCount("bp1"); got != 1 {
		t.Fatalf("apply count = %d, want 1", got)
	}
	if got := len(g.Placed("alice")); got != len(placements) {
		t.Fatalf("placed furniture = %d, want the %d blueprint items once", got, len(placements))
	}
}

func TestPlacementKeepsPositionAndRotation(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	src := impl.Placement{FurnitureID: "statue", OwnerID: "alice", X: 1.5, Y: -2.5, Rotation: 90, Durability: 33, Terrain: "grass"}
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{src}, base()))
	if _, err := g.Apply("bp1", "alice", "grass", base()); err != nil {
		must(t, err)
	}
	placed := g.Placed("alice")
	if len(placed) != 1 {
		t.Fatalf("placed = %d, want 1", len(placed))
	}
	got := placed[0]
	if got.X != 1.5 || got.Y != -2.5 || got.Rotation != 90 {
		t.Fatalf("position/rotation = (%v,%v,%d), want (1.5,-2.5,90)", got.X, got.Y, got.Rotation)
	}
}

func TestImportRemapsConflictingIDs(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("f1", "alice")}, base()))
	imported := impl.Blueprint{
		ID:         "bp1",
		OwnerID:    "bob",
		Version:    2,
		Placements: []impl.Placement{furn("f1", "bob")},
		At:         base(),
	}
	newID, err := g.Import(imported, base())
	must(t, err)
	if newID == "" || newID == "bp1" {
		t.Fatalf("imported blueprint id = %q, want a fresh non conflicting id", newID)
	}
	exported, err := g.Export(newID)
	must(t, err)
	if len(exported.Placements) != 1 {
		t.Fatalf("imported placements = %d, want 1", len(exported.Placements))
	}
	if exported.Placements[0].FurnitureID == "f1" {
		t.Fatalf("furniture id f1 was not remapped on import")
	}
}

func TestFurnitureCapRejectsOversizedBlueprint(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(2))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("a", "alice"), furn("b", "alice"), furn("c", "alice")}, base()))
	if _, err := g.Apply("bp1", "alice", "grass", base()); err == nil {
		t.Fatalf("applying a blueprint above the furniture cap must fail")
	}
	if got := len(g.Placed("alice")); got != 0 {
		t.Fatalf("placed = %d after a rejected apply, want 0", got)
	}
	if got := g.ApplyCount("bp1"); got != 0 {
		t.Fatalf("apply count = %d, want 0", got)
	}
}

func TestFailedApplyLeavesNoPartialPlacements(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(4))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("a", "alice"), furn("b", "alice")}, base()))
	if _, err := g.Apply("bp1", "alice", "grass", base()); err != nil {
		must(t, err)
	}
	must(t, g.SetFurnitureCap(3))
	must(t, g.CreateBlueprint("bp2", "alice", []impl.Placement{furn("c", "alice"), furn("d", "alice")}, base()))
	if _, err := g.Apply("bp2", "alice", "grass", base().Add(time.Second)); err == nil {
		t.Fatalf("the second blueprint must not fit into the cap of 3")
	}
	if got := len(g.Placed("alice")); got != 2 {
		t.Fatalf("placed = %d after the failed apply, want only the 2 items of bp1", got)
	}
}

func TestLegacyImportMigratesToCurrentVersion(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	legacy := impl.Blueprint{
		ID:         "legacy",
		OwnerID:    "alice",
		Version:    0,
		Placements: []impl.Placement{{FurnitureID: "old", OwnerID: "alice", X: 3.25, Y: 4.5, Rotation: 45, Terrain: "grass"}},
		At:         base(),
	}
	newID, err := g.Import(legacy, base())
	must(t, err)
	exported, err := g.Export(newID)
	must(t, err)
	if exported.Version != g.CurrentVersion() {
		t.Fatalf("imported version = %d, want the current version %d", exported.Version, g.CurrentVersion())
	}
	if len(exported.Placements) != 1 {
		t.Fatalf("placements = %d, want 1", len(exported.Placements))
	}
	got := exported.Placements[0]
	if got.X != 3.25 || got.Y != 4.5 || got.Rotation != 45 {
		t.Fatalf("legacy fields shifted: %+v", got)
	}
}

func TestSharedBlueprintNeedsGrant(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	must(t, g.CreateBlueprint("shared", "alice", []impl.Placement{furn("sofa", "alice")}, base()))
	must(t, g.SetShared("shared", true))
	bp, ok := g.Blueprint("shared")
	if !ok {
		t.Fatalf("blueprint missing")
	}
	if !bp.Shared {
		t.Fatalf("blueprint should be shared")
	}
	if _, err := g.Apply("shared", "bob", "grass", base()); err == nil {
		t.Fatalf("a shared blueprint must still reject players without access")
	}
	must(t, g.GrantAccess("shared", "bob"))
	if _, err := g.Apply("shared", "bob", "grass", base()); err != nil {
		must(t, err)
	}
	if got := len(g.Placed("bob")); got != 1 {
		t.Fatalf("bob placed = %d, want 1", got)
	}
}

func TestDurabilityTransferredOnApply(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	src := furn("lamp", "alice")
	src.Durability = 77
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{src}, base()))
	if _, err := g.Apply("bp1", "alice", "grass", base()); err != nil {
		must(t, err)
	}
	placed := g.Placed("alice")
	if len(placed) != 1 {
		t.Fatalf("placed = %d, want 1", len(placed))
	}
	if placed[0].Durability != 77 {
		t.Fatalf("durability = %d, want 77 copied from the blueprint", placed[0].Durability)
	}
}

func TestDeleteReclaimsPlacedFurniture(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("a", "alice"), furn("b", "alice")}, base()))
	if _, err := g.Apply("bp1", "alice", "grass", base()); err != nil {
		must(t, err)
	}
	reclaimed, err := g.Delete("bp1")
	must(t, err)
	if len(reclaimed) != 2 {
		t.Fatalf("reclaimed = %d, want 2", len(reclaimed))
	}
	if got := len(g.Placed("alice")); got != 0 {
		t.Fatalf("placed = %d after delete, want 0", got)
	}
	if _, err := g.Apply("bp1", "alice", "grass", base()); err == nil {
		t.Fatalf("a deleted blueprint must not be applicable")
	}
}

func TestTerrainMismatchRejected(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("tree", "alice")}, base()))
	if _, err := g.Apply("bp1", "alice", "sand", base()); err == nil {
		t.Fatalf("applying a grass layout on sand must fail")
	}
	if got := len(g.Placed("alice")); got != 0 {
		t.Fatalf("placed = %d after a terrain mismatch, want 0", got)
	}
}

func TestGrantAccessIsPerPlayer(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("a", "alice")}, base()))
	must(t, g.CreateBlueprint("bp2", "alice", []impl.Placement{furn("b", "alice")}, base()))
	must(t, g.SetShared("bp1", true))
	must(t, g.SetShared("bp2", true))
	must(t, g.GrantAccess("bp1", "bob"))
	if _, err := g.Apply("bp1", "bob", "grass", base()); err != nil {
		must(t, err)
	}
	if _, err := g.Apply("bp2", "carol", "grass", base()); err == nil {
		t.Fatalf("carol has no access to bp2 and must be rejected")
	}
	must(t, g.GrantAccess("bp2", "carol"))
	if _, err := g.Apply("bp2", "carol", "grass", base()); err != nil {
		must(t, err)
	}
}

func TestCapCountsExistingFurniture(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(3))
	must(t, g.CreateBlueprint("bp1", "alice", []impl.Placement{furn("a", "alice"), furn("b", "alice")}, base()))
	if _, err := g.Apply("bp1", "alice", "grass", base()); err != nil {
		must(t, err)
	}
	must(t, g.CreateBlueprint("bp2", "alice", []impl.Placement{furn("c", "alice"), furn("d", "alice")}, base()))
	if _, err := g.Apply("bp2", "alice", "grass", base().Add(time.Second)); err == nil {
		t.Fatalf("2 existing + 2 new items exceed the cap of 3")
	}
	if got := len(g.Placed("alice")); got != 2 {
		t.Fatalf("placed = %d, want the original 2 items", got)
	}
}

func TestApplyUnknownBlueprintFails(t *testing.T) {
	g := impl.NewBlueprintGuard()
	must(t, g.SetFurnitureCap(10))
	must(t, g.CreateBlueprint("known", "alice", []impl.Placement{furn("a", "alice")}, base()))
	if _, err := g.Apply("known", "alice", "grass", base()); err != nil {
		must(t, err)
	}
	if _, err := g.Apply("missing", "alice", "grass", base()); err == nil {
		t.Fatalf("applying an unknown blueprint must fail")
	}
}
