package task

import (
	"testing"

	task "gsb/batch45-game-b-14"
)

func closeTo(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func rampLut(size int) task.Lut3D {
	n := size * size * size
	data := make([]task.RGB, n)
	for i := 0; i < n; i++ {
		data[i] = task.RGB{R: float64(i) / float64(n-1)}
	}
	return task.Lut3D{Size: size, Data: data}
}

func TestSampleInterpolatesTrilinearly(t *testing.T) {
	s := task.NewLutSampler(2)
	lut := rampLut(2)
	corner, err := s.Sample(lut, task.RGB{})
	if err != nil {
		t.Fatalf("Sample(corner) returned error: %v", err)
	}
	if !closeTo(corner.R, 0, 1e-9) {
		t.Fatalf("corner sample = %v, want 0", corner.R)
	}
	mid, err := s.Sample(lut, task.RGB{R: 0.5, G: 0.5, B: 0.5})
	if err != nil {
		t.Fatalf("Sample(mid) returned error: %v", err)
	}
	if !closeTo(mid.R, 0.5, 1e-9) {
		t.Fatalf("mid cell sample = %v, want the interpolated 0.5", mid.R)
	}
}

func TestSampleClampsLowEdge(t *testing.T) {
	s := task.NewLutSampler(2)
	lut := rampLut(2)
	out, err := s.Sample(lut, task.RGB{R: -2, G: 0, B: 0})
	if err != nil {
		t.Fatalf("Sample returned error: %v", err)
	}
	if !closeTo(out.R, 0, 1e-9) {
		t.Fatalf("out of range low sample = %v, want the clamped 0, the atlas wrapped into the next cell", out.R)
	}
}

func TestSampleClampsHighEdge(t *testing.T) {
	s := task.NewLutSampler(2)
	lut := rampLut(2)
	out, err := s.Sample(lut, task.RGB{R: 2, G: 1, B: 1})
	if err != nil {
		t.Fatalf("Sample returned error: %v", err)
	}
	if !closeTo(out.R, 1, 1e-9) {
		t.Fatalf("out of range high sample = %v, want the clamped 1", out.R)
	}
}

func TestSampleRejectsEmptyLut(t *testing.T) {
	s := task.NewLutSampler(2)
	if _, err := s.Sample(rampLut(2), task.RGB{R: 0.5}); err != nil {
		t.Fatalf("Sample rejected a valid LUT: %v", err)
	}
	empty := task.Lut3D{}
	if _, err := s.Sample(empty, task.RGB{R: 0.5}); err == nil {
		t.Fatalf("Sample accepted a LUT with size 0")
	}
}

func TestUpdateIsAtomicAndBumpsVersion(t *testing.T) {
	s := task.NewLutSampler(2)
	lut := rampLut(2)
	before := s.Version()
	if err := s.Update(&lut, rampLut(2).Data); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	after := s.Version()
	if after == before {
		t.Fatalf("LUT version stayed %d after an update", after)
	}
	inverted := make([]task.RGB, 8)
	for i := range inverted {
		inverted[i] = task.RGB{R: 1}
	}
	if err := s.Update(&lut, inverted); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, err := s.Sample(lut, task.RGB{R: 0.5, G: 0.5, B: 0.5})
	if err != nil {
		t.Fatalf("Sample returned error: %v", err)
	}
	if !closeTo(got.R, 1, 1e-9) {
		t.Fatalf("sample = %v right after an update, the LUT is half new and half old", got.R)
	}
}

func TestUpdateRejectsWrongSize(t *testing.T) {
	s := task.NewLutSampler(2)
	lut := rampLut(2)
	if err := s.Update(&lut, rampLut(2).Data); err != nil {
		t.Fatalf("Update rejected a full 2x2x2 LUT: %v", err)
	}
	if err := s.Update(&lut, make([]task.RGB, 4)); err == nil {
		t.Fatalf("Update accepted 4 samples for a 2x2x2 LUT")
	}
}

func TestSRGBToLinearMidpoint(t *testing.T) {
	s := task.NewLutSampler(2)
	got := s.SRGBToLinear(task.RGB{R: 0.5, G: 0.5, B: 0.5})
	if !closeTo(got.R, 0.2140, 0.001) {
		t.Fatalf("sRGB 0.5 maps to %v, want about 0.2140 in linear space", got.R)
	}
	if got := s.SRGBToLinear(task.RGB{R: 1, G: 1, B: 1}); !closeTo(got.R, 1, 1e-9) {
		t.Fatalf("sRGB 1.0 maps to %v, want 1.0", got.R)
	}
}

func TestSamplerWorksInLinearSpace(t *testing.T) {
	s := task.NewLutSampler(2)
	if !s.SpaceIsLinear() {
		t.Fatalf("the LUT is sampled in sRGB encoded space, linear and sRGB are mixed")
	}
}

func TestSetSceneSwitchesLut(t *testing.T) {
	s := task.NewLutSampler(2)
	if err := s.SetScene(1); err != nil {
		t.Fatalf("SetScene(1) returned error: %v", err)
	}
	if got := s.SceneID(); got != 1 {
		t.Fatalf("SceneID() = %d after SetScene(1)", got)
	}
	before := s.SceneGeneration()
	if err := s.SetScene(2); err != nil {
		t.Fatalf("SetScene(2) returned error: %v", err)
	}
	if after := s.SceneGeneration(); after == before {
		t.Fatalf("scene generation stayed %d, the previous scene LUT is still bound", after)
	}
	if got := s.SceneID(); got != 2 {
		t.Fatalf("SceneID() = %d after SetScene(2), want 2", got)
	}
}

func TestPreviewMatchesFinalSampling(t *testing.T) {
	s := task.NewLutSampler(2)
	if !s.PreviewMatchesFinal() {
		t.Fatalf("the LUT preview and the final frame use different sampling")
	}
}

func TestAnimationAccumulatesByTime(t *testing.T) {
	oneStep := task.NewLutSampler(2)
	twoSteps := task.NewLutSampler(2)
	if err := oneStep.AdvanceAnimation(0.2); err != nil {
		t.Fatalf("AdvanceAnimation returned error: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := twoSteps.AdvanceAnimation(0.1); err != nil {
			t.Fatalf("AdvanceAnimation returned error: %v", err)
		}
	}
	a := oneStep.AnimationTime()
	b := twoSteps.AnimationTime()
	if a <= 0 || b <= 0 {
		t.Fatalf("animation time does not advance: %v and %v", a, b)
	}
	if !closeTo(a, b, 1e-9) {
		t.Fatalf("animation time depends on the frame count: %v vs %v", a, b)
	}
}

func TestAllocatePageReturnsDistinctPages(t *testing.T) {
	s := task.NewLutSampler(2)
	first, err := s.AllocatePage(64, 64)
	if err != nil {
		t.Fatalf("AllocatePage returned error: %v", err)
	}
	second, err := s.AllocatePage(64, 64)
	if err != nil {
		t.Fatalf("AllocatePage returned error: %v", err)
	}
	if first == second {
		t.Fatalf("both LUTs were placed on atlas page %d", first)
	}
}

func TestExportKeepsPrecision(t *testing.T) {
	s := task.NewLutSampler(2)
	lut := rampLut(2)
	out, err := s.ExportQuantized(lut, 10)
	if err != nil {
		t.Fatalf("ExportQuantized returned error: %v", err)
	}
	if len(out) != len(lut.Data) {
		t.Fatalf("ExportQuantized returned %d values for %d LUT entries", len(out), len(lut.Data))
	}
	last := out[len(out)-1]
	if last <= 255 {
		t.Fatalf("10 bit export stored %d for full white, precision was reduced to 8 bit", last)
	}
	if last > 1023 {
		t.Fatalf("10 bit export stored %d, above the 10 bit maximum", last)
	}
}

func TestLockPreventsConcurrentUpdate(t *testing.T) {
	s := task.NewLutSampler(2)
	if err := s.Lock(); err != nil {
		t.Fatalf("Lock returned error: %v", err)
	}
	if err := s.Lock(); err == nil {
		t.Fatalf("LUT locked twice, an update can read half new data")
	}
	if err := s.Unlock(); err != nil {
		t.Fatalf("Unlock returned error: %v", err)
	}
	if err := s.Lock(); err != nil {
		t.Fatalf("Lock after unlock returned error: %v", err)
	}
}

func TestLutAppliesBeforeUI(t *testing.T) {
	s := task.NewLutSampler(2)
	if !s.AppliesBeforeUI() {
		t.Fatalf("the colour LUT is applied after the UI pass and tints the HUD")
	}
}
