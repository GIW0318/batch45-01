package task

import "errors"

var (
	ErrNotImplemented   = errors.New("not implemented")
	ErrUploadInProgress = errors.New("page upload already in progress")
	ErrUnknownPage      = errors.New("unknown virtual texture page")
	ErrDecalPoolFull    = errors.New("decal page pool is full")
)

// PageID identifies one virtual texture page at a mip level.
type PageID struct {
	Mip int
	X   int
	Y   int
}

// Feedback is the page request set of one frame.
type Feedback struct {
	Requests []PageID
}

// VirtualTexture streams virtual texture pages driven by page feedback.
type VirtualTexture struct{}

// NewVirtualTexture builds a texture with the given resident page budget.
func NewVirtualTexture(budget int) *VirtualTexture { return &VirtualTexture{} }

// Budget reports the resident page budget.
func (v *VirtualTexture) Budget() int { return 0 }

// SubmitFeedback applies page requests in the same frame.
func (v *VirtualTexture) SubmitFeedback(f Feedback) error { return ErrNotImplemented }

// FeedbackSize reports how many requests the current frame buffer holds.
func (v *VirtualTexture) FeedbackSize() int { return 0 }

// PendingLoads returns the queued page loads.
func (v *VirtualTexture) PendingLoads() []PageID { return nil }

// CompleteLoad marks a queued page as resident.
func (v *VirtualTexture) CompleteLoad(p PageID) error { return ErrNotImplemented }

// ResidentPages returns the resident pages.
func (v *VirtualTexture) ResidentPages() []PageID { return nil }

// IsResident reports whether a page is resident.
func (v *VirtualTexture) IsResident(p PageID) bool { return false }

// FrameEnd clears the per frame feedback buffer.
func (v *VirtualTexture) FrameEnd() error { return ErrNotImplemented }

// Reserve pins a resident page so that it cannot be evicted.
func (v *VirtualTexture) Reserve(p PageID) error { return ErrNotImplemented }

// Evict evicts the least recently requested page.
func (v *VirtualTexture) Evict() (PageID, bool) { return PageID{}, false }

// DroppedRequests reports how many requests were silently dropped.
func (v *VirtualTexture) DroppedRequests() int { return 0 }

// MarkLoadFailed records a failed load that must use the fallback page.
func (v *VirtualTexture) MarkLoadFailed(p PageID) error { return ErrNotImplemented }

// IsFallback reports whether a page renders with the fallback texture.
func (v *VirtualTexture) IsFallback(p PageID) bool { return false }

// SamplePage maps UV coordinates to a page, clamping out of range input.
func (v *VirtualTexture) SamplePage(u, texV float64, mip int) (PageID, error) {
	return PageID{}, ErrNotImplemented
}

// BeginUpload starts uploading a page, rejecting parallel uploads of it.
func (v *VirtualTexture) BeginUpload(p PageID) error { return ErrNotImplemented }

// EndUpload finishes uploading a page.
func (v *VirtualTexture) EndUpload(p PageID) error { return ErrNotImplemented }

// RequestDecal allocates a page from the separate decal pool.
func (v *VirtualTexture) RequestDecal(p PageID) error { return ErrNotImplemented }

// SwitchCamera rebuilds the page table for the new camera.
func (v *VirtualTexture) SwitchCamera(camID string) error { return ErrNotImplemented }

// PageTableVersion reports the page table generation.
func (v *VirtualTexture) PageTableVersion() int { return 0 }

// BarrierCount reports how many page table update barriers were inserted.
func (v *VirtualTexture) BarrierCount() int { return 0 }
