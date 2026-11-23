package task

import (
	"testing"

	task "gsb/batch45-game-b-07"
)

func approx(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestGroupByAdjacencyKeepsNeighborsTogether(t *testing.T) {
	m := task.NewMeshletLod(16)
	clusters := []task.Cluster{
		{ID: 1, Bounds: task.Sphere{Center: task.Vec3{X: 0}}},
		{ID: 2, Bounds: task.Sphere{Center: task.Vec3{X: 1}}},
		{ID: 3, Bounds: task.Sphere{Center: task.Vec3{X: 100}}},
	}
	groups, err := m.GroupByAdjacency(clusters, 2.0)
	if err != nil {
		t.Fatalf("GroupByAdjacency returned error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (two neighbours plus one distant cluster)", len(groups))
	}
	neighbours := 0
	for _, ids := range groups {
		if len(ids) == 2 {
			neighbours++
		}
	}
	if neighbours != 1 {
		t.Fatalf("adjacent clusters were not grouped together: %v", groups)
	}
}

func TestGroupByAdjacencyRejectsEmptyInput(t *testing.T) {
	m := task.NewMeshletLod(16)
	if _, err := m.GroupByAdjacency([]task.Cluster{{ID: 1}}, 1.0); err != nil {
		t.Fatalf("GroupByAdjacency rejected a single cluster: %v", err)
	}
	if _, err := m.GroupByAdjacency(nil, 1.0); err == nil {
		t.Fatalf("GroupByAdjacency accepted an empty cluster list")
	}
}

func TestBoundSphereCoversAllVertices(t *testing.T) {
	m := task.NewMeshletLod(16)
	verts := []task.Vec3{
		{X: -1, Y: -1, Z: -1},
		{X: 1, Y: -1, Z: -1},
		{X: -1, Y: 1, Z: -1},
		{X: -1, Y: -1, Z: 1},
		{X: 1, Y: 1, Z: 1},
	}
	s := m.BoundSphere(verts)
	if s.Radius < 1.7 {
		t.Fatalf("bound sphere radius %v does not cover the unit cube corners", s.Radius)
	}
	c := task.Cluster{ID: 1, Bounds: s, Vertices: verts}
	if !m.BoundsCoverVertices(c) {
		t.Fatalf("BoundsCoverVertices rejected the sphere built from the cluster vertices")
	}
}

func TestBoundSphereCenterIsVertexCentroid(t *testing.T) {
	m := task.NewMeshletLod(16)
	verts := []task.Vec3{
		{X: -2, Y: 0, Z: 0},
		{X: 2, Y: 0, Z: 0},
		{X: 0, Y: -2, Z: 0},
		{X: 0, Y: 2, Z: 0},
	}
	s := m.BoundSphere(verts)
	if !approx(s.Center.X, 0, 1e-9) || !approx(s.Center.Y, 0, 1e-9) {
		t.Fatalf("bound sphere centre %+v is not the centroid of a symmetric vertex set", s.Center)
	}
	if !approx(s.Radius, 2, 1e-9) {
		t.Fatalf("bound sphere radius %v, want 2", s.Radius)
	}
}

func TestSimplifyKeepsMaterialBoundary(t *testing.T) {
	m := task.NewMeshletLod(16)
	orig := task.Cluster{ID: 1, Material: 5, Vertices: []task.Vec3{{X: 0}, {X: 1}}}
	same := task.Cluster{ID: 1, Material: 5, Vertices: []task.Vec3{{X: 0}, {X: 1}}}
	if !m.SimplifyPreservesMaterialBoundary(orig, same) {
		t.Fatalf("simplification across the same material was rejected")
	}
	crossed := task.Cluster{ID: 1, Material: 6, Vertices: []task.Vec3{{X: 0}, {X: 1}}}
	if m.SimplifyPreservesMaterialBoundary(orig, crossed) {
		t.Fatalf("simplification that crosses a material boundary was accepted")
	}
}

func TestRebuildIndexBufferRespectsLOD(t *testing.T) {
	m := task.NewMeshletLod(16)
	c := task.Cluster{ID: 1, Indices: []uint32{0, 1, 2, 3, 0, 2, 1, 3}}
	lod0, err := m.RebuildIndexBuffer(c, 0)
	if err != nil {
		t.Fatalf("RebuildIndexBuffer(LOD 0) returned error: %v", err)
	}
	lod1, err := m.RebuildIndexBuffer(c, 1)
	if err != nil {
		t.Fatalf("RebuildIndexBuffer(LOD 1) returned error: %v", err)
	}
	if len(lod0) == 0 || len(lod1) == 0 {
		t.Fatalf("index buffer is empty at LOD 0 (%d) or LOD 1 (%d)", len(lod0), len(lod1))
	}
	if len(lod1) >= len(lod0) {
		t.Fatalf("LOD 1 index buffer keeps %d of %d indices, no reduction happened", len(lod1), len(lod0))
	}
	for _, idx := range lod1 {
		if int(idx) >= len(c.Vertices) && len(c.Vertices) > 0 {
			t.Fatalf("LOD 1 index %d points outside the cluster vertex buffer", idx)
		}
	}
}

func TestRebuildIndexBufferRejectsUnknownLOD(t *testing.T) {
	m := task.NewMeshletLod(16)
	c := task.Cluster{ID: 1, Indices: []uint32{0, 1, 2}}
	if _, err := m.RebuildIndexBuffer(c, 0); err != nil {
		t.Fatalf("RebuildIndexBuffer(LOD 0) returned error: %v", err)
	}
	if _, err := m.RebuildIndexBuffer(c, 9); err == nil {
		t.Fatalf("RebuildIndexBuffer accepted LOD 9 that does not exist")
	}
}

func TestScreenPixelSizeShrinksWithDistance(t *testing.T) {
	m := task.NewMeshletLod(16)
	c := task.Cluster{ID: 1, Bounds: task.Sphere{Center: task.Vec3{X: 0}, Radius: 1}}
	near := m.ScreenPixelSize(c, task.Vec3{X: 5}, 60, 1080)
	far := m.ScreenPixelSize(c, task.Vec3{X: 50}, 60, 1080)
	if near <= 0 || far <= 0 {
		t.Fatalf("screen size is not positive: near=%v far=%v", near, far)
	}
	if far >= near {
		t.Fatalf("distant cluster reports %v px, near cluster %v px, size does not shrink", far, near)
	}
}

func TestSelectLODUsesScreenPixels(t *testing.T) {
	m := task.NewMeshletLod(16)
	c := task.Cluster{ID: 1}
	thresholds := []float64{200, 80, 20}
	cases := []struct {
		pixels float64
		want   int
	}{
		{400, 0},
		{100, 1},
		{30, 2},
		{5, 3},
	}
	for _, tc := range cases {
		if got := m.SelectLOD(c, tc.pixels, thresholds); got != tc.want {
			t.Fatalf("SelectLOD(%v px) = %d, want %d", tc.pixels, got, tc.want)
		}
	}
}

func TestGroupBufferEpochChangesOnSync(t *testing.T) {
	m := task.NewMeshletLod(16)
	before, err := m.GroupBufferEpoch(0)
	if err != nil {
		t.Fatalf("GroupBufferEpoch returned error: %v", err)
	}
	if err := m.SyncGroupBuffer(0); err != nil {
		t.Fatalf("SyncGroupBuffer returned error: %v", err)
	}
	after, err := m.GroupBufferEpoch(0)
	if err != nil {
		t.Fatalf("GroupBufferEpoch after sync returned error: %v", err)
	}
	if after <= before {
		t.Fatalf("shared vertex buffer epoch stayed %d after a sync from %d", after, before)
	}
}

func TestTransitionAlphaIsMonotonic(t *testing.T) {
	m := task.NewMeshletLod(16)
	if a := m.TransitionAlpha(0, 1, 0); a != 0 {
		t.Fatalf("transition alpha at progress 0 is %v, want 0", a)
	}
	mid := m.TransitionAlpha(0, 1, 0.5)
	if !approx(mid, 0.5, 1e-9) {
		t.Fatalf("transition alpha at progress 0.5 is %v, want 0.5", mid)
	}
	if a := m.TransitionAlpha(0, 1, 1); a != 1 {
		t.Fatalf("transition alpha at progress 1 is %v, want 1", a)
	}
}

func TestShadowPassReusesMainLOD(t *testing.T) {
	m := task.NewMeshletLod(16)
	for _, lod := range []int{0, 1, 2, 3} {
		if got := m.ShadowLOD(lod); got != lod {
			t.Fatalf("ShadowLOD(%d) = %d, shadow and main pass disagree", lod, got)
		}
	}
}

func TestClusterMustBeResidentBeforeRender(t *testing.T) {
	m := task.NewMeshletLod(16)
	if m.IsResident(3) {
		t.Fatalf("cluster 3 is resident before it was streamed")
	}
	if err := m.StreamCluster(3); err != nil {
		t.Fatalf("StreamCluster returned error: %v", err)
	}
	if !m.IsResident(3) {
		t.Fatalf("cluster 3 is still not resident after streaming, render would use garbage")
	}
}

func TestOverflowReportsDroppedClusters(t *testing.T) {
	m := task.NewMeshletLod(2)
	if got := m.MaxClusters(); got != 2 {
		t.Fatalf("MaxClusters() = %d, want 2", got)
	}
	for i := 1; i <= 3; i++ {
		if err := m.AddCluster(task.Cluster{ID: i}); err != nil {
			t.Fatalf("AddCluster(%d) returned error: %v", i, err)
		}
	}
	if got := m.OverflowCount(); got != 1 {
		t.Fatalf("OverflowCount() = %d, want 1 silently dropped cluster to be reported", got)
	}
}

func TestDeferredFreeKeepsBufferUntilFlush(t *testing.T) {
	m := task.NewMeshletLod(16)
	if err := m.DeferFree(4); err != nil {
		t.Fatalf("DeferFree returned error: %v", err)
	}
	if got := m.PendingFrees(); got != 1 {
		t.Fatalf("PendingFrees() = %d right after DeferFree, want 1", got)
	}
	if err := m.FlushFrees(); err != nil {
		t.Fatalf("FlushFrees returned error: %v", err)
	}
	if got := m.PendingFrees(); got != 0 {
		t.Fatalf("PendingFrees() = %d after flush, want 0", got)
	}
}
