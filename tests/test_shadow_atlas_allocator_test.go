package task

import (
	"errors"
	"testing"

	impl "gsb/batch45-game-b-02"
)

func newAtlas(t *testing.T, w, h, cell int) *impl.ShadowAtlas {
	t.Helper()
	a, err := impl.NewShadowAtlas(w, h, cell)
	if err != nil {
		t.Fatalf("NewShadowAtlas error: %v", err)
	}
	return a
}

func alloc(t *testing.T, a *impl.ShadowAtlas, id string) impl.Page {
	t.Helper()
	p, err := a.Allocate(id, 64, 64)
	if err != nil {
		t.Fatalf("Allocate(%s) error: %v", id, err)
	}
	return p
}

func overlaps(a, b impl.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

func TestAllocationAlignedAndNonOverlapping(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	p1 := alloc(t, a, "l1")
	p2 := alloc(t, a, "l2")
	if p1.Rect.X%64 != 0 || p1.Rect.Y%64 != 0 {
		t.Fatalf("page rect %+v is not 64 aligned", p1.Rect)
	}
	if p1.Rect.W < 64 || p1.Rect.H < 64 {
		t.Fatalf("page size %+v smaller than requested", p1.Rect)
	}
	if overlaps(p1.Rect, p2.Rect) {
		t.Fatalf("allocated pages overlap: %+v and %+v", p1.Rect, p2.Rect)
	}
}

func TestDuplicateAllocationRejected(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	alloc(t, a, "l1")
	if _, err := a.Allocate("l1", 64, 64); !errors.Is(err, impl.ErrAlreadyAllocated) {
		t.Fatalf("duplicate allocation error = %v, want ErrAlreadyAllocated", err)
	}
	if got := len(a.Pages()); got != 1 {
		t.Fatalf("page count = %d, want 1", got)
	}
}

func TestReleaseClearsDepth(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	p := alloc(t, a, "l1")
	if err := a.Release("l1"); err != nil {
		t.Fatalf("Release error: %v", err)
	}
	released, ok := a.PageAt(p.Index)
	if !ok {
		t.Fatalf("page slot %d vanished after release", p.Index)
	}
	if !released.Cleared {
		t.Fatalf("released page still holds depth content")
	}
}

func TestDistinctPagesForDistinctLights(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	seen := map[int]bool{}
	for _, id := range []string{"l1", "l2", "l3"} {
		p := alloc(t, a, id)
		if seen[p.Index] {
			t.Fatalf("page index %d handed out twice", p.Index)
		}
		seen[p.Index] = true
	}
	if got := len(a.Pages()); got != 3 {
		t.Fatalf("page count = %d, want 3", got)
	}
}

func TestCascadePagesTracked(t *testing.T) {
	a := newAtlas(t, 2048, 2048, 64)
	for cascade, id := range []string{"c0", "c1", "c2"} {
		if _, err := a.AllocateCascade(id, 64, 64, cascade); err != nil {
			t.Fatalf("AllocateCascade(%s) error: %v", id, err)
		}
	}
	for cascade, id := range []string{"c0", "c1", "c2"} {
		p, ok := a.PageOf(id)
		if !ok {
			t.Fatalf("cascade page %s missing", id)
		}
		if p.Cascade != cascade {
			t.Fatalf("page %s cascade = %d, want %d", id, p.Cascade, cascade)
		}
	}
}

func TestStaticAndDynamicRegionsSeparated(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	sp, err := a.AllocateIn("static-1", 64, 64, impl.RegionStatic)
	if err != nil {
		t.Fatalf("AllocateIn(static) error: %v", err)
	}
	dp, err := a.AllocateIn("dynamic-1", 64, 64, impl.RegionDynamic)
	if err != nil {
		t.Fatalf("AllocateIn(dynamic) error: %v", err)
	}
	if sp.Region != impl.RegionStatic {
		t.Fatalf("static page region = %v", sp.Region)
	}
	if dp.Region != impl.RegionDynamic {
		t.Fatalf("dynamic page region = %v", dp.Region)
	}
	if overlaps(sp.Rect, dp.Rect) {
		t.Fatalf("static and dynamic pages overlap: %+v vs %+v", sp.Rect, dp.Rect)
	}
	if sp.Rect.Y >= dp.Rect.Y {
		t.Fatalf("static region at y=%d must sit above dynamic region at y=%d", sp.Rect.Y, dp.Rect.Y)
	}
}

func TestUVInsetByBorder(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	alloc(t, a, "l1")
	u0, _, u1, _, err := a.UVRect("l1")
	if err != nil {
		t.Fatalf("UVRect error: %v", err)
	}
	if u1-u0 >= 64.0/1024.0 {
		t.Fatalf("uv width %v has no border padding for a 64px page", u1-u0)
	}
	if u1-u0 <= 0 {
		t.Fatalf("uv width %v is not positive", u1-u0)
	}
}

func TestOverflowDoesNotOverwrite(t *testing.T) {
	a := newAtlas(t, 128, 128, 64)
	first := alloc(t, a, "l0")
	for i := 1; i < 4; i++ {
		alloc(t, a, "l"+string(rune('0'+i)))
	}
	if _, err := a.Allocate("l-extra", 64, 64); !errors.Is(err, impl.ErrAtlasFull) {
		t.Fatalf("allocation on full atlas error = %v, want ErrAtlasFull", err)
	}
	if a.Overflow() == 0 {
		t.Fatalf("overflow was not counted")
	}
	kept, ok := a.PageOf("l0")
	if !ok {
		t.Fatalf("existing page was overwritten")
	}
	if kept.Rect != first.Rect {
		t.Fatalf("existing page moved from %+v to %+v", first.Rect, kept.Rect)
	}
}

func TestReleasedPageRecycled(t *testing.T) {
	a := newAtlas(t, 256, 256, 64)
	p := alloc(t, a, "l1")
	if err := a.Release("l1"); err != nil {
		t.Fatalf("Release error: %v", err)
	}
	reused := alloc(t, a, "l2")
	if reused.Index != p.Index {
		t.Fatalf("recycled page index = %d, want %d", reused.Index, p.Index)
	}
}

func TestResizeRecomputesUV(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	alloc(t, a, "l1")
	u0, _, u1, _, err := a.UVRect("l1")
	if err != nil {
		t.Fatalf("UVRect error: %v", err)
	}
	if err := a.Resize(2048, 2048); err != nil {
		t.Fatalf("Resize error: %v", err)
	}
	nu0, _, nu1, _, err := a.UVRect("l1")
	if err != nil {
		t.Fatalf("UVRect after resize error: %v", err)
	}
	if nu0 == u0 && nu1 == u1 {
		t.Fatalf("UV rect did not change after atlas resize: %v..%v", nu0, nu1)
	}
	if nu0 < 0 || nu1 > 1 {
		t.Fatalf("UV rect %v..%v outside [0,1]", nu0, nu1)
	}
}

func TestBarrierAfterUpdate(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	alloc(t, a, "l1")
	if err := a.BeginUpdate("l1"); err != nil {
		t.Fatalf("BeginUpdate error: %v", err)
	}
	if err := a.EndUpdate("l1"); err != nil {
		t.Fatalf("EndUpdate error: %v", err)
	}
	if a.BarrierCount() < 1 {
		t.Fatalf("no barrier recorded between atlas update and sampling")
	}
}

func TestRebuildRejectsInflightPages(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	alloc(t, a, "l1")
	if err := a.BeginUpdate("l1"); err != nil {
		t.Fatalf("BeginUpdate error: %v", err)
	}
	if err := a.Rebuild(); !errors.Is(err, impl.ErrAtlasBusy) {
		t.Fatalf("rebuild error = %v, want ErrAtlasBusy", err)
	}
	if err := a.EndUpdate("l1"); err != nil {
		t.Fatalf("EndUpdate error: %v", err)
	}
	if err := a.Rebuild(); err != nil {
		t.Fatalf("rebuild after end update error: %v", err)
	}
}

func TestReleaseUnknownErrors(t *testing.T) {
	a := newAtlas(t, 256, 256, 64)
	if err := a.Release("missing"); !errors.Is(err, impl.ErrUnknownLight) {
		t.Fatalf("release unknown light error = %v, want ErrUnknownLight", err)
	}
}

func TestUVBoundsOrdered(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	alloc(t, a, "l1")
	u0, v0, u1, v1, err := a.UVRect("l1")
	if err != nil {
		t.Fatalf("UVRect error: %v", err)
	}
	if !(u0 < u1) || !(v0 < v1) {
		t.Fatalf("uv rect not ordered: (%v,%v)-(%v,%v)", u0, v0, u1, v1)
	}
	if u0 < 0 || v0 < 0 || u1 > 1 || v1 > 1 {
		t.Fatalf("uv rect outside [0,1]: (%v,%v)-(%v,%v)", u0, v0, u1, v1)
	}
}

func TestConcurrentUpdateRejected(t *testing.T) {
	a := newAtlas(t, 1024, 1024, 64)
	alloc(t, a, "l1")
	if err := a.BeginUpdate("l1"); err != nil {
		t.Fatalf("first BeginUpdate error: %v", err)
	}
	if err := a.BeginUpdate("l1"); !errors.Is(err, impl.ErrPageBusy) {
		t.Fatalf("second BeginUpdate error = %v, want ErrPageBusy", err)
	}
}
