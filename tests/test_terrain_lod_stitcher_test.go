package task

import (
	"errors"
	"math"
	"testing"

	impl "gsb/batch45-game-b-05"
)

const chunkSize = 16

func coord(x, y int) impl.ChunkCoord { return impl.ChunkCoord{X: x, Y: y} }

func newTerrain(t *testing.T) *impl.TerrainLod {
	t.Helper()
	tl, err := impl.NewTerrainLod(impl.Config{
		ChunkSize:   chunkSize,
		MaxLOD:      4,
		SkirtHeight: 1,
		Hysteresis:  0.1,
		HeightScale: 8,
	})
	if err != nil {
		t.Fatalf("NewTerrainLod error: %v", err)
	}
	return tl
}

func mustLoad(t *testing.T, tl *impl.TerrainLod, c impl.ChunkCoord) {
	t.Helper()
	if err := tl.LoadChunk(c); err != nil {
		t.Fatalf("LoadChunk(%+v) error: %v", c, err)
	}
}

func TestNeighborLODsAligned(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	mustLoad(t, tl, coord(1, 0))
	if err := tl.ForceLOD(coord(0, 0), 1); err != nil {
		t.Fatalf("ForceLOD(0,0) error: %v", err)
	}
	if err := tl.ForceLOD(coord(1, 0), 3); err != nil {
		t.Fatalf("ForceLOD(1,0) error: %v", err)
	}
	if err := tl.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	a, err := tl.StitchedLOD(coord(0, 0))
	if err != nil {
		t.Fatalf("StitchedLOD(0,0) error: %v", err)
	}
	b, err := tl.StitchedLOD(coord(1, 0))
	if err != nil {
		t.Fatalf("StitchedLOD(1,0) error: %v", err)
	}
	if a != b {
		t.Fatalf("neighbour lod mismatch: %d vs %d (seam will crack)", a, b)
	}
	if a != 3 {
		t.Fatalf("stitched lod = %d, want the coarser neighbour 3", a)
	}
}

func TestLODHysteresisPreventsFlapping(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	if err := tl.SetCamera(impl.Vec3{X: 100}); err != nil {
		t.Fatalf("SetCamera error: %v", err)
	}
	if err := tl.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	base, err := tl.LODOf(coord(0, 0))
	if err != nil {
		t.Fatalf("LODOf error: %v", err)
	}
	if base == 0 {
		t.Fatalf("camera far away but lod = 0 (no screen error based selection)")
	}
	if err := tl.SetCamera(impl.Vec3{X: 101}); err != nil {
		t.Fatalf("SetCamera jitter error: %v", err)
	}
	if err := tl.Update(); err != nil {
		t.Fatalf("Update jitter error: %v", err)
	}
	jittered, err := tl.LODOf(coord(0, 0))
	if err != nil {
		t.Fatalf("LODOf jitter error: %v", err)
	}
	if jittered != base {
		t.Fatalf("lod flapped from %d to %d for a 1 unit camera jitter", base, jittered)
	}
}

func TestSkirtHeightGrowsWithLOD(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	mustLoad(t, tl, coord(1, 0))
	if err := tl.ForceLOD(coord(0, 0), 0); err != nil {
		t.Fatalf("ForceLOD(0,0) error: %v", err)
	}
	if err := tl.ForceLOD(coord(1, 0), 3); err != nil {
		t.Fatalf("ForceLOD(1,0) error: %v", err)
	}
	fine, err := tl.SkirtHeight(coord(0, 0))
	if err != nil {
		t.Fatalf("SkirtHeight(fine) error: %v", err)
	}
	coarse, err := tl.SkirtHeight(coord(1, 0))
	if err != nil {
		t.Fatalf("SkirtHeight(coarse) error: %v", err)
	}
	if fine <= 0 || coarse <= 0 {
		t.Fatalf("skirt heights = %v / %v, want positive", fine, coarse)
	}
	if coarse < 2*fine {
		t.Fatalf("coarse skirt height = %v, want at least twice the fine skirt %v", coarse, fine)
	}
}

