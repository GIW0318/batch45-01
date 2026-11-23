package task

import (
	"errors"
	"testing"

	impl "gsb/batch45-game-b-04"
)

func newDecalAtlas(t *testing.T, maxDecals, pages int) *impl.DecalAtlas {
	t.Helper()
	a, err := impl.NewDecalAtlas(maxDecals, pages)
	if err != nil {
		t.Fatalf("NewDecalAtlas error: %v", err)
	}
	return a
}

func addDecal(t *testing.T, a *impl.DecalAtlas, id string, page int, pos impl.Vec3) int {
	t.Helper()
	slot, err := a.Add(id, page, pos, impl.Vec3{Y: 1})
	if err != nil {
		t.Fatalf("Add(%s) error: %v", id, err)
	}
	return slot
}

func translationOf(m [16]float64) impl.Vec3 {
	return impl.Vec3{X: m[12], Y: m[13], Z: m[14]}
}

func TestProjectionFollowsMove(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{})
	before, err := a.ProjectionMatrix("d1")
	if err != nil {
		t.Fatalf("ProjectionMatrix error: %v", err)
	}
	if got := translationOf(before); got != (impl.Vec3{}) {
		t.Fatalf("initial translation = %+v, want origin", got)
	}
	if err := a.Move("d1", impl.Vec3{X: 3, Y: 4, Z: 5}); err != nil {
		t.Fatalf("Move error: %v", err)
	}
	after, err := a.ProjectionMatrix("d1")
	if err != nil {
		t.Fatalf("ProjectionMatrix after move error: %v", err)
	}
	got := translationOf(after)
	want := impl.Vec3{X: 3, Y: 4, Z: 5}
	if got != want {
		t.Fatalf("projection translation = %+v, want %+v (matrix not updated with the object)", got, want)
	}
}

func TestSortBackToFront(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "near", 0, impl.Vec3{Z: 1})
	addDecal(t, a, "far", 1, impl.Vec3{Z: 10})
	order := a.SortedIDs(impl.Vec3{})
	if len(order) != 2 {
		t.Fatalf("sorted decals = %v, want 2 entries", order)
	}
	if order[0] != "far" {
		t.Fatalf("sorted order = %v, want the far decal first", order)
	}
}

func TestSortByDepthNotIDOrder(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "aaa", 0, impl.Vec3{Z: 1})
	addDecal(t, a, "zzz", 1, impl.Vec3{Z: 10})
	order := a.SortedIDs(impl.Vec3{})
	if len(order) != 2 {
		t.Fatalf("sorted decals = %v, want 2 entries", order)
	}
	if order[0] != "zzz" || order[1] != "aaa" {
		t.Fatalf("sorted order = %v, want depth order [zzz aaa] and not id order", order)
	}
}

func TestPageFreedOnDelete(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	if got := a.FreePages(); got != 4 {
		t.Fatalf("free pages = %d, want 4", got)
	}
	addDecal(t, a, "d1", 0, impl.Vec3{})
	if got := a.FreePages(); got != 3 {
		t.Fatalf("free pages after add = %d, want 3", got)
	}
	if err := a.Delete("d1"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if got := a.FreePages(); got != 4 {
		t.Fatalf("free pages after delete = %d, want 4 (page not released)", got)
	}
}

func TestProjectionCoversAllMeshes(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{})
	got, err := a.ProjectMeshes("d1", 4)
	if err != nil {
		t.Fatalf("ProjectMeshes error: %v", err)
	}
	if got != 4 {
		t.Fatalf("projected meshes = %d, want 4", got)
	}
}

func TestNormalBlendOrientation(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	same := a.BlendNormal(impl.Vec3{Y: 1}, impl.Vec3{Y: 1})
	if same.Y < 0.5 {
		t.Fatalf("blend of aligned normals = %+v, want +Y", same)
	}
	side := a.BlendNormal(impl.Vec3{Y: 1}, impl.Vec3{X: 1})
	if side.X <= 0 {
		t.Fatalf("blend toward +X decal normal = %+v, direction inverted", side)
	}
}

func TestDecalPassSeparatedFromParticles(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{Z: 2})
	if err := a.AddParticle("p1", impl.Vec3{Z: 3}); err != nil {
		t.Fatalf("AddParticle error: %v", err)
	}
	if got := a.PassOf("d1"); got != "decal" {
		t.Fatalf("decal pass = %q, want decal", got)
	}
	if got := a.PassOf("p1"); got != "particles" {
		t.Fatalf("particle pass = %q, want particles", got)
	}
	if got := len(a.SortedIDs(impl.Vec3{})); got != 1 {
		t.Fatalf("decal sort list length = %d, want 1 (particles must not mix in)", got)
	}
}

