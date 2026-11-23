package task

import (
	"sync"
	"testing"

	task "gsb/batch45-game-b-25"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestSameGlyphCachedOnce(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	k := a.KeyFor('A', 16, 400)
	g1, err := a.Load(k)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	g2, err := a.Load(k)
	if err != nil {
		t.Fatalf("second Load returned error: %v", err)
	}
	if g1 != g2 {
		t.Fatal("the same glyph at the same size was rasterised twice")
	}
}

func TestPinnedGlyphNotEvicted(t *testing.T) {
	a := task.NewGlyphAtlas(2)
	ka := a.KeyFor('A', 16, 400)
	kb := a.KeyFor('B', 16, 400)
	kc := a.KeyFor('C', 16, 400)
	if _, err := a.Load(ka); err != nil {
		t.Fatalf("Load(A) returned error: %v", err)
	}
	a.Pin(ka)
	if _, err := a.Load(kb); err != nil {
		t.Fatalf("Load(B) returned error: %v", err)
	}
	if _, err := a.Load(kc); err != nil {
		t.Fatalf("Load(C) returned error: %v", err)
	}
	if _, ok := a.Get(ka); !ok {
		t.Fatal("a glyph that is still on screen was evicted from the atlas")
	}
}

func TestRowBytesFourByteAligned(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	if got := a.RowBytes(3); got != 4 {
		t.Fatalf("RowBytes(3) = %d, want 4", got)
	}
	if got := a.RowBytes(5); got != 8 {
		t.Fatalf("RowBytes(5) = %d, want 8", got)
	}
}

func TestWeightGetsSeparateSlot(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	k1 := a.KeyFor('A', 16, 400)
	k2 := a.KeyFor('A', 16, 700)
	if k1 == k2 {
		t.Fatalf("regular and bold share the key %+v", k1)
	}
	g1, err := a.Load(k1)
	if err != nil {
		t.Fatalf("Load(regular) returned error: %v", err)
	}
	g2, err := a.Load(k2)
	if err != nil {
		t.Fatalf("Load(bold) returned error: %v", err)
	}
	if g1 == g2 {
		t.Fatal("regular and bold masks share one atlas slot")
	}
}

func TestLigatureSingleCluster(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	got := a.Cluster("fi")
	if len(got) != 1 || got[0] != "fi" {
		t.Fatalf("Cluster(fi) = %v, want [fi]", got)
	}
}

func TestFallbackChainByLanguage(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	chain := a.FallbackChain("zh")
	if len(chain) == 0 {
		t.Fatal("FallbackChain(zh) is empty")
	}
	if chain[0] != "NotoSansCJK" {
		t.Fatalf("FallbackChain(zh)[0] = %q, want NotoSansCJK", chain[0])
	}
}

func TestSwapIncrementsGeneration(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	if err := a.SwapBuffer(); err != nil {
		t.Fatalf("SwapBuffer returned error: %v", err)
	}
	if got := a.Generation(); got != 1 {
		t.Fatalf("Generation after one swap = %d, want 1", got)
	}
}

func TestBaselineFromFontMetrics(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	if got := a.Baseline(16); !near(got, 12.8, 1e-6) {
		t.Fatalf("Baseline(16) = %v, want the 0.8 em ascent 12.8", got)
	}
}

func TestResizeInvalidatesLayout(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	k := a.KeyFor('A', 16, 400)
	if _, err := a.Load(k); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, ok := a.Get(k); !ok {
		t.Fatal("glyph not cached after Load")
	}
	if err := a.Resize(8); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}
	if _, ok := a.Get(k); ok {
		t.Fatal("stale atlas coordinates survived a resize")
	}
}

func TestConcurrentLoadNoDuplicateSlots(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	k := a.KeyFor('A', 16, 400)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = a.Load(k)
		}()
	}
	wg.Wait()
	if got := a.Count(); got != 1 {
		t.Fatalf("Count = %d after 50 concurrent loads of one glyph, want 1", got)
	}
}

func TestWrapUsesRuneWidth(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	got := a.Wrap("中文abc", 4)
	if len(got) != 2 {
		t.Fatalf("Wrap(中文abc, 4) = %v, want two lines", got)
	}
	if got[0] != "中文" || got[1] != "abc" {
		t.Fatalf("Wrap(中文abc, 4) = %v, want [中文 abc]", got)
	}
}

func TestColorEmojiFlagged(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	if !a.IsColorRune(0x1F600) {
		t.Fatal("an emoji codepoint was not flagged as colour")
	}
}

func TestColorEmojiNotMonochrome(t *testing.T) {
	a := task.NewGlyphAtlas(16)
	g, err := a.Load(a.KeyFor(0x1F600, 16, 400))
	if err != nil {
		t.Fatalf("Load(emoji) returned error: %v", err)
	}
	if !g.Color {
		t.Fatal("colour emoji was rasterised into a monochrome glyph")
	}
}

func TestCapacityIsBounded(t *testing.T) {
	a := task.NewGlyphAtlas(1)
	k1 := a.KeyFor('A', 16, 400)
	if _, err := a.Load(k1); err != nil {
		t.Fatalf("Load(A) returned error: %v", err)
	}
	a.Pin(k1)
	if _, err := a.Load(a.KeyFor('B', 16, 400)); err == nil {
		t.Fatal("loading past capacity evicted a glyph that was still on screen")
	}
}

func TestCoverageFraction(t *testing.T) {
	a := task.NewGlyphAtlas(4)
	if _, err := a.Load(a.KeyFor('A', 16, 400)); err != nil {
		t.Fatalf("Load(A) returned error: %v", err)
	}
	if _, err := a.Load(a.KeyFor('B', 16, 400)); err != nil {
		t.Fatalf("Load(B) returned error: %v", err)
	}
	if got := a.Coverage(); !near(got, 0.5, 1e-9) {
		t.Fatalf("Coverage after 2 of 4 slots = %v, want 0.5", got)
	}
}