func TestHeightClampedAtBoundary(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	edge := tl.HeightAt(0, 0)
	far := tl.HeightAt(-1e6, -1e6)
	if math.IsNaN(edge) || math.IsNaN(far) {
		t.Fatalf("height sampling produced NaN")
	}
	if edge != far {
		t.Fatalf("out of range sample %v differs from clamped edge %v", far, edge)
	}
}

func TestLODChosenByScreenError(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	mustLoad(t, tl, coord(5, 5))
	if err := tl.SetCamera(impl.Vec3{X: 8, Z: 8}); err != nil {
		t.Fatalf("SetCamera error: %v", err)
	}
	if err := tl.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	nearErr, err := tl.ScreenError(coord(0, 0))
	if err != nil {
		t.Fatalf("ScreenError(near) error: %v", err)
	}
	farErr, err := tl.ScreenError(coord(5, 5))
	if err != nil {
		t.Fatalf("ScreenError(far) error: %v", err)
	}
	if nearErr <= farErr {
		t.Fatalf("near screen error %v not larger than far screen error %v", nearErr, farErr)
	}
	nearLOD, err := tl.LODOf(coord(0, 0))
	if err != nil {
		t.Fatalf("LODOf(near) error: %v", err)
	}
	farLOD, err := tl.LODOf(coord(5, 5))
	if err != nil {
		t.Fatalf("LODOf(far) error: %v", err)
	}
	if nearLOD > farLOD {
		t.Fatalf("near chunk lod %d is coarser than far chunk lod %d", nearLOD, farLOD)
	}
}

func TestNormalIsUnitLength(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	n, err := tl.NormalAt(coord(0, 0), 4, 4)
	if err != nil {
		t.Fatalf("NormalAt error: %v", err)
	}
	length := math.Sqrt(n.X*n.X + n.Y*n.Y + n.Z*n.Z)
	if math.Abs(length-1) > 1e-6 {
		t.Fatalf("normal %+v has length %v, want unit length", n, length)
	}
}

func TestIndexOffsetPerChunk(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	mustLoad(t, tl, coord(1, 0))
	off0, err := tl.IndexOffset(coord(0, 0))
	if err != nil {
		t.Fatalf("IndexOffset(0,0) error: %v", err)
	}
	off1, err := tl.IndexOffset(coord(1, 0))
	if err != nil {
		t.Fatalf("IndexOffset(1,0) error: %v", err)
	}
	if off0 != 0 {
		t.Fatalf("first chunk index offset = %d, want 0", off0)
	}
	verts, err := tl.Vertices(coord(0, 0))
	if err != nil {
		t.Fatalf("Vertices(0,0) error: %v", err)
	}
	if off1 != verts {
		t.Fatalf("second chunk index offset = %d, want %d (offset by the first chunk)", off1, verts)
	}
}

func TestPerChunkVertexBuffers(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	mustLoad(t, tl, coord(3, 3))
	for _, c := range []impl.ChunkCoord{coord(0, 0), coord(3, 3)} {
		got, err := tl.GPUBuffers(c)
		if err != nil {
			t.Fatalf("GPUBuffers(%+v) error: %v", c, err)
		}
		if got != 1 {
			t.Fatalf("chunk %+v owns %d buffers, want its own single buffer", c, got)
		}
	}
}

func TestShadowLODMatchesMainLOD(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	mustLoad(t, tl, coord(2, 0))
	if err := tl.ForceLOD(coord(0, 0), 1); err != nil {
		t.Fatalf("ForceLOD(0,0) error: %v", err)
	}
	if err := tl.ForceLOD(coord(2, 0), 4); err != nil {
		t.Fatalf("ForceLOD(2,0) error: %v", err)
	}
	if err := tl.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	for _, c := range []impl.ChunkCoord{coord(0, 0), coord(2, 0)} {
		main, err := tl.LODOf(c)
		if err != nil {
			t.Fatalf("LODOf(%+v) error: %v", c, err)
		}
		shadow, err := tl.ShadowLOD(c)
		if err != nil {
			t.Fatalf("ShadowLOD(%+v) error: %v", c, err)
		}
		if shadow != main {
			t.Fatalf("chunk %+v shadow lod %d != main lod %d", c, shadow, main)
		}
	}
}

