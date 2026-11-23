package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch45-game-b-04"
)

func TestSelfConcurrentBeginWriteSameSlot(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	slot := addDecal(t, a, "d1", 0, impl.Vec3{})

	const writers = 32
	start := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, writers)

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			<-start
			err := a.BeginWrite(slot)
			results <- err
			if err == nil {
				<-release
			}
		}()
	}

	close(start)
	successes := 0
	busy := 0
	for i := 0; i < writers; i++ {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, impl.ErrSlotBusy):
			busy++
		default:
			t.Fatalf("BeginWrite returned unexpected error: %v", err)
		}
	}
	if successes != 1 || busy != writers-1 {
		close(release)
		wg.Wait()
		t.Fatalf("BeginWrite successes=%d busy=%d, want 1 and %d", successes, busy, writers-1)
	}

	close(release)
	wg.Wait()
	if err := a.EndWrite(slot); err != nil {
		t.Fatalf("EndWrite error: %v", err)
	}
	if err := a.BeginWrite(slot); err != nil {
		t.Fatalf("BeginWrite after EndWrite error: %v", err)
	}
	if err := a.EndWrite(slot); err != nil {
		t.Fatalf("final EndWrite error: %v", err)
	}
}

func TestSelfBatchFadeAndReclaim(t *testing.T) {
	a := newDecalAtlas(t, 8, 8)
	firstSlot := addDecal(t, a, "d1", 0, impl.Vec3{Z: 3})
	secondSlot := addDecal(t, a, "d2", 1, impl.Vec3{Z: 2})
	survivorSlot := addDecal(t, a, "d3", 2, impl.Vec3{Z: 1})

	u0, v0, u1, v1, err := a.UVRect("d3")
	if err != nil {
		t.Fatalf("UVRect before fade error: %v", err)
	}

	if err := a.FadeOut("d1", "d2"); err != nil {
		t.Fatalf("FadeOut error: %v", err)
	}
	if !a.IsFading("d1") || !a.IsFading("d2") {
		t.Fatalf("batch fade did not mark both decals")
	}
	if order := a.SortedIDs(impl.Vec3{}); len(order) != 1 || order[0] != "d3" {
		t.Fatalf("SortedIDs during fade = %v, want [d3]", order)
	}
	if _, err := a.ProjectMeshes("d1", 4); !errors.Is(err, impl.ErrDecalFading) {
		t.Fatalf("ProjectMeshes(fading) error = %v, want ErrDecalFading", err)
	}
	if _, ok := a.SlotOf("d1"); !ok {
		t.Fatalf("fading decal slot is no longer queryable")
	}
	if _, _, _, _, err := a.UVRect("d1"); err != nil {
		t.Fatalf("fading decal UV is no longer queryable: %v", err)
	}
	if a.DepthRestored("d1") {
		t.Fatalf("depth restored before the fading decal was reclaimed")
	}

	if got := a.ReclaimFaded(); got != 2 {
		t.Fatalf("ReclaimFaded = %d, want 2", got)
	}
	if got := a.Count(); got != 1 {
		t.Fatalf("decal count after reclaim = %d, want 1", got)
	}
	if got := a.FreePages(); got != 7 {
		t.Fatalf("free pages after reclaim = %d, want 7", got)
	}
	if !a.DepthRestored("d1") || !a.DepthRestored("d2") {
		t.Fatalf("depth buffer was not restored for reclaimed decals")
	}
	if _, ok := a.SlotOf("d1"); ok {
		t.Fatalf("reclaimed decal still holds a slot")
	}
	if got, ok := a.SlotOf("d3"); !ok || got != survivorSlot {
		t.Fatalf("survivor slot = %d (ok=%v), want %d", got, ok, survivorSlot)
	}

	nu0, nv0, nu1, nv1, err := a.UVRect("d3")
	if err != nil {
		t.Fatalf("UVRect after reclaim error: %v", err)
	}
	if nu0 != u0 || nv0 != v0 || nu1 != u1 || nv1 != v1 {
		t.Fatalf("survivor UV changed during reclaim: %v..%v -> %v..%v", u0, u1, nu0, nu1)
	}

	reused, err := a.Add("d4", 3, impl.Vec3{}, impl.Vec3{Y: 1})
	if err != nil {
		t.Fatalf("Add after reclaim error: %v", err)
	}
	if reused != secondSlot {
		t.Fatalf("recycled slot = %d, want %d (most recently reclaimed slot)", reused, secondSlot)
	}
	if reused == firstSlot {
		t.Fatalf("recycled slot unexpectedly used the oldest reclaimed slot")
	}
}

