package task

import (
	"errors"
	"math"
	"testing"

	impl "gsb/batch45-game-b-01"
)

func boxCamera(min, max float64) impl.Camera {
	return impl.Camera{
		Min: impl.Vec3{X: min, Y: min, Z: min},
		Max: impl.Vec3{X: max, Y: max, Z: max},
	}
}

func newBinner(t *testing.T, cam impl.Camera) *impl.LightBinner {
	t.Helper()
	b, err := impl.NewLightBinner(cam, 2, 2, 2)
	if err != nil {
		t.Fatalf("NewLightBinner error: %v", err)
	}
	return b
}

func addLight(t *testing.T, b *impl.LightBinner, l impl.Light) int {
	t.Helper()
	idx, err := b.AddLight(l)
	if err != nil {
		t.Fatalf("AddLight(%s) error: %v", l.ID, err)
	}
	return idx
}

func binnedCount(clusters map[impl.ClusterKey][]int, idx int) int {
	n := 0
	for _, idxs := range clusters {
		for _, got := range idxs {
			if got == idx {
				n++
			}
		}
	}
	return n
}

func TestLightsOutsideFrustumCulled(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	nearIdx := addLight(t, b, impl.Light{ID: "near", Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	farIdx := addLight(t, b, impl.Light{ID: "far", Type: impl.PointLight, Position: impl.Vec3{X: 100, Y: 100, Z: 100}, Radius: 1, Intensity: 1})
	clusters := b.Clusters()
	if binnedCount(clusters, nearIdx) == 0 {
		t.Fatalf("visible light was not binned into any cluster")
	}
	if binnedCount(clusters, farIdx) != 0 {
		t.Fatalf("light outside the frustum was binned")
	}
}

func TestBoundaryLightIncluded(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	idx := addLight(t, b, impl.Light{ID: "edge", Type: impl.PointLight, Position: impl.Vec3{X: 11, Y: 5, Z: 5}, Radius: 2, Intensity: 1})
	if binnedCount(b.Clusters(), idx) == 0 {
		t.Fatalf("light touching the tile boundary was dropped")
	}
}

func TestLightDedupedPerCluster(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	idx := addLight(t, b, impl.Light{ID: "big", Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 6, Intensity: 1})
	total := 0
	for _, idxs := range b.Clusters() {
		seen := 0
		for _, got := range idxs {
			if got == idx {
				seen++
			}
		}
		if seen > 1 {
			t.Fatalf("light counted %d times in one cluster", seen)
		}
		total += seen
	}
	if total < 2 {
		t.Fatalf("large light was binned into %d clusters, want several", total)
	}
}

func TestClusterKeysWithinGrid(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	addLight(t, b, impl.Light{ID: "near", Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	clusters := b.Clusters()
	if len(clusters) == 0 {
		t.Fatalf("no clusters were produced")
	}
	for k := range clusters {
		if k.X < 0 || k.X >= 2 || k.Y < 0 || k.Y >= 2 || k.Z < 0 || k.Z >= 2 {
			t.Fatalf("cluster key %+v outside grid [0,2)", k)
		}
	}
}

func TestPointAttenuationValue(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	l := impl.Light{ID: "p", Type: impl.PointLight, Position: impl.Vec3{}, Radius: 10, Intensity: 1}
	got := b.Attenuation(l, impl.Vec3{X: 1})
	if math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("point attenuation at distance 1 = %v, want 0.5", got)
	}
}

func TestSpotOutsideConeZero(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	spot := impl.Light{ID: "s", Type: impl.SpotLight, Position: impl.Vec3{}, Direction: impl.Vec3{Z: -1}, Radius: 10, CosCutoff: 0.5, Intensity: 1}
	inside := b.Attenuation(spot, impl.Vec3{Z: -1})
	if inside <= 0.4 {
		t.Fatalf("spot attenuation inside cone = %v, want about 0.5", inside)
	}
	outside := b.Attenuation(spot, impl.Vec3{Z: 1})
	if outside != 0 {
		t.Fatalf("spot attenuation outside cone = %v, want 0", outside)
	}
}

func TestOverflowReportedNotSilent(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	if err := b.SetClusterCapacity(2); err != nil {
		t.Fatalf("SetClusterCapacity error: %v", err)
	}
	for i := 0; i < 5; i++ {
		addLight(t, b, impl.Light{ID: "l" + string(rune('a'+i)), Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	}
	if b.Overflows() == 0 {
		t.Fatalf("capacity overflow was not reported")
	}
	for k, idxs := range b.Clusters() {
		if len(idxs) > 2 {
			t.Fatalf("cluster %+v holds %d lights, capacity is 2", k, len(idxs))
		}
	}
}

func TestShadowSlotMatchesLightIndex(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	for i := 0; i < 3; i++ {
		addLight(t, b, impl.Light{ID: "l" + string(rune('a'+i)), Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	}
	for i := 0; i < 3; i++ {
		if got := b.ShadowSlot(i); got != i {
			t.Fatalf("shadow slot of light %d = %d, want %d", i, got, i)
		}
	}
}

func TestShadowSlotsDenseAfterRemoval(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	for i := 0; i < 3; i++ {
		addLight(t, b, impl.Light{ID: "l" + string(rune('a'+i)), Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	}
	if err := b.RemoveLight("lb"); err != nil {
		t.Fatalf("RemoveLight error: %v", err)
	}
	slots := map[int]bool{b.ShadowSlot(0): true, b.ShadowSlot(2): true}
	if len(slots) != 2 {
		t.Fatalf("shadow slots collided after removal: %v", slots)
	}
}

func TestTransparentPassReceivesLights(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	addLight(t, b, impl.Light{ID: "a", Type: impl.PointLight, Position: impl.Vec3{X: 2, Y: 2, Z: 2}, Radius: 1, Intensity: 1})
	addLight(t, b, impl.Light{ID: "b", Type: impl.PointLight, Position: impl.Vec3{X: 8, Y: 8, Z: 8}, Radius: 1, Intensity: 1})
	addLight(t, b, impl.Light{ID: "c", Type: impl.PointLight, Position: impl.Vec3{X: 90, Y: 90, Z: 90}, Radius: 1, Intensity: 1})
	if got := b.TransparentPassLights(); got != 2 {
		t.Fatalf("transparent pass lights = %d, want 2", got)
	}
}

func TestCameraChangeRebuildsImmediately(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	idx := addLight(t, b, impl.Light{ID: "a", Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	if binnedCount(b.Clusters(), idx) == 0 {
		t.Fatalf("light missing before camera change")
	}
	if err := b.SetCamera(boxCamera(1000, 1010)); err != nil {
		t.Fatalf("SetCamera error: %v", err)
	}
	if binnedCount(b.Clusters(), idx) != 0 {
		t.Fatalf("clusters still use the previous camera")
	}
}

func TestGridResolutionConfigurable(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	if err := b.SetGrid(4, 4, 4); err != nil {
		t.Fatalf("SetGrid error: %v", err)
	}
	nx, ny, nz := b.Grid()
	if nx != 4 || ny != 4 || nz != 4 {
		t.Fatalf("grid = %dx%dx%d, want 4x4x4", nx, ny, nz)
	}
	if err := b.SetGrid(0, 4, 4); !errors.Is(err, impl.ErrInvalidGrid) {
		t.Fatalf("SetGrid(0,4,4) error = %v, want ErrInvalidGrid", err)
	}
}

func TestMovingLightRebuildsClusters(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	if err := b.SetGrid(4, 4, 4); err != nil {
		t.Fatalf("SetGrid error: %v", err)
	}
	idx := addLight(t, b, impl.Light{ID: "m", Type: impl.PointLight, Position: impl.Vec3{X: 1, Y: 1, Z: 1}, Radius: 0.5, Intensity: 1})
	oldKey := impl.ClusterKey{X: 0, Y: 0, Z: 0}
	if !clusterHas(b.ClusterAt(oldKey), idx) {
		t.Fatalf("light not binned at its start cluster")
	}
	if err := b.UpdateLight("m", impl.Light{ID: "m", Type: impl.PointLight, Position: impl.Vec3{X: 9, Y: 9, Z: 9}, Radius: 0.5, Intensity: 1}); err != nil {
		t.Fatalf("UpdateLight error: %v", err)
	}
	newKey := impl.ClusterKey{X: 3, Y: 3, Z: 3}
	if clusterHas(b.ClusterAt(oldKey), idx) {
		t.Fatalf("light still binned in the old cluster after moving")
	}
	if !clusterHas(b.ClusterAt(newKey), idx) {
		t.Fatalf("light not binned in the new cluster after moving")
	}
}

func TestRemoveLightRecyclesIndex(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	addLight(t, b, impl.Light{ID: "a", Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	addLight(t, b, impl.Light{ID: "b", Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	if err := b.RemoveLight("a"); err != nil {
		t.Fatalf("RemoveLight error: %v", err)
	}
	newIdx := addLight(t, b, impl.Light{ID: "c", Type: impl.PointLight, Position: impl.Vec3{X: 5, Y: 5, Z: 5}, Radius: 1, Intensity: 1})
	if newIdx != 0 {
		t.Fatalf("recycled light index = %d, want 0", newIdx)
	}
	if _, ok := b.LightIndex("a"); ok {
		t.Fatalf("removed light still indexed")
	}
	if got, ok := b.LightIndex("c"); !ok || got != 0 {
		t.Fatalf("new light index = %d (ok=%v), want 0", got, ok)
	}
}

func TestUpdateLightKeepsShadowSlot(t *testing.T) {
	b := newBinner(t, boxCamera(0, 10))
	addLight(t, b, impl.Light{ID: "a", Type: impl.PointLight, Position: impl.Vec3{X: 2, Y: 2, Z: 2}, Radius: 1, Intensity: 1})
	addLight(t, b, impl.Light{ID: "b", Type: impl.PointLight, Position: impl.Vec3{X: 8, Y: 8, Z: 8}, Radius: 1, Intensity: 1})
	if err := b.UpdateLight("b", impl.Light{ID: "b", Type: impl.PointLight, Position: impl.Vec3{X: 7, Y: 7, Z: 7}, Radius: 1, Intensity: 1}); err != nil {
		t.Fatalf("UpdateLight error: %v", err)
	}
	if got := b.ShadowSlot(1); got != 1 {
		t.Fatalf("shadow slot of updated light = %d, want 1", got)
	}
}

func clusterHas(idxs []int, idx int) bool {
	for _, got := range idxs {
		if got == idx {
			return true
		}
	}
	return false
}