func TestUVContinuousAcrossChunks(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	mustLoad(t, tl, coord(1, 0))
	uLeft, vLeft, err := tl.UVAt(coord(0, 0), chunkSize, 0)
	if err != nil {
		t.Fatalf("UVAt(left) error: %v", err)
	}
	uRight, vRight, err := tl.UVAt(coord(1, 0), 0, 0)
	if err != nil {
		t.Fatalf("UVAt(right) error: %v", err)
	}
	if math.Abs(uLeft-uRight) > 1e-9 || math.Abs(vLeft-vRight) > 1e-9 {
		t.Fatalf("uv discontinuity at chunk border: (%v,%v) vs (%v,%v)", uLeft, vLeft, uRight, vRight)
	}
}

func TestUnloadReleasesGPUBuffers(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	if err := tl.UnloadChunk(coord(0, 0)); err != nil {
		t.Fatalf("UnloadChunk error: %v", err)
	}
	if tl.Loaded(coord(0, 0)) {
		t.Fatalf("chunk still loaded after unload")
	}
	if _, err := tl.GPUBuffers(coord(0, 0)); !errors.Is(err, impl.ErrChunkNotLoaded) {
		t.Fatalf("GPUBuffers after unload error = %v, want ErrChunkNotLoaded", err)
	}
}

func TestRapidCameraMovementCoalesced(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	for i := 0; i < 3; i++ {
		if err := tl.SetCamera(impl.Vec3{X: float64(i * 10)}); err != nil {
			t.Fatalf("SetCamera %d error: %v", i, err)
		}
	}
	if got := tl.PendingUpdates(); got != 1 {
		t.Fatalf("pending lod updates = %d, want 1 coalesced update", got)
	}
}

func TestUnloadUnknownErrors(t *testing.T) {
	tl := newTerrain(t)
	if err := tl.UnloadChunk(coord(9, 9)); !errors.Is(err, impl.ErrChunkNotLoaded) {
		t.Fatalf("unload unknown chunk error = %v, want ErrChunkNotLoaded", err)
	}
}

func TestLODUpdatesAfterCameraMove(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	if err := tl.SetCamera(impl.Vec3{X: 8, Z: 8}); err != nil {
		t.Fatalf("SetCamera near error: %v", err)
	}
	if err := tl.Update(); err != nil {
		t.Fatalf("Update near error: %v", err)
	}
	near, err := tl.LODOf(coord(0, 0))
	if err != nil {
		t.Fatalf("LODOf near error: %v", err)
	}
	if near != 0 {
		t.Fatalf("chunk under the camera lod = %d, want 0", near)
	}
	if err := tl.SetCamera(impl.Vec3{X: 2000, Z: 2000}); err != nil {
		t.Fatalf("SetCamera far error: %v", err)
	}
	if err := tl.Update(); err != nil {
		t.Fatalf("Update far error: %v", err)
	}
	far, err := tl.LODOf(coord(0, 0))
	if err != nil {
		t.Fatalf("LODOf far error: %v", err)
	}
	if far <= near {
		t.Fatalf("lod after moving the camera away = %d, want a coarser level than %d", far, near)
	}
}

func TestVerticesCountDecreasesWithLOD(t *testing.T) {
	tl := newTerrain(t)
	mustLoad(t, tl, coord(0, 0))
	if err := tl.ForceLOD(coord(0, 0), 0); err != nil {
		t.Fatalf("ForceLOD(0) error: %v", err)
	}
	fine, err := tl.Vertices(coord(0, 0))
	if err != nil {
		t.Fatalf("Vertices(lod0) error: %v", err)
	}
	if fine <= 0 {
		t.Fatalf("lod 0 vertex count = %d, want positive", fine)
	}
	if err := tl.ForceLOD(coord(0, 0), 3); err != nil {
		t.Fatalf("ForceLOD(3) error: %v", err)
	}
	coarse, err := tl.Vertices(coord(0, 0))
	if err != nil {
		t.Fatalf("Vertices(lod3) error: %v", err)
	}
	if coarse >= fine {
		t.Fatalf("lod 3 vertex count = %d, want fewer than lod 0 count %d", coarse, fine)
	}
}
