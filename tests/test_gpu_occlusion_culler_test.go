package task

import (
	"testing"

	task "gsb/batch45-game-b-06"
)

func hasID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestDilateDepthExpandsOccluderSilhouette(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	depth := make([]float32, 25)
	depth[12] = 1
	out, err := c.DilateDepth(depth, 5, 5, 1)
	if err != nil {
		t.Fatalf("DilateDepth returned error: %v", err)
	}
	if len(out) != 25 {
		t.Fatalf("dilated buffer has %d texels, want 25", len(out))
	}
	expanded := 0
	for i, d := range out {
		if i != 12 && d != 0 {
			expanded++
		}
	}
	if expanded == 0 {
		t.Fatalf("radius 1 dilation kept the occluder at a single texel, silhouette not widened")
	}
}

func TestDilateDepthRejectsDimensionMismatch(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	depth := make([]float32, 9)
	depth[4] = 1
	if _, err := c.DilateDepth(depth, 3, 3, 1); err != nil {
		t.Fatalf("DilateDepth rejected a well formed 3x3 buffer: %v", err)
	}
	if _, err := c.DilateDepth(make([]float32, 5), 3, 3, 1); err == nil {
		t.Fatalf("DilateDepth accepted 5 texels for a 3x3 buffer")
	}
}

func TestHiZLevelCountMatchesReduction(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	p, err := c.BuildHiZ(make([]float32, 64), 8, 8)
	if err != nil {
		t.Fatalf("BuildHiZ returned error: %v", err)
	}
	if len(p.Levels) != 4 {
		t.Fatalf("pyramid has %d levels, want 4 for an 8x8 buffer", len(p.Levels))
	}
	if p.Widths[0] != 8 || p.Widths[3] != 1 {
		t.Fatalf("pyramid widths = %v, want level 0 = 8 and level 3 = 1", p.Widths)
	}
}

func TestHiZConservativeRangeCoversChildren(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	depth := make([]float32, 64)
	depth[0] = 0.25
	depth[63] = 0.75
	p, err := c.BuildHiZ(depth, 8, 8)
	if err != nil {
		t.Fatalf("BuildHiZ returned error: %v", err)
	}
	lo, hi, err := p.ConservativeRange(0, 0, 0)
	if err != nil {
		t.Fatalf("ConservativeRange level 0: %v", err)
	}
	if lo > 0.25 || hi < 0.25 {
		t.Fatalf("level 0 range [%v,%v] does not cover texel depth 0.25", lo, hi)
	}
	_, coarse, err := p.ConservativeRange(2, 0, 0)
	if err != nil {
		t.Fatalf("ConservativeRange level 2: %v", err)
	}
	if coarse < hi {
		t.Fatalf("coarser level range %v is tighter than its children %v, not conservative", coarse, hi)
	}
}

func TestCameraInsideOccluderKeepsEverythingVisible(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	occ := task.AABB{Min: task.Vec3{X: -1, Y: -1, Z: -1}, Max: task.Vec3{X: 1, Y: 1, Z: 1}}
	cam := task.Vec3{X: 0, Y: 0, Z: 0}
	if !c.CameraInsideOccluder(cam, occ) {
		t.Fatalf("CameraInsideOccluder missed a camera at the centre of the box")
	}
	objs := []task.Object{
		{ID: 1, Occluder: true, Bounds: task.AABB{Min: task.Vec3{X: -1, Y: -1, Z: -1}, Max: task.Vec3{X: 1, Y: 1, Z: 1}}},
		{ID: 2, Bounds: task.AABB{Min: task.Vec3{X: 5, Y: 5, Z: 5}, Max: task.Vec3{X: 6, Y: 6, Z: 6}}},
	}
	vis, err := c.CullVisible(0, objs)
	if err != nil {
		t.Fatalf("CullVisible returned error: %v", err)
	}
	if len(vis) != 2 {
		t.Fatalf("camera inside the occluder culled %d of 2 objects, want all visible", 2-len(vis))
	}
}

func TestTransparentGeometryNeverOccludes(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	if c.TransparentCanOcclude() {
		t.Fatalf("transparent geometry is treated as an occluder")
	}
}

func TestObjectBehindTransparentStaysVisible(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	occluder := task.Object{
		ID:          1,
		Occluder:    true,
		Transparent: true,
		Bounds:      task.AABB{Min: task.Vec3{X: 1, Y: 1, Z: 1}, Max: task.Vec3{X: 2, Y: 2, Z: 2}},
	}
	behind := task.Object{
		ID:     2,
		Bounds: task.AABB{Min: task.Vec3{X: 5, Y: 5, Z: 5}, Max: task.Vec3{X: 6, Y: 6, Z: 6}},
	}
	vis, err := c.CullVisible(0, []task.Object{occluder, behind})
	if err != nil {
		t.Fatalf("CullVisible returned error: %v", err)
	}
	if !hasID(vis, 2) {
		t.Fatalf("object behind a transparent occluder was culled, visible set = %v", vis)
	}
}

