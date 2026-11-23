package task

import (
	"testing"

	task "gsb/batch45-game-b-22"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func constant(n int, v float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestKWeightLowFrequency(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if got := n.KWeight(50); !near(got, 0.708, 1e-3) {
		t.Fatalf("KWeight(50 Hz) = %v, want about 0.708", got)
	}
}

func TestKWeightMidBand(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if got := n.KWeight(1000); !near(got, 1.0, 1e-6) {
		t.Fatalf("KWeight(1 kHz) = %v, want 1.0", got)
	}
}

func TestIntegratedLUFSOfConstantSignal(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	got := n.IntegratedLUFS(constant(48000, 0.5))
	if !near(got, -6.02, 0.1) {
		t.Fatalf("IntegratedLUFS(0.5 full scale) = %v, want about -6.02", got)
	}
}

func TestMomentaryLUFSOfConstantSignal(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	got := n.MomentaryLUFS(constant(19200, 0.25))
	if !near(got, -12.04, 0.1) {
		t.Fatalf("MomentaryLUFS(0.25 full scale) = %v, want about -12.04", got)
	}
}

func TestLookaheadDelaysPeak(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	got := n.LookaheadDelay([]float32{0, 0, 1, 0}, 2)
	if len(got) != 6 {
		t.Fatalf("LookaheadDelay returned %d samples, want 6", len(got))
	}
	if got[4] != 1 {
		t.Fatalf("lookahead delay did not shift the peak to index 4: %v", got)
	}
}

func TestChainOrderNormalizeBeforeLimit(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	got := n.ChainOrder()
	if len(got) != 2 || got[0] != "normalize" || got[1] != "limit" {
		t.Fatalf("ChainOrder = %v, want [normalize limit]", got)
	}
}

func TestDownmixCenterWeight(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	got := n.Downmix([]float32{1}, []float32{0})
	if len(got) != 1 {
		t.Fatalf("Downmix returned %d samples, want 1", len(got))
	}
	if !near(float64(got[0]), 0.70710678, 1e-6) {
		t.Fatalf("Downmix(L=1, R=0) = %v, want about 0.7071", got[0])
	}
}

func TestGainIndependentOfSampleRate(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	a := n.GainForSampleRate(0.5, 44100)
	b := n.GainForSampleRate(0.5, 48000)
	if !near(a, 0.5, 1e-9) || !near(b, 0.5, 1e-9) {
		t.Fatalf("GainForSampleRate = (%v, %v), want (0.5, 0.5)", a, b)
	}
}

func TestSmoothGainRamp(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if got := n.SmoothGain(0, 1, 0.1, 0.5); !near(got, 0.2, 1e-9) {
		t.Fatalf("SmoothGain(0, 1, dt=0.1, ramp=0.5) = %v, want 0.2", got)
	}
}

func TestChainsSeparatePerContext(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if !n.SharedChain("combat", "combat") {
		t.Fatal("the same context must reuse its measurement chain")
	}
	if n.SharedChain("combat", "cutscene") {
		t.Fatal("cutscene and combat share one measurement chain")
	}
}

func TestLimiterReleaseTime(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if got := n.LimiterReleaseTime(); !near(got, 250, 1e-9) {
		t.Fatalf("LimiterReleaseTime = %v ms, want 250", got)
	}
}

func TestLimiterLatency(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if got := n.LimiterLatency(50); got != 50 {
		t.Fatalf("LimiterLatency(50) = %d, want 50", got)
	}
}

func TestResetClearsMeasurement(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if err := n.AddBlock(constant(19200, 0.1)); err != nil {
		t.Fatalf("AddBlock returned error: %v", err)
	}
	if got := n.BlockCount(); got != 1 {
		t.Fatalf("BlockCount = %d after one block, want 1", got)
	}
	if err := n.ResetMeasurement(); err != nil {
		t.Fatalf("ResetMeasurement returned error: %v", err)
	}
	if got := n.BlockCount(); got != 0 {
		t.Fatalf("BlockCount after reset = %d, want 0", got)
	}
}

func TestTargetPerPlatform(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if got := n.TargetForPlatform("ebu"); !near(got, -23, 1e-9) {
		t.Fatalf("TargetForPlatform(ebu) = %v, want -23", got)
	}
}

func TestSecondPassIsNoOp(t *testing.T) {
	n := task.NewLoudnessNormalizer(-14, 48000)
	if !n.IsSecondPass(-14, -14) {
		t.Fatal("a gain already at target must be treated as a no-op")
	}
	if n.IsSecondPass(-20, -14) {
		t.Fatal("a gain 6 LU below target must not be treated as a no-op")
	}
}
