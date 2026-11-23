package task

import (
	"testing"

	task "gsb/batch45-game-b-08"
)

func idsOf(parts []task.Particle) []int {
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i] = p.ID
	}
	return out
}

func TestSortBackToFrontFarthestFirst(t *testing.T) {
	s := task.NewParticleSorter(64)
	if err := s.SetCamera(task.Vec3{}); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	parts := []task.Particle{
		{ID: 1, Pos: task.Vec3{Z: 1}, Alive: true},
		{ID: 2, Pos: task.Vec3{Z: 10}, Alive: true},
		{ID: 3, Pos: task.Vec3{Z: 5}, Alive: true},
	}
	got, err := s.SortBackToFront(parts)
	if err != nil {
		t.Fatalf("SortBackToFront returned error: %v", err)
	}
	ids := idsOf(got)
	if len(ids) != 3 || ids[0] != 2 || ids[1] != 3 || ids[2] != 1 {
		t.Fatalf("particles sorted by emission order instead of depth: %v", ids)
	}
}

func TestSortKeySubtractsBoundingRadius(t *testing.T) {
	s := task.NewParticleSorter(64)
	if err := s.SetCamera(task.Vec3{}); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	p := task.Particle{ID: 1, Pos: task.Vec3{Z: 10}, Radius: 2, Alive: true}
	if key := s.SortKey(p); key != 8 {
		t.Fatalf("SortKey = %v, want 8, the sort key ignores the particle bounding radius", key)
	}
}

func TestSortStableTiesKeepEmissionOrder(t *testing.T) {
	s := task.NewParticleSorter(64)
	if err := s.SetCamera(task.Vec3{}); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	parts := []task.Particle{
		{ID: 7, Pos: task.Vec3{Z: 5}, Alive: true},
		{ID: 3, Pos: task.Vec3{Y: 5}, Alive: true},
	}
	got, err := s.SortStableTies(parts)
	if err != nil {
		t.Fatalf("SortStableTies returned error: %v", err)
	}
	for i := range parts {
		if got[i].ID != parts[i].ID {
			t.Fatalf("equal depth particles reordered: got %v, want %v", idsOf(got), idsOf(parts))
		}
	}
}

