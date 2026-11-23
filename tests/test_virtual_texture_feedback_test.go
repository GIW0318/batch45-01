package task

import (
	"errors"
	"testing"

	impl "gsb/batch45-game-b-03"
)

func pid(mip, x, y int) impl.PageID {
	return impl.PageID{Mip: mip, X: x, Y: y}
}

func pendingHas(list []impl.PageID, want impl.PageID) bool {
	for _, p := range list {
		if p == want {
			return true
		}
	}
	return false
}

func complete(t *testing.T, v *impl.VirtualTexture, p impl.PageID) {
	t.Helper()
	if err := v.CompleteLoad(p); err != nil {
		t.Fatalf("CompleteLoad(%+v) error: %v", p, err)
	}
}

func TestFeedbackAppliedSameFrame(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	if err := v.SubmitFeedback(impl.Feedback{Requests: []impl.PageID{pid(0, 1, 1)}}); err != nil {
		t.Fatalf("SubmitFeedback error: %v", err)
	}
	if got := v.FeedbackSize(); got != 1 {
		t.Fatalf("feedback size = %d, want 1", got)
	}
	if !pendingHas(v.PendingLoads(), pid(0, 1, 1)) {
		t.Fatalf("page request was not applied in the same frame: %v", v.PendingLoads())
	}
}

func TestJustRequestedPageProtected(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	complete(t, v, pid(0, 0, 0))
	complete(t, v, pid(0, 1, 0))
	requested := pid(0, 2, 0)
	if err := v.SubmitFeedback(impl.Feedback{Requests: []impl.PageID{requested}}); err != nil {
		t.Fatalf("SubmitFeedback error: %v", err)
	}
	evicted, ok := v.Evict()
	if !ok {
		t.Fatalf("Evict reported nothing evictable while two cold pages existed")
	}
	if evicted == requested {
		t.Fatalf("freshly requested page was evicted as cold")
	}
	if !pendingHas(v.PendingLoads(), requested) {
		t.Fatalf("freshly requested page disappeared from the pending queue")
	}
}

func TestDuplicateRequestsDeduped(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	p := pid(0, 3, 3)
	if err := v.SubmitFeedback(impl.Feedback{Requests: []impl.PageID{p, p, p}}); err != nil {
		t.Fatalf("SubmitFeedback error: %v", err)
	}
	if got := len(v.PendingLoads()); got != 1 {
		t.Fatalf("pending loads = %d, want 1 deduped request", got)
	}
}

func TestOutOfRangeUVClamped(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	p, err := v.SamplePage(-0.25, 1.4, 0)
	if err != nil {
		t.Fatalf("SamplePage error: %v", err)
	}
	if p.Mip != 0 {
		t.Fatalf("sample mip = %d, want 0", p.Mip)
	}
	if p.X < 0 || p.X >= 8 || p.Y < 0 || p.Y >= 8 {
		t.Fatalf("sampled page %+v outside the mip 0 page grid", p)
	}
}

func TestResidentPinnedNotEvicted(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	p := pid(0, 4, 4)
	complete(t, v, p)
	if err := v.Reserve(p); err != nil {
		t.Fatalf("Reserve error: %v", err)
	}
	if _, ok := v.Evict(); ok {
		t.Fatalf("Evict removed a pinned resident page")
	}
	if !v.IsResident(p) {
		t.Fatalf("pinned page is no longer resident")
	}
}

func TestFeedbackClearedEachFrame(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	if err := v.SubmitFeedback(impl.Feedback{Requests: []impl.PageID{pid(0, 1, 1), pid(0, 2, 2)}}); err != nil {
		t.Fatalf("SubmitFeedback error: %v", err)
	}
	if got := v.FeedbackSize(); got != 2 {
		t.Fatalf("feedback size before frame end = %d, want 2", got)
	}
	if err := v.FrameEnd(); err != nil {
		t.Fatalf("FrameEnd error: %v", err)
	}
	if got := v.FeedbackSize(); got != 0 {
		t.Fatalf("feedback buffer not cleared at frame end: %d", got)
	}
}

func TestBarrierAfterPageTableUpdate(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	if err := v.SubmitFeedback(impl.Feedback{Requests: []impl.PageID{pid(0, 0, 1)}}); err != nil {
		t.Fatalf("SubmitFeedback error: %v", err)
	}
	if v.BarrierCount() < 1 {
		t.Fatalf("page table update did not insert a barrier")
	}
}

