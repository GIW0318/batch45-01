package task

import (
	"sync"
	"testing"

	task "gsb/batch45-game-b-19"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestRatioFromRates(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 2)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if got := r.Ratio(); !near(got, 48000.0/44100.0, 1e-9) {
		t.Fatalf("Ratio = %v, want %v", got, 48000.0/44100.0)
	}
}

func TestResamplePreservesChannelCount(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 2)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	out, err := r.Resample(make([]float32, 441*2))
	if err != nil {
		t.Fatalf("Resample returned error: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("Resample produced no samples")
	}
	if len(out)%2 != 0 {
		t.Fatalf("Resample produced %d samples, not a whole number of stereo frames", len(out))
	}
}

func TestResampleOutputLength(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	out, err := r.Resample(make([]float32, 441))
	if err != nil {
		t.Fatalf("Resample returned error: %v", err)
	}
	if len(out) != 480 {
		t.Fatalf("Resample(441 mono @44100->48000) produced %d samples, want 480", len(out))
	}
}

func TestNextFrameIndexNoDrift(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if got := r.NextFrameIndex(1000); got != 1088 {
		t.Fatalf("NextFrameIndex(1000) = %d, want 1088", got)
	}
}

func TestResetBufferPreservesPhase(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if _, err := r.Resample(make([]float32, 441)); err != nil {
		t.Fatalf("Resample returned error: %v", err)
	}
	phase := r.Phase()
	if phase == 0 {
		t.Fatal("Phase is 0 after resampling, the fractional position was dropped")
	}
	if err := r.ResetBuffer(); err != nil {
		t.Fatalf("ResetBuffer returned error: %v", err)
	}
	if got := r.Phase(); got != phase {
		t.Fatalf("Phase after ResetBuffer = %d, want %d", got, phase)
	}
}

func TestPadBoundaryAddsZeros(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	in := []float32{1, 2, 3, 4, 5}
	got := r.PadBoundary(in, 4)
	if len(got) != len(in)+4 {
		t.Fatalf("PadBoundary returned %d samples, want %d", len(got), len(in)+4)
	}
	if got[0] != 0 || got[1] != 0 || got[len(got)-1] != 0 {
		t.Fatalf("PadBoundary did not zero pad the edges: %v", got)
	}
	if got[2] != 1 {
		t.Fatalf("PadBoundary moved the first sample to %v, want 1 at index 2", got[2])
	}
}

func TestSmoothRatioRamp(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if got := r.SmoothRatio(1.0, 2.0, 0.05, 10); !near(got, 1.5, 1e-9) {
		t.Fatalf("SmoothRatio(1, 2, dt=0.05, rate=10) = %v, want 1.5", got)
	}
}

func TestConcurrentOutputWrites(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.WriteOutput(1)
		}()
	}
	wg.Wait()
	if got := r.OutputLen(); got != 100 {
		t.Fatalf("OutputLen = %d after 100 concurrent writes, want 100", got)
	}
}

func TestTimestampAfterResample(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if got := r.TimestampAt(48000); got != 1000000 {
		t.Fatalf("TimestampAt(48000) at 48 kHz = %d us, want 1000000", got)
	}
}

func TestUnderrunCompensation(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	buf := make([]float32, 4)
	got := r.FillUnderrun(buf, 3)
	if len(got) != 7 {
		t.Fatalf("FillUnderrun produced %d samples, want 7", len(got))
	}
}

func TestPipelineOrderMixThenResample(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if got := r.PipelineOrder(); got != "mix_then_resample" {
		t.Fatalf("PipelineOrder = %q, want mix_then_resample", got)
	}
}

func TestStableRatePicksMode(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if got := r.StableRate([]int{48000, 44100, 48000, 48000}); got != 48000 {
		t.Fatalf("StableRate = %d, want 48000", got)
	}
}

func TestRebuildAfterDeviceSwitch(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if err := r.Rebuild(48000, 48000, 2); err != nil {
		t.Fatalf("Rebuild returned error: %v", err)
	}
	if got := r.Ratio(); !near(got, 1.0, 1e-9) {
		t.Fatalf("Ratio after Rebuild = %v, want 1.0", got)
	}
}

func TestOverflowReturnsError(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	got, err := r.AppendClamped(nil, make([]float32, 4), 8)
	if err != nil {
		t.Fatalf("AppendClamped within capacity returned error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("AppendClamped appended %d samples, want 4", len(got))
	}
	if _, err := r.AppendClamped(make([]float32, 4), make([]float32, 6), 6); err == nil {
		t.Fatal("AppendClamped silently truncated an overflowing append")
	}
}

func TestFrameCountFromInterleaved(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 2)
	if err != nil {
		t.Fatalf("NewAudioResampler returned error: %v", err)
	}
	if got := r.FrameCount(make([]float32, 6)); got != 3 {
		t.Fatalf("FrameCount(6 stereo samples) = %d, want 3", got)
	}
}
