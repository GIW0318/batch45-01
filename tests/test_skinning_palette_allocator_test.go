package task

import (
	"testing"

	task "gsb/batch45-game-b-15"
)

func identity() task.Mat4 {
	return task.Mat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

func sameMatrix(a, b task.Mat4) bool {
	for i := 0; i < 16; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTextureWidthMatchesRequest(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	if got := p.TextureWidth(); got != 1024 {
		t.Fatalf("TextureWidth() = %d, want 1024", got)
	}
}

func TestAllocateIsolatesCharacters(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	first, err := p.Allocate(1, 8)
	if err != nil {
		t.Fatalf("Allocate(1) returned error: %v", err)
	}
	second, err := p.Allocate(2, 8)
	if err != nil {
		t.Fatalf("Allocate(2) returned error: %v", err)
	}
	if first == second {
		t.Fatalf("both characters were allocated base slot %d, the slot ranges overlap", first)
	}
	offset, err := p.SlotOffset(2)
	if err != nil {
		t.Fatalf("SlotOffset(2) returned error: %v", err)
	}
	if offset != second {
		t.Fatalf("SlotOffset(2) = %d, Allocate(2) returned %d", offset, second)
	}
}

func TestAllocateRejectsBoneOverflow(t *testing.T) {
	p := task.NewSkinPalette(16, 64)
	if _, err := p.Allocate(1, 8); err != nil {
		t.Fatalf("Allocate rejected a valid 8 bone character: %v", err)
	}
	if _, err := p.Allocate(2, 17); err == nil {
		t.Fatalf("Allocate accepted 17 bones in a 16 wide palette, the slot index wraps")
	}
}

func TestWriteMatricesStoresPerCharacter(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	if _, err := p.Allocate(1, 2); err != nil {
		t.Fatalf("Allocate(1) returned error: %v", err)
	}
	if _, err := p.Allocate(2, 2); err != nil {
		t.Fatalf("Allocate(2) returned error: %v", err)
	}
	a := []task.Mat4{identity(), identity()}
	a[0][12] = 1
	b := []task.Mat4{identity(), identity()}
	b[0][12] = 2
	if err := p.WriteMatrices(1, a); err != nil {
		t.Fatalf("WriteMatrices(1) returned error: %v", err)
	}
	if err := p.WriteMatrices(2, b); err != nil {
		t.Fatalf("WriteMatrices(2) returned error: %v", err)
	}
	gotA, err := p.ReadBack(1, 2)
	if err != nil {
		t.Fatalf("ReadBack(1) returned error: %v", err)
	}
	gotB, err := p.ReadBack(2, 2)
	if err != nil {
		t.Fatalf("ReadBack(2) returned error: %v", err)
	}
	if gotA[0][12] != 1 || gotB[0][12] != 2 {
		t.Fatalf("palette of character 1 holds %v and character 2 holds %v, the characters overwrite each other", gotA[0][12], gotB[0][12])
	}
}

func TestReadBackReturnsWrittenMatrices(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	if _, err := p.Allocate(7, 1); err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	want := identity()
	want[0] = 3.5
	if err := p.WriteMatrices(7, []task.Mat4{want}); err != nil {
		t.Fatalf("WriteMatrices returned error: %v", err)
	}
	got, err := p.ReadBack(7, 1)
	if err != nil {
		t.Fatalf("ReadBack returned error: %v", err)
	}
	if len(got) != 1 || !sameMatrix(got[0], want) {
		t.Fatalf("read back %v, want the matrix that was written (%v)", got, want)
	}
}

func TestPaletteMatricesAreTransposed(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	m := identity()
	m[1] = 2
	out := p.Transpose([]task.Mat4{m})
	if len(out) != 1 {
		t.Fatalf("Transpose returned %d matrices for 1 input", len(out))
	}
	if out[0][4] != 2 {
		t.Fatalf("transposed element 4 is %v, want 2 from element 1, the palette is uploaded untransposed", out[0][4])
	}
	if !sameMatrix(p.Transpose([]task.Mat4{identity()})[0], identity()) {
		t.Fatalf("transposing the identity matrix changed it")
	}
}

func TestBarrierBumpsPaletteGeneration(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	before := p.PaletteGeneration()
	if err := p.Barrier(); err != nil {
		t.Fatalf("Barrier returned error: %v", err)
	}
	if after := p.PaletteGeneration(); after == before {
		t.Fatalf("palette generation stayed %d after a barrier, the sampling stage reads stale bones", after)
	}
}

func TestRemapBoneIndexAfterLODSwitch(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	if _, err := p.Allocate(1, 8); err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	idx, err := p.RemapBoneIndex(1, 3, 0)
	if err != nil {
		t.Fatalf("RemapBoneIndex returned error: %v", err)
	}
	if idx < 0 || idx >= 8 {
		t.Fatalf("remapped bone index %d is outside the 8 slot range", idx)
	}
	again, err := p.RemapBoneIndex(1, 3, 0)
	if err != nil {
		t.Fatalf("RemapBoneIndex returned error: %v", err)
	}
	if again != idx {
		t.Fatalf("remapping bone 3 twice gave %d then %d", idx, again)
	}
	if _, err := p.RemapBoneIndex(1, 99, 0); err == nil {
		t.Fatalf("RemapBoneIndex accepted bone index 99 of an 8 bone skeleton")
	}
}

func TestValidateWeightsRequiresNormalized(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	if err := p.ValidateWeights([]float64{0.5, 0.5}); err != nil {
		t.Fatalf("ValidateWeights rejected normalized weights: %v", err)
	}
	if err := p.ValidateWeights([]float64{0.5, 0.4}); err == nil {
		t.Fatalf("ValidateWeights accepted weights summing to 0.9")
	}
}

func TestReleaseRecyclesSlots(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	base, err := p.Allocate(1, 4)
	if err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	if err := p.Release(1); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	again, err := p.Allocate(2, 4)
	if err != nil {
		t.Fatalf("Allocate after release returned error: %v", err)
	}
	if again != base {
		t.Fatalf("released slots starting at %d were not recycled, next character starts at %d", base, again)
	}
}

func TestActiveCharactersTracksLifecycle(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	if got := p.ActiveCharacters(); got != 0 {
		t.Fatalf("ActiveCharacters() = %d on a fresh palette, want 0", got)
	}
	if _, err := p.Allocate(1, 4); err != nil {
		t.Fatalf("Allocate(1) returned error: %v", err)
	}
	if _, err := p.Allocate(2, 4); err != nil {
		t.Fatalf("Allocate(2) returned error: %v", err)
	}
	if got := p.ActiveCharacters(); got != 2 {
		t.Fatalf("ActiveCharacters() = %d after two allocations, want 2", got)
	}
	if err := p.Release(1); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	if got := p.ActiveCharacters(); got != 1 {
		t.Fatalf("ActiveCharacters() = %d after one release, want 1", got)
	}
}

func TestSplitRootMotionSeparatesTranslation(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	root := identity()
	root[13] = 5
	bones := []task.Mat4{identity()}
	bones[0][13] = 5
	outRoot, outBones, err := p.SplitRootMotion(root, bones)
	if err != nil {
		t.Fatalf("SplitRootMotion returned error: %v", err)
	}
	if outRoot[13] != 5 {
		t.Fatalf("root motion translation %v was lost", outRoot[13])
	}
	if outBones[0][13] != 0 {
		t.Fatalf("bone still carries the root translation %v, motion is applied twice", outBones[0][13])
	}
}

func TestRecomputeUVMatchesWidth(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	u, err := p.RecomputeUV(1024)
	if err != nil {
		t.Fatalf("RecomputeUV returned error: %v", err)
	}
	want := 1.0 / 1024.0
	if u != want {
		t.Fatalf("RecomputeUV(1024) = %v, want %v", u, want)
	}
	if _, err := p.RecomputeUV(0); err == nil {
		t.Fatalf("RecomputeUV accepted a zero width palette")
	}
}

func TestCPUGPUConsistencyAfterBarrier(t *testing.T) {
	p := task.NewSkinPalette(1024, 256)
	if _, err := p.Allocate(1, 2); err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	if err := p.WriteMatrices(1, []task.Mat4{identity(), identity()}); err != nil {
		t.Fatalf("WriteMatrices returned error: %v", err)
	}
	if err := p.Barrier(); err != nil {
		t.Fatalf("Barrier returned error: %v", err)
	}
	if !p.CPUGPUConsistent(1) {
		t.Fatalf("CPU and GPU skinning disagree after a synchronized write")
	}
}

func TestWrapSafelyRejectsOutOfRangeSlot(t *testing.T) {
	p := task.NewSkinPalette(16, 64)
	if got, err := p.WrapSafely(0); err != nil || got != 0 {
		t.Fatalf("WrapSafely(0) = (%d, %v), want (0, nil)", got, err)
	}
	if _, err := p.WrapSafely(21); err == nil {
		t.Fatalf("WrapSafely accepted slot 21 of a 16 wide palette, the index wraps around")
	}
}