func TestSelfFadeBatchIsAtomic(t *testing.T) {
	a := newDecalAtlas(t, 8, 4)
	addDecal(t, a, "d1", 0, impl.Vec3{})
	addDecal(t, a, "d2", 1, impl.Vec3{})

	if err := a.FadeOut("d1", "missing"); !errors.Is(err, impl.ErrUnknownDecal) {
		t.Fatalf("FadeOut with unknown id error = %v, want ErrUnknownDecal", err)
	}
	if a.IsFading("d1") || a.IsFading("d2") {
		t.Fatalf("failed batch changed fade state")
	}
	if got := a.Count(); got != 2 {
		t.Fatalf("decal count after failed batch = %d, want 2", got)
	}
}

func TestSelfPagePoolExhausted(t *testing.T) {
	a := newDecalAtlas(t, 8, 1)
	addDecal(t, a, "d1", 0, impl.Vec3{})

	if _, err := a.Add("d2", 0, impl.Vec3{}, impl.Vec3{Y: 1}); !errors.Is(err, impl.ErrDecalPoolFull) {
		t.Fatalf("Add beyond page pool error = %v, want ErrDecalPoolFull", err)
	}
	if got := a.Count(); got != 1 {
		t.Fatalf("decal count after pool exhaustion = %d, want 1", got)
	}
	if _, ok := a.SlotOf("d1"); !ok {
		t.Fatalf("existing decal lost its slot when the page pool was exhausted")
	}
}

func TestSelfMoveUpdatesSortAndPlatformBounds(t *testing.T) {
	a := newDecalAtlas(t, 4, 4)
	addDecal(t, a, "a", 0, impl.Vec3{Z: 1})
	addDecal(t, a, "b", 1, impl.Vec3{Z: 9})

	if got := a.SortedIDs(impl.Vec3{}); len(got) != 2 || got[0] != "b" {
		t.Fatalf("initial order = %v, want b nearest-to-far first", got)
	}
	if err := a.Move("a", impl.Vec3{Z: 20}); err != nil {
		t.Fatalf("Move error: %v", err)
	}
	if got := a.SortedIDs(impl.Vec3{}); got[0] != "a" {
		t.Fatalf("order after Move = %v, want the moved decal first", got)
	}

	if err := a.SetBounds("a", impl.Vec3{X: -1, Y: -1, Z: 19}, impl.Vec3{X: 1, Y: 1, Z: 21}); err != nil {
		t.Fatalf("SetBounds error: %v", err)
	}
	if err := a.AddPlatform("p", impl.Vec3{}); err != nil {
		t.Fatalf("AddPlatform error: %v", err)
	}
	if err := a.AttachToPlatform("a", "p"); err != nil {
		t.Fatalf("AttachToPlatform error: %v", err)
	}
	if err := a.MovePlatform("p", impl.Vec3{X: 5}); err != nil {
		t.Fatalf("MovePlatform error: %v", err)
	}
	visible, err := a.Visible("a", impl.Vec3{X: 4, Y: -1, Z: 19}, impl.Vec3{X: 6, Y: 1, Z: 21})
	if err != nil {
		t.Fatalf("Visible error: %v", err)
	}
	if !visible {
		t.Fatalf("decal bounds did not follow the platform move")
	}
}