func TestCullUsesBoundingBoxNotCenter(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{X: 100, Y: 100, Z: 100})
	if err := a.SetBounds("d1", impl.Vec3{X: -2, Y: -2, Z: -2}, impl.Vec3{X: 2, Y: 2, Z: 2}); err != nil {
		t.Fatalf("SetBounds error: %v", err)
	}
	visible, err := a.Visible("d1", impl.Vec3{}, impl.Vec3{X: 10, Y: 10, Z: 10})
	if err != nil {
		t.Fatalf("Visible error: %v", err)
	}
	if !visible {
		t.Fatalf("decal with overlapping bounding box was culled by its center")
	}
}

func TestOverLimitRejectsNewDecal(t *testing.T) {
	a := newDecalAtlas(t, 2, 8)
	addDecal(t, a, "d1", 0, impl.Vec3{})
	addDecal(t, a, "d2", 1, impl.Vec3{})
	if _, err := a.Add("d3", 2, impl.Vec3{}, impl.Vec3{Y: 1}); !errors.Is(err, impl.ErrDecalLimit) {
		t.Fatalf("add beyond limit error = %v, want ErrDecalLimit", err)
	}
	if got := a.Count(); got != 2 {
		t.Fatalf("decal count = %d, want 2", got)
	}
	if _, ok := a.SlotOf("d1"); !ok {
		t.Fatalf("oldest decal was overwritten when the limit was hit")
	}
}

func TestFollowsMovingPlatform(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{})
	if err := a.AddPlatform("plat-1", impl.Vec3{}); err != nil {
		t.Fatalf("AddPlatform error: %v", err)
	}
	if err := a.AttachToPlatform("d1", "plat-1"); err != nil {
		t.Fatalf("AttachToPlatform error: %v", err)
	}
	if err := a.MovePlatform("plat-1", impl.Vec3{X: 7}); err != nil {
		t.Fatalf("MovePlatform error: %v", err)
	}
	m, err := a.ProjectionMatrix("d1")
	if err != nil {
		t.Fatalf("ProjectionMatrix error: %v", err)
	}
	if got := translationOf(m); got.X != 7 {
		t.Fatalf("decal x = %v, want 7 after platform moved", got.X)
	}
}

func TestUVRecalculatedOnAtlasUpdate(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 1, impl.Vec3{})
	u0, _, u1, _, err := a.UVRect("d1")
	if err != nil {
		t.Fatalf("UVRect error: %v", err)
	}
	if err := a.UpdateAtlas(8); err != nil {
		t.Fatalf("UpdateAtlas error: %v", err)
	}
	nu0, _, nu1, _, err := a.UVRect("d1")
	if err != nil {
		t.Fatalf("UVRect after update error: %v", err)
	}
	if nu0 == u0 && nu1 == u1 {
		t.Fatalf("UV rect did not change after atlas grew: %v..%v", nu0, nu1)
	}
	if nu0 < 0 || nu1 > 1 {
		t.Fatalf("UV rect %v..%v outside [0,1]", nu0, nu1)
	}
}

func TestConcurrentSlotWriteRejected(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{})
	if err := a.BeginWrite(0); err != nil {
		t.Fatalf("first BeginWrite error: %v", err)
	}
	if err := a.BeginWrite(0); !errors.Is(err, impl.ErrSlotBusy) {
		t.Fatalf("second BeginWrite error = %v, want ErrSlotBusy", err)
	}
}

func TestDeleteRestoresDepthBuffer(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{})
	if err := a.Delete("d1"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if !a.DepthRestored("d1") {
		t.Fatalf("depth buffer not restored after decal delete")
	}
}

func TestDeleteUnknownErrors(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	if err := a.Delete("missing"); !errors.Is(err, impl.ErrUnknownDecal) {
		t.Fatalf("delete unknown decal error = %v, want ErrUnknownDecal", err)
	}
}

func TestSlotRecycledAfterDelete(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	slot := addDecal(t, a, "d1", 0, impl.Vec3{})
	if err := a.Delete("d1"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	newSlot := addDecal(t, a, "d2", 1, impl.Vec3{})
	if newSlot != slot {
		t.Fatalf("recycled slot = %d, want %d", newSlot, slot)
	}
	if got, ok := a.SlotOf("d2"); !ok || got != slot {
		t.Fatalf("SlotOf(d2) = %d (ok=%v), want %d", got, ok, slot)
	}
}