func TestBudgetQueuesInsteadOfDropping(t *testing.T) {
	v := impl.NewVirtualTexture(4)
	var requests []impl.PageID
	for i := 0; i < 10; i++ {
		requests = append(requests, pid(0, i%4, i/4))
	}
	if err := v.SubmitFeedback(impl.Feedback{Requests: requests}); err != nil {
		t.Fatalf("SubmitFeedback error: %v", err)
	}
	if got := v.DroppedRequests(); got != 0 {
		t.Fatalf("dropped requests = %d, want 0 (queue over budget instead)", got)
	}
	if got := len(v.PendingLoads()); got != 10 {
		t.Fatalf("pending loads = %d, want all 10 requests queued", got)
	}
}

func TestDecalPoolSeparate(t *testing.T) {
	v := impl.NewVirtualTexture(2)
	complete(t, v, pid(0, 0, 0))
	complete(t, v, pid(0, 1, 0))
	if got := len(v.ResidentPages()); got != 2 {
		t.Fatalf("resident pages = %d, want 2", got)
	}
	if err := v.RequestDecal(pid(1, 0, 0)); err != nil {
		t.Fatalf("decal request error: %v", err)
	}
	if got := len(v.ResidentPages()); got != 2 {
		t.Fatalf("decal request changed the virtual texture budget: %d residents", got)
	}
}

func TestLoadFailureUsesFallback(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	p := pid(0, 2, 1)
	if err := v.MarkLoadFailed(p); err != nil {
		t.Fatalf("MarkLoadFailed error: %v", err)
	}
	if !v.IsFallback(p) {
		t.Fatalf("failed page does not render the fallback texture")
	}
	if v.IsResident(p) {
		t.Fatalf("failed page was marked resident (would render black)")
	}
}

func TestCameraSwitchRebuildsTable(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	before := v.PageTableVersion()
	if err := v.SwitchCamera("cam-2"); err != nil {
		t.Fatalf("SwitchCamera error: %v", err)
	}
	if after := v.PageTableVersion(); after <= before {
		t.Fatalf("page table version %d did not grow past %d", after, before)
	}
}

func TestConcurrentUploadRejected(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	p := pid(0, 5, 5)
	if err := v.BeginUpload(p); err != nil {
		t.Fatalf("BeginUpload error: %v", err)
	}
	if err := v.BeginUpload(p); !errors.Is(err, impl.ErrUploadInProgress) {
		t.Fatalf("parallel upload error = %v, want ErrUploadInProgress", err)
	}
	if err := v.EndUpload(p); err != nil {
		t.Fatalf("EndUpload error: %v", err)
	}
	if err := v.BeginUpload(p); err != nil {
		t.Fatalf("upload after end error: %v", err)
	}
}

func TestEvictReturnsFalseWhenAllPinned(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	a := pid(0, 0, 0)
	b := pid(0, 1, 0)
	complete(t, v, a)
	complete(t, v, b)
	if err := v.Reserve(a); err != nil {
		t.Fatalf("Reserve(a) error: %v", err)
	}
	if err := v.Reserve(b); err != nil {
		t.Fatalf("Reserve(b) error: %v", err)
	}
	if evicted, ok := v.Evict(); ok {
		t.Fatalf("Evict returned %+v while every page is pinned", evicted)
	}
	if got := len(v.ResidentPages()); got != 2 {
		t.Fatalf("resident pages = %d, want 2", got)
	}
}

func TestEvictOldestResidentFirst(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	oldPage := pid(0, 0, 3)
	newPage := pid(0, 1, 3)
	complete(t, v, oldPage)
	complete(t, v, newPage)
	evicted, ok := v.Evict()
	if !ok {
		t.Fatalf("Evict reported nothing evictable")
	}
	if evicted != oldPage {
		t.Fatalf("evicted %+v, want the least recently requested %+v", evicted, oldPage)
	}
	if !v.IsResident(newPage) {
		t.Fatalf("newer page was evicted instead of the older one")
	}
}

func TestMipLevelsTrackedSeparately(t *testing.T) {
	v := impl.NewVirtualTexture(8)
	base := pid(0, 2, 2)
	mipOne := pid(1, 2, 2)
	complete(t, v, base)
	if !v.IsResident(base) {
		t.Fatalf("base mip page is not resident")
	}
	if v.IsResident(mipOne) {
		t.Fatalf("mip 1 page inherited the mip 0 residency")
	}
	if err := v.SubmitFeedback(impl.Feedback{Requests: []impl.PageID{mipOne}}); err != nil {
		t.Fatalf("SubmitFeedback error: %v", err)
	}
	if !pendingHas(v.PendingLoads(), mipOne) {
		t.Fatalf("mip 1 request missing from pending loads")
	}
}
