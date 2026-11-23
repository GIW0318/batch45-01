package task

import (
	"sync"
	"testing"

	task "gsb/batch45-game-b-24"
)

func TestAcquireAllocatesRequestedResolution(t *testing.T) {
	p := task.NewSurfacePool(8)
	s, err := p.Acquire(1920, 1080, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if s.Width != 1920 || s.Height != 1080 {
		t.Fatalf("surface is %dx%d, want 1920x1080 after a bitrate switch", s.Width, s.Height)
	}
}

func TestStatsCountsUsedSurfaces(t *testing.T) {
	p := task.NewSurfacePool(8)
	if _, err := p.Acquire(1280, 720, "nv12"); err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if _, err := p.Acquire(1280, 720, "nv12"); err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	used, free := p.Stats()
	if used != 2 || free != 6 {
		t.Fatalf("Stats = (%d used, %d free), want (2, 6)", used, free)
	}
}

func TestReclaimAfterBitrateSwitch(t *testing.T) {
	p := task.NewSurfacePool(8)
	for i := 0; i < 2; i++ {
		s, err := p.Acquire(1920, 1080, "nv12")
		if err != nil {
			t.Fatalf("Acquire returned error: %v", err)
		}
		if err := p.GPUDone(s); err != nil {
			t.Fatalf("GPUDone returned error: %v", err)
		}
		if err := p.Release(s); err != nil {
			t.Fatalf("Release returned error: %v", err)
		}
	}
	if _, err := p.Acquire(1280, 720, "nv12"); err != nil {
		t.Fatalf("Acquire after bitrate switch returned error: %v", err)
	}
	if freed := p.Reclaim(); freed <= 0 {
		t.Fatalf("Reclaim freed %d surfaces, want the stale 1080p surfaces released", freed)
	}
}

func TestFrameBusyForSecondDecoder(t *testing.T) {
	p := task.NewSurfacePool(4)
	s, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if err := p.AcquireFrame(s, 1); err != nil {
		t.Fatalf("first AcquireFrame returned error: %v", err)
	}
	if err := p.AcquireFrame(s, 2); err == nil {
		t.Fatal("the same frame was handed to a second decoder")
	}
}

func TestReleaseWaitsForGPUFence(t *testing.T) {
	p := task.NewSurfacePool(4)
	s, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if err := p.GPUDone(s); err != nil {
		t.Fatalf("GPUDone returned error: %v", err)
	}
	if err := p.Release(s); err != nil {
		t.Fatalf("Release after GPUDone returned error: %v", err)
	}
	s2, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("second Acquire returned error: %v", err)
	}
	if err := p.Release(s2); err == nil {
		t.Fatal("surface was recycled before the GPU finished with it")
	}
}

func TestGrowRespectsMaxSize(t *testing.T) {
	p := task.NewSurfacePool(2)
	if err := p.Grow(1280, 720); err != nil {
		t.Fatalf("Grow within the limit returned error: %v", err)
	}
	if err := p.Grow(1280, 720); err != nil {
		t.Fatalf("Grow within the limit returned error: %v", err)
	}
	if err := p.Grow(1280, 720); err == nil {
		t.Fatal("Grow exceeded the pool limit")
	}
}

func TestResetRebuildsPool(t *testing.T) {
	p := task.NewSurfacePool(4)
	if _, err := p.Acquire(1280, 720, "nv12"); err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if err := p.Reset(); err != nil {
		t.Fatalf("Reset returned error: %v", err)
	}
	used, free := p.Stats()
	if used != 0 || free != 0 {
		t.Fatalf("Stats after Reset = (%d, %d), want (0, 0)", used, free)
	}
}

func TestTracksIsolated(t *testing.T) {
	p := task.NewSurfacePool(8)
	for i := 0; i < 2; i++ {
		if _, err := p.AcquireForTrack(1, 1280, 720); err != nil {
			t.Fatalf("AcquireForTrack(1) returned error: %v", err)
		}
	}
	if _, err := p.AcquireForTrack(2, 1280, 720); err != nil {
		t.Fatalf("AcquireForTrack(2) returned error: %v", err)
	}
	if got := p.UsedByTrack(1); got != 2 {
		t.Fatalf("UsedByTrack(1) = %d, want 2", got)
	}
	if got := p.UsedByTrack(2); got != 1 {
		t.Fatalf("UsedByTrack(2) = %d, want 1", got)
	}
}

func TestFormatChangeInvalidates(t *testing.T) {
	p := task.NewSurfacePool(4)
	s, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if err := p.GPUDone(s); err != nil {
		t.Fatalf("GPUDone returned error: %v", err)
	}
	if err := p.Release(s); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	if err := p.FormatChanged("p010"); err != nil {
		t.Fatalf("FormatChanged returned error: %v", err)
	}
	if _, free := p.Stats(); free != 0 {
		t.Fatalf("%d surfaces survived a pixel format change", free)
	}
}

func TestTryAcquireNonBlockingWhenFull(t *testing.T) {
	p := task.NewSurfacePool(2)
	for i := 0; i < 2; i++ {
		if _, err := p.Acquire(1280, 720, "nv12"); err != nil {
			t.Fatalf("Acquire returned error: %v", err)
		}
	}
	if _, ok := p.TryAcquire(1280, 720); ok {
		t.Fatal("TryAcquire succeeded on a full pool, it must not block or over-allocate")
	}
}

func TestConcurrentRefCount(t *testing.T) {
	p := task.NewSurfacePool(4)
	s, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.AddRef(s)
		}()
	}
	wg.Wait()
	if s.RefCount != 100 {
		t.Fatalf("RefCount = %d after 100 concurrent AddRef, want 100", s.RefCount)
	}
}

func TestFailDecodeReleasesSurface(t *testing.T) {
	p := task.NewSurfacePool(4)
	s, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if err := p.FailDecode(s); err != nil {
		t.Fatalf("FailDecode returned error: %v", err)
	}
	if used, _ := p.Stats(); used != 0 {
		t.Fatalf("%d surfaces still in use after a failed decode", used)
	}
}

func TestUsedPlusFreeEqualsTotal(t *testing.T) {
	p := task.NewSurfacePool(4)
	if _, err := p.Acquire(1280, 720, "nv12"); err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	used, free := p.Stats()
	if used+free != p.Total() {
		t.Fatalf("used %d + free %d != total %d", used, free, p.Total())
	}
	if p.Total() != 4 {
		t.Fatalf("Total = %d, want 4", p.Total())
	}
}

func TestAcquireDistinctSurfaces(t *testing.T) {
	p := task.NewSurfacePool(4)
	a, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	b, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("two live decoders received the same surface id %d", a.ID)
	}
}

func TestDoubleReleaseRejected(t *testing.T) {
	p := task.NewSurfacePool(4)
	s, err := p.Acquire(1280, 720, "nv12")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if err := p.GPUDone(s); err != nil {
		t.Fatalf("GPUDone returned error: %v", err)
	}
	if err := p.Release(s); err != nil {
		t.Fatalf("first Release returned error: %v", err)
	}
	if err := p.Release(s); err == nil {
		t.Fatal("double release returned the same surface to the pool twice")
	}
}