func TestSkinnedBoundsRefreshPicksUpNewPose(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	old := task.AABB{Min: task.Vec3{X: -1, Y: -1, Z: -1}, Max: task.Vec3{X: 1, Y: 1, Z: 1}}
	if err := c.AddObject(task.Object{ID: 7, Skinned: true, Bounds: old}); err != nil {
		t.Fatalf("AddObject returned error: %v", err)
	}
	updated := task.AABB{Min: task.Vec3{X: -2, Y: -2, Z: -2}, Max: task.Vec3{X: 2, Y: 2, Z: 2}}
	got, err := c.UpdateSkinnedBounds(7, updated)
	if err != nil {
		t.Fatalf("UpdateSkinnedBounds returned error: %v", err)
	}
	if got.Max.X != 2 || got.Min.X != -2 {
		t.Fatalf("skinned bounds stay %+v after the pose changed to %+v", got, updated)
	}
}

func TestDelayedQueryResultsMergedAcrossFrames(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	merged := c.MergeDelayed([]task.FrameResult{
		{Frame: 1, Visible: map[int]bool{1: true, 2: false}},
		{Frame: 2, Visible: map[int]bool{2: true, 3: false}},
	})
	if !merged[1] || !merged[2] {
		t.Fatalf("merged visibility %v lost results of a delayed frame", merged)
	}
	if merged[3] {
		t.Fatalf("merged visibility %v invented visibility for an always hidden object", merged)
	}
}

func TestPerCameraDepthTargetsAreDistinct(t *testing.T) {
	c := task.NewOcclusionCuller(2)
	d0, err := c.DepthTargetFor(0)
	if err != nil {
		t.Fatalf("DepthTargetFor(0) returned error: %v", err)
	}
	d1, err := c.DepthTargetFor(1)
	if err != nil {
		t.Fatalf("DepthTargetFor(1) returned error: %v", err)
	}
	if d0.Camera == d1.Camera {
		t.Fatalf("both cameras read depth from camera %d, the depth buffer is shared", d0.Camera)
	}
}

func TestOccludersAreNeverCulledByTheirOwnQuery(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	objs := []task.Object{
		{ID: 5, Occluder: true, Bounds: task.AABB{Min: task.Vec3{X: 0, Y: 0, Z: 0}, Max: task.Vec3{X: 1, Y: 1, Z: 1}}},
		{ID: 6, Bounds: task.AABB{Min: task.Vec3{X: 0, Y: 0, Z: 0}, Max: task.Vec3{X: 1, Y: 1, Z: 1}}},
	}
	vis, err := c.CullVisible(0, objs)
	if err != nil {
		t.Fatalf("CullVisible returned error: %v", err)
	}
	if !hasID(vis, 5) {
		t.Fatalf("occluder culled itself, visible set = %v", vis)
	}
}

func TestHiZRebuildPicksUpNewDepth(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	shallow := make([]float32, 16)
	deep := make([]float32, 16)
	for i := range deep {
		deep[i] = 0.9
	}
	first, err := c.BuildHiZ(shallow, 4, 4)
	if err != nil {
		t.Fatalf("BuildHiZ(shallow) returned error: %v", err)
	}
	_, hiFirst, err := first.ConservativeRange(0, 0, 0)
	if err != nil {
		t.Fatalf("ConservativeRange shallow: %v", err)
	}
	second, err := c.BuildHiZ(deep, 4, 4)
	if err != nil {
		t.Fatalf("BuildHiZ(deep) returned error: %v", err)
	}
	_, hiSecond, err := second.ConservativeRange(0, 0, 0)
	if err != nil {
		t.Fatalf("ConservativeRange deep: %v", err)
	}
	if hiSecond <= hiFirst {
		t.Fatalf("pyramid still holds the old depth after rebuild: [%v,%v] vs [%v,%v]", hiFirst, hiSecond, hiFirst, hiSecond)
	}
}

func TestReadbackMapsQueryResultsToObjectIDs(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	queries := []task.Query{
		{ObjectID: 10, Camera: 0},
		{ObjectID: 20, Camera: 0},
		{ObjectID: 30, Camera: 0},
	}
	got, err := c.ReadbackByObjectID(queries, []uint64{1, 0, 1})
	if err != nil {
		t.Fatalf("ReadbackByObjectID returned error: %v", err)
	}
	if !got[10] || got[20] || !got[30] {
		t.Fatalf("readback mapped results to the wrong IDs: %v", got)
	}
}

func TestQueryPoolExhaustionFallsBackToVisible(t *testing.T) {
	c := task.NewOcclusionCuller(1)
	capacity := c.QueryPoolCapacity()
	if capacity <= 0 {
		t.Fatalf("query pool capacity is %d, no queries can ever be issued", capacity)
	}
	if _, err := c.AcquireQueries(capacity + 1); err == nil {
		t.Fatalf("AcquireQueries accepted %d queries from a %d query pool", capacity+1, capacity)
	}
	vis, err := c.CullVisible(0, []task.Object{{ID: 1}, {ID: 2}, {ID: 3}})
	if err != nil {
		t.Fatalf("CullVisible returned error: %v", err)
	}
	if len(vis) != 3 {
		t.Fatalf("pool exhaustion culled objects instead of falling back to visible, got %v", vis)
	}
}

func TestShadowPassUsesMainCamera(t *testing.T) {
	c := task.NewOcclusionCuller(2)
	if got := c.ShadowCameraIndex(1); got != 1 {
		t.Fatalf("shadow pass uses camera %d while the main pass uses camera 1", got)
	}
}