func TestSortEmptyReturnsEmpty(t *testing.T) {
	s := task.NewParticleSorter(64)
	got, err := s.SortBackToFront(nil)
	if err != nil {
		t.Fatalf("SortBackToFront(empty) returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("SortBackToFront(empty) returned %d particles", len(got))
	}
}

func TestSetCameraBumpsGeneration(t *testing.T) {
	s := task.NewParticleSorter(64)
	first := s.CameraGeneration()
	if err := s.SetCamera(task.Vec3{X: 1}); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	second := s.CameraGeneration()
	if second == first {
		t.Fatalf("camera generation stayed %d after the camera moved", second)
	}
	if err := s.SetCamera(task.Vec3{X: 2}); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	if third := s.CameraGeneration(); third == second {
		t.Fatalf("camera generation stayed %d after the second move", third)
	}
}

func TestSortRejectsStaleCameraStamp(t *testing.T) {
	s := task.NewParticleSorter(64)
	if err := s.SetCamera(task.Vec3{Z: 1}); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	parts := []task.Particle{{ID: 1, Pos: task.Vec3{Z: 2}, Alive: true}}
	if _, err := s.SortWithCameraStamp(parts, s.CameraGeneration()); err != nil {
		t.Fatalf("SortWithCameraStamp rejected the current camera: %v", err)
	}
	stale := s.CameraGeneration()
	if err := s.SetCamera(task.Vec3{Z: 3}); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	if _, err := s.SortWithCameraStamp(parts, stale); err == nil {
		t.Fatalf("SortWithCameraStamp accepted a stamp of the previous camera")
	}
}

func TestBatchByBlendSplitsAlphaAndAdditive(t *testing.T) {
	s := task.NewParticleSorter(64)
	parts := []task.Particle{
		{ID: 1, Blend: task.BlendAdditive, Alive: true},
		{ID: 2, Blend: task.BlendAlpha, Alive: true},
		{ID: 3, Blend: task.BlendAdditive, Alive: true},
	}
	batches, err := s.BatchByBlend(parts)
	if err != nil {
		t.Fatalf("BatchByBlend returned error: %v", err)
	}
	if len(batches) != 2 {
		t.Fatalf("BatchByBlend produced %d batches, want 2 (alpha and additive)", len(batches))
	}
	if len(batches[0]) != 1 || len(batches[1]) != 2 {
		t.Fatalf("batch sizes = %d and %d, want alpha 1 and additive 2", len(batches[0]), len(batches[1]))
	}
}

func TestBatchByBlendPreservesOrder(t *testing.T) {
	s := task.NewParticleSorter(64)
	parts := []task.Particle{
		{ID: 5, Blend: task.BlendAlpha, Alive: true},
		{ID: 9, Blend: task.BlendAlpha, Alive: true},
		{ID: 6, Blend: task.BlendAdditive, Alive: true},
	}
	batches, err := s.BatchByBlend(parts)
	if err != nil {
		t.Fatalf("BatchByBlend returned error: %v", err)
	}
	if len(batches) != 2 {
		t.Fatalf("BatchByBlend produced %d batches, want 2", len(batches))
	}
	if batches[0][0].ID != 5 || batches[0][1].ID != 9 {
		t.Fatalf("alpha batch order = %v, want [5 9]", idsOf(batches[0]))
	}
}

func TestSoftDepthAveragesNeighbors(t *testing.T) {
	s := task.NewParticleSorter(64)
	depth := []float32{1, 2, 3, 4}
	got, err := s.SampleSoftDepth(depth, 2, 2, 0, 0)
	if err != nil {
		t.Fatalf("SampleSoftDepth returned error: %v", err)
	}
	if got == 1 {
		t.Fatalf("soft depth is the single centre tap %v, neighbours were not sampled", got)
	}
	if got < 1 || got > 4 {
		t.Fatalf("soft depth %v is outside the neighbourhood range [1,4]", got)
	}
}

func TestSoftDepthRejectsOutOfBounds(t *testing.T) {
	s := task.NewParticleSorter(64)
	depth := []float32{1, 2, 3, 4}
	if _, err := s.SampleSoftDepth(depth, 2, 2, 1, 1); err != nil {
		t.Fatalf("SampleSoftDepth rejected an in range texel: %v", err)
	}
	if _, err := s.SampleSoftDepth(depth, 2, 2, 5, 5); err == nil {
		t.Fatalf("SampleSoftDepth accepted a texel outside the depth buffer")
	}
}

func TestAcquireSlotReturnsUniqueIDs(t *testing.T) {
	s := task.NewParticleSorter(4)
	seen := map[int]bool{}
	for i := 0; i < 3; i++ {
		id, err := s.AcquireSlot()
		if err != nil {
			t.Fatalf("AcquireSlot %d returned error: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("AcquireSlot handed out slot %d twice", id)
		}
		seen[id] = true
	}
}

func TestReleaseSlotRecycles(t *testing.T) {
	s := task.NewParticleSorter(4)
	id, err := s.AcquireSlot()
	if err != nil {
		t.Fatalf("AcquireSlot returned error: %v", err)
	}
	if err := s.ReleaseSlot(id); err != nil {
		t.Fatalf("ReleaseSlot returned error: %v", err)
	}
	again, err := s.AcquireSlot()
	if err != nil {
		t.Fatalf("AcquireSlot after release returned error: %v", err)
	}
	if again != id {
		t.Fatalf("released slot %d was not recycled, next acquire returned %d", id, again)
	}
}

func TestBeginFrameResetsFrameBuffer(t *testing.T) {
	s := task.NewParticleSorter(8)
	if err := s.EmitBatch([]task.Particle{{ID: 1, Alive: true}, {ID: 2, Alive: true}}); err != nil {
		t.Fatalf("EmitBatch returned error: %v", err)
	}
	if err := s.BeginFrame(); err != nil {
		t.Fatalf("BeginFrame returned error: %v", err)
	}
	if got := s.ActiveSlots(); got != 0 {
		t.Fatalf("ActiveSlots = %d after BeginFrame, stale particles of the previous frame survived", got)
	}
	if err := s.EmitBatch([]task.Particle{{ID: 3, Alive: true}}); err != nil {
		t.Fatalf("EmitBatch after BeginFrame returned error: %v", err)
	}
	if got := s.ActiveSlots(); got != 1 {
		t.Fatalf("ActiveSlots = %d after emitting one particle, want 1", got)
	}
}

func TestOverflowNeverOverwritesActiveParticles(t *testing.T) {
	s := task.NewParticleSorter(2)
	if s.Capacity() != 2 {
		t.Fatalf("Capacity() = %d, want 2", s.Capacity())
	}
	if s.OverflowOverwritesActive() {
		t.Fatalf("buffer overflow is documented to recycle live particles")
	}
	if err := s.EmitBatch([]task.Particle{{ID: 1, Alive: true}, {ID: 2, Alive: true}}); err != nil {
		t.Fatalf("EmitBatch within capacity returned error: %v", err)
	}
	if err := s.EmitBatch([]task.Particle{{ID: 3, Alive: true}}); err == nil {
		t.Fatalf("EmitBatch accepted a particle while the buffer is full")
	}
	if got := s.ActiveSlots(); got != 2 {
		t.Fatalf("ActiveSlots = %d after overflow, active particles were overwritten", got)
	}
}

func TestAdditiveBlendsAfterAlpha(t *testing.T) {
	s := task.NewParticleSorter(8)
	alpha := s.BlendOrderIndex(task.BlendAlpha)
	additive := s.BlendOrderIndex(task.BlendAdditive)
	if alpha < 0 || additive < 0 {
		t.Fatalf("blend order ranks are not set: alpha=%d additive=%d", alpha, additive)
	}
	if additive <= alpha {
		t.Fatalf("additive rank %d is not after alpha rank %d", additive, alpha)
	}
}
