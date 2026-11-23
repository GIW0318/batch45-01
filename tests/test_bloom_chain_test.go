package task

import (
	"testing"

	task "gsb/batch45-game-b-09"
)

func closeEnough(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestBrightPassKeepsHDRHighlights(t *testing.T) {
	b := task.NewBloomChain(5)
	hdr := []task.RGB{{R: 8, G: 4, B: 2}, {R: 0.2, G: 0.3, B: 0.4}}
	out, err := b.BrightPass(hdr, 1.0)
	if err != nil {
		t.Fatalf("BrightPass returned error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("BrightPass returned %d samples, want 2", len(out))
	}
	if out[0].R != 8 {
		t.Fatalf("HDR highlight 8.0 became %v, the SDR threshold clipped it", out[0].R)
	}
	if out[1].R != 0 || out[1].G != 0 || out[1].B != 0 {
		t.Fatalf("sample below threshold survived as %+v", out[1])
	}
}

func TestBrightPassRejectsNonPositiveThreshold(t *testing.T) {
	b := task.NewBloomChain(5)
	hdr := []task.RGB{{R: 4}}
	if _, err := b.BrightPass(hdr, 1.0); err != nil {
		t.Fatalf("BrightPass rejected threshold 1.0: %v", err)
	}
	if _, err := b.BrightPass(hdr, 0); err == nil {
		t.Fatalf("BrightPass accepted threshold 0, every texel would bloom")
	}
}

func TestKarisAverageWeightsCentreTap(t *testing.T) {
	b := task.NewBloomChain(5)
	got, err := b.KarisAverage([]task.RGB{{}, {R: 4}, {}, {}})
	if err != nil {
		t.Fatalf("KarisAverage returned error: %v", err)
	}
	if !closeEnough(got.R, 2, 1e-9) {
		t.Fatalf("KarisAverage = %v, want 2 for a centre tap of 4 and three black taps", got.R)
	}
}

func TestDownsampleHalvesResolution(t *testing.T) {
	b := task.NewBloomChain(3)
	src := make([]task.RGB, 16)
	for i := range src {
		src[i] = task.RGB{R: 2}
	}
	out, ow, oh, err := b.Downsample(src, 4, 4)
	if err != nil {
		t.Fatalf("Downsample returned error: %v", err)
	}
	if ow != 2 || oh != 2 {
		t.Fatalf("Downsample produced %dx%d, want 2x2", ow, oh)
	}
	if len(out) != 4 {
		t.Fatalf("Downsample returned %d samples, want 4", len(out))
	}
}

func TestDownsampleAveragesCell(t *testing.T) {
	b := task.NewBloomChain(3)
	src := make([]task.RGB, 16)
	for i := range src {
		src[i] = task.RGB{R: 0.5, G: 0.5, B: 0.5}
	}
	out, _, _, err := b.Downsample(src, 4, 4)
	if err != nil {
		t.Fatalf("Downsample returned error: %v", err)
	}
	for i, c := range out {
		if !closeEnough(c.R, 0.5, 1e-9) || !closeEnough(c.G, 0.5, 1e-9) || !closeEnough(c.B, 0.5, 1e-9) {
			t.Fatalf("cell %d averaged to %+v, want 0.5 on every channel", i, c)
		}
	}
}

func TestUpsampleWeightedPrefersBrighterMip(t *testing.T) {
	b := task.NewBloomChain(3)
	levels := [][]task.RGB{{{R: 0}}, {{R: 4}}}
	got, err := b.UpsampleWeighted(levels, []float64{1, 3})
	if err != nil {
		t.Fatalf("UpsampleWeighted returned error: %v", err)
	}
	if got.R <= 2.5 || got.R > 4 {
		t.Fatalf("weighted upsample = %v, want a normalized value between the mip levels, near 3", got.R)
	}
}

func TestUpsampleRejectsWeightMismatch(t *testing.T) {
	b := task.NewBloomChain(3)
	levels := [][]task.RGB{{{R: 1}}, {{R: 2}}}
	if _, err := b.UpsampleWeighted(levels, []float64{1, 1}); err != nil {
		t.Fatalf("UpsampleWeighted rejected matching weights: %v", err)
	}
	if _, err := b.UpsampleWeighted(levels, []float64{1, 1, 1}); err == nil {
		t.Fatalf("UpsampleWeighted accepted 3 weights for 2 mip levels")
	}
}

func TestUIExcludedFromBloom(t *testing.T) {
	b := task.NewBloomChain(3)
	if b.IncludesUI() {
		t.Fatalf("UI pixels are part of the bloom chain")
	}
}

func TestMipCountMatchesRequest(t *testing.T) {
	b := task.NewBloomChain(5)
	if got := b.MipCount(); got != 5 {
		t.Fatalf("MipCount() = %d, want 5", got)
	}
}

func TestIntensityIsFrameRateIndependent(t *testing.T) {
	oneStep := task.NewBloomChain(4)
	twoSteps := task.NewBloomChain(4)
	at30 := oneStep.AccumulateIntensity(1.0/30.0, 1)
	at60 := twoSteps.AccumulateIntensity(1.0/60.0, 1)
	at60 = twoSteps.AccumulateIntensity(1.0/60.0, 1)
	if at30 <= 0 || at60 <= 0 {
		t.Fatalf("bloom intensity is not rising: 30Hz=%v 60Hz=%v", at30, at60)
	}
	if !closeEnough(at30, at60, 0.02) {
		t.Fatalf("bloom intensity depends on frame rate: 30Hz=%v 60Hz=%v", at30, at60)
	}
}

func TestTonemapHighlightRollsOff(t *testing.T) {
	b := task.NewBloomChain(3)
	got := b.TonemapHighlight(task.RGB{R: 10, G: 10, B: 10})
	if got.R > 1 || got.G > 1 || got.B > 1 {
		t.Fatalf("tonemapped highlight %+v is still above 1.0, it clips to white", got)
	}
	if got.R < 0.5 {
		t.Fatalf("tonemapped highlight %v is crushed to black", got.R)
	}
}

func TestClearBuffersMarksClean(t *testing.T) {
	b := task.NewBloomChain(3)
	if err := b.ClearBuffers(); err != nil {
		t.Fatalf("ClearBuffers returned error: %v", err)
	}
	if !b.BuffersClean() {
		t.Fatalf("bloom buffers still hold the previous frame after ClearBuffers")
	}
}

func TestCameraSwitchResetsAccumulation(t *testing.T) {
	b := task.NewBloomChain(3)
	if err := b.AdvanceFrame(); err != nil {
		t.Fatalf("AdvanceFrame returned error: %v", err)
	}
	if err := b.AdvanceFrame(); err != nil {
		t.Fatalf("AdvanceFrame returned error: %v", err)
	}
	if got := b.AccumulatedFrame(); got != 2 {
		t.Fatalf("AccumulatedFrame() = %d after two frames, want 2", got)
	}
	if err := b.SetCamera(1); err != nil {
		t.Fatalf("SetCamera returned error: %v", err)
	}
	if got := b.AccumulatedFrame(); got != 0 {
		t.Fatalf("AccumulatedFrame() = %d after a camera switch, the old bloom survived", got)
	}
}

func TestExposureRunsBeforeBloom(t *testing.T) {
	b := task.NewBloomChain(3)
	if !b.ExposureBeforeBloom() {
		t.Fatalf("bloom runs before exposure, the bloom intensity depends on the frame exposure")
	}
}

func TestBloomCompositesBeforeTransparent(t *testing.T) {
	b := task.NewBloomChain(3)
	if b.CompositeAfterTransparent() {
		t.Fatalf("bloom is composited after the transparent pass and reads a wrong depth buffer")
	}
}
