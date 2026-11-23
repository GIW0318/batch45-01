package task

import (
	"sync"
	"testing"

	task "gsb/batch45-game-b-20"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestLimitSoftClipsHotSignal(t *testing.T) {
	m := task.NewMixerGuard(16)
	if got := m.Limit(2.0); !near(float64(got), 2.0/3.0, 1e-6) {
		t.Fatalf("Limit(2.0) = %v, want about 0.667", got)
	}
}

func TestLimitPassesQuietSignal(t *testing.T) {
	m := task.NewMixerGuard(16)
	if got := m.Limit(0.5); !near(float64(got), 0.5, 1e-6) {
		t.Fatalf("Limit(0.5) = %v, want 0.5", got)
	}
}

func TestVoiceChannelNotStarvedBySfx(t *testing.T) {
	m := task.NewMixerGuard(2)
	if _, err := m.AllocateVoice("sfx", 1); err != nil {
		t.Fatalf("first sfx allocation failed: %v", err)
	}
	if _, err := m.AllocateVoice("sfx", 1); err != nil {
		t.Fatalf("second sfx allocation failed: %v", err)
	}
	if _, err := m.AllocateVoice("voice", 10); err != nil {
		t.Fatalf("voice channel starved by sfx channels: %v", err)
	}
}

func TestStealVoicePicksQuietest(t *testing.T) {
	m := task.NewMixerGuard(3)
	for i := 0; i < 3; i++ {
		if _, err := m.AllocateVoice("sfx", 1); err != nil {
			t.Fatalf("AllocateVoice(%d) returned error: %v", i, err)
		}
	}
	for id, amp := range map[int]float32{0: 0.9, 1: 0.1, 2: 0.5} {
		if err := m.SetVoiceAmplitude(id, amp); err != nil {
			t.Fatalf("SetVoiceAmplitude(%d) returned error: %v", id, err)
		}
	}
	got, ok := m.StealVoice()
	if !ok {
		t.Fatal("StealVoice found no candidate while three voices played")
	}
	if got != 1 {
		t.Fatalf("StealVoice picked voice %d, want the quietest voice 1", got)
	}
}

func TestPhaseOffsetsDecorrelated(t *testing.T) {
	m := task.NewMixerGuard(4)
	p0 := m.PhaseOffset(0)
	p1 := m.PhaseOffset(1)
	if p1 <= p0 {
		t.Fatalf("PhaseOffset(1) = %v is not greater than PhaseOffset(0) = %v", p1, p0)
	}
	if p1 < 0 || p1 >= 1 {
		t.Fatalf("PhaseOffset(1) = %v is outside [0, 1)", p1)
	}
}

func TestClearBufferZeros(t *testing.T) {
	m := task.NewMixerGuard(4)
	buf := []float32{1, -1, 0.5, 0.25}
	got := m.ClearBuffer(buf)
	if len(got) != 4 {
		t.Fatalf("ClearBuffer returned %d samples, want 4", len(got))
	}
	for i, v := range got {
		if v != 0 {
			t.Fatalf("ClearBuffer left %v at index %d", v, i)
		}
	}
}

func TestResampleChannelToRate(t *testing.T) {
	m := task.NewMixerGuard(4)
	got := m.ResampleChannelTo(make([]float32, 100), 22050, 44100)
	if len(got) != 200 {
		t.Fatalf("ResampleChannelTo(100 @22050->44100) produced %d samples, want 200", len(got))
	}
}

func TestAttenuationUnityAtRef(t *testing.T) {
	m := task.NewMixerGuard(4)
	if got := m.Attenuation(10, 10, 1); !near(got, 1.0, 1e-9) {
		t.Fatalf("Attenuation at reference distance = %v, want 1.0", got)
	}
}

func TestAttenuationFallsWithDistance(t *testing.T) {
	m := task.NewMixerGuard(4)
	if got := m.Attenuation(20, 10, 1); !near(got, 0.5, 1e-9) {
		t.Fatalf("Attenuation at twice the reference distance = %v, want 0.5", got)
	}
}

func TestConcurrentSampleLoad(t *testing.T) {
	m := task.NewMixerGuard(4)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.AddSample("s", []float32{float32(i)})
		}(i)
	}
	wg.Wait()
	if got := m.SampleCount(); got != 100 {
		t.Fatalf("SampleCount = %d after 100 concurrent loads, want 100", got)
	}
}

func TestDuckPriorityForVoice(t *testing.T) {
	m := task.NewMixerGuard(4)
	if got := m.DuckPriority(5, true); got != 4 {
		t.Fatalf("DuckPriority(5, voice active) = %d, want 4", got)
	}
}

func TestRenderSamplesPreallocated(t *testing.T) {
	m := task.NewMixerGuard(4)
	got := m.RenderSamples(64)
	if len(got) != 64 {
		t.Fatalf("RenderSamples(64) returned %d samples", len(got))
	}
	if cap(got) < 64 {
		t.Fatalf("RenderSamples(64) returned cap %d, buffer was allocated inside the callback", cap(got))
	}
}

func TestMixBudgetDropsExcess(t *testing.T) {
	m := task.NewMixerGuard(4)
	got, err := m.MixBudget(1024, 512)
	if err == nil {
		t.Fatal("MixBudget did not report a budget overflow")
	}
	if got != 512 {
		t.Fatalf("MixBudget rendered %d frames, want 512", got)
	}
}

func TestPauseResumeKeepsPosition(t *testing.T) {
	m := task.NewMixerGuard(4)
	m.Advance(100)
	if got := m.Position(); got != 100 {
		t.Fatalf("Position after Advance(100) = %d, want 100", got)
	}
	if err := m.Pause(); err != nil {
		t.Fatalf("Pause returned error: %v", err)
	}
	m.Advance(50)
	if got := m.Position(); got != 100 {
		t.Fatalf("Position advanced while paused: %d, want 100", got)
	}
	if err := m.Resume(); err != nil {
		t.Fatalf("Resume returned error: %v", err)
	}
	m.Advance(10)
	if got := m.Position(); got != 110 {
		t.Fatalf("Position after resume = %d, want 110", got)
	}
}

func TestSumVoicesClamps(t *testing.T) {
	m := task.NewMixerGuard(4)
	got := m.SumVoices([][]float32{{1}, {1}})
	if len(got) != 1 {
		t.Fatalf("SumVoices returned %d samples, want 1", len(got))
	}
	if !near(float64(got[0]), 1.0, 1e-9) {
		t.Fatalf("SumVoices(1+1) = %v, want clamped 1.0", got[0])
	}
}
