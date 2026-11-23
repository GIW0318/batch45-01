package task

import (
	"sync"
	"testing"

	task "gsb/batch45-game-b-16"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestNormalizeWeightsSumToOne(t *testing.T) {
	m := task.NewMorphMixer("hero")
	got := m.Normalize([]float32{0.6, 0.6})
	if len(got) != 2 {
		t.Fatalf("Normalize returned %d weights, want 2", len(got))
	}
	if !near(float64(got[0]), 0.5, 1e-6) || !near(float64(got[1]), 0.5, 1e-6) {
		t.Fatalf("Normalize = %v, want [0.5 0.5]", got)
	}
}

func TestNormalizeClampsNegativeWeights(t *testing.T) {
	m := task.NewMorphMixer("hero")
	got := m.Normalize([]float32{-0.5, 0.5, 0.5})
	if len(got) != 3 {
		t.Fatalf("Normalize returned %d weights, want 3", len(got))
	}
	if got[0] != 0 {
		t.Fatalf("negative weight normalized to %v, want 0", got[0])
	}
	if !near(float64(got[1]+got[2]), 1, 1e-6) {
		t.Fatalf("positive weights sum to %v, want 1", got[1]+got[2])
	}
}

func TestBlendOrderIsDeterministic(t *testing.T) {
	m := task.NewMorphMixer("hero")
	got := m.BlendOrder([]string{"smile", "angry", "blink"})
	want := []string{"angry", "blink", "smile"}
	if len(got) != len(want) {
		t.Fatalf("BlendOrder returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BlendOrder = %v, want %v", got, want)
		}
	}
	if again := m.BlendOrder([]string{"blink", "smile", "angry"}); len(again) != len(want) || again[0] != want[0] {
		t.Fatalf("BlendOrder is not stable across calls: %v", again)
	}
}

func TestSyncTimelineConvertsFPS(t *testing.T) {
	m := task.NewMorphMixer("hero")
	got := m.SyncTimeline(30, 30, 60)
	if !near(got, 60, 1e-9) {
		t.Fatalf("SyncTimeline(30, 30fps, 60fps) = %v, want 60", got)
	}
}

func TestCrossfadeMidpoint(t *testing.T) {
	m := task.NewMorphMixer("hero")
	got := m.Crossfade(0, 1, 0.5)
	if !near(float64(got), 0.5, 1e-6) {
		t.Fatalf("Crossfade midpoint = %v, want 0.5", got)
	}
}

func TestCrossfadeEndpoints(t *testing.T) {
	m := task.NewMorphMixer("hero")
	if got := m.Crossfade(0.25, 0.75, 0); !near(float64(got), 0.25, 1e-6) {
		t.Fatalf("Crossfade at t=0 = %v, want 0.25", got)
	}
	if got := m.Crossfade(0.25, 0.75, 1); !near(float64(got), 0.75, 1e-6) {
		t.Fatalf("Crossfade at t=1 = %v, want 0.75", got)
	}
}

func TestTargetForLODRemapsIndices(t *testing.T) {
	m := task.NewMorphMixer("hero")
	if err := m.SetTarget(0, "smile", []int{1, 2, 3}); err != nil {
		t.Fatalf("SetTarget LOD0 returned error: %v", err)
	}
	if err := m.SetTarget(1, "smile", []int{7, 8}); err != nil {
		t.Fatalf("SetTarget LOD1 returned error: %v", err)
	}
	got, err := m.TargetForLOD("smile", 1)
	if err != nil {
		t.Fatalf("TargetForLOD returned error: %v", err)
	}
	if len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Fatalf("TargetForLOD(smile, 1) = %v, want [7 8]", got)
	}
}

func TestTargetForLODUnknownTargetFails(t *testing.T) {
	m := task.NewMorphMixer("hero")
	if err := m.SetTarget(0, "smile", []int{1}); err != nil {
		t.Fatalf("SetTarget returned error: %v", err)
	}
	if _, err := m.TargetForLOD("frown", 0); err == nil {
		t.Fatal("TargetForLOD accepted an unregistered target")
	}
}

func TestSetWeightRejectsNegative(t *testing.T) {
	m := task.NewMorphMixer("hero")
	if err := m.SetWeight("smile", 0.5); err != nil {
		t.Fatalf("SetWeight(0.5) returned error: %v", err)
	}
	if err := m.SetWeight("smile", -0.2); err == nil {
		t.Fatal("SetWeight accepted a negative weight")
	}
	if w := m.Weight("smile"); !near(float64(w), 0.5, 1e-6) {
		t.Fatalf("weight after rejected update = %v, want 0.5", w)
	}
}

func TestCacheKeyIsolatedPerCharacter(t *testing.T) {
	a := task.NewMorphMixer("hero")
	b := task.NewMorphMixer("npc")
	ka := a.CacheKey("smile")
	kb := b.CacheKey("smile")
	if ka == "" {
		t.Fatal("CacheKey returned an empty key")
	}
	if ka == kb {
		t.Fatalf("CacheKey shared %q between two characters", ka)
	}
}

func TestConcurrentUpdateWeights(t *testing.T) {
	m := task.NewMorphMixer("hero")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.UpdateWeights(map[string]float32{"blink": 0.01})
		}()
	}
	wg.Wait()
	snap := m.Snapshot()
	v, ok := snap["blink"]
	if !ok {
		t.Fatalf("Snapshot lost the blink channel: %v", snap)
	}
	if !near(float64(v), 1.0, 1e-3) {
		t.Fatalf("accumulated blink weight = %v, want 1.0", v)
	}
}

func TestOnFinishedResetsWeight(t *testing.T) {
	m := task.NewMorphMixer("hero")
	if err := m.SetWeight("smile", 0.8); err != nil {
		t.Fatalf("SetWeight returned error: %v", err)
	}
	if !m.IsActive("smile") {
		t.Fatal("expression not active right after SetWeight")
	}
	m.OnFinished("smile")
	if m.IsActive("smile") {
		t.Fatal("expression still active after OnFinished")
	}
	if w := m.Weight("smile"); w != 0 {
		t.Fatalf("weight after OnFinished = %v, want 0", w)
	}
}

func TestDecompressWeightPrecision(t *testing.T) {
	m := task.NewMorphMixer("hero")
	got := m.DecompressWeight(16384, 1)
	if !near(float64(got), 0.5, 1e-3) {
		t.Fatalf("DecompressWeight(16384, 1) = %v, want about 0.5", got)
	}
}

func TestLipSyncFollowsAudioClock(t *testing.T) {
	m := task.NewMorphMixer("hero")
	got := m.LipSync(1.0, 2.0)
	if !near(got, 2.0, 1e-9) {
		t.Fatalf("LipSync(morph=1.0, audio=2.0) = %v, want 2.0", got)
	}
}

func TestShadowPassReusesColorWeights(t *testing.T) {
	m := task.NewMorphMixer("hero")
	if err := m.SetWeight("smile", 0.5); err != nil {
		t.Fatalf("SetWeight returned error: %v", err)
	}
	w := m.PassWeights("shadow")
	v, ok := w["smile"]
	if !ok {
		t.Fatalf("shadow pass has no smile weight: %v", w)
	}
	if !near(float64(v), 0.5, 1e-6) {
		t.Fatalf("shadow pass smile weight = %v, want 0.5", v)
	}
}
