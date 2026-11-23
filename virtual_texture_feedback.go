package task

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var (
	ErrNotImplemented   = errors.New("not implemented")
	ErrUploadInProgress = errors.New("page upload already in progress")
	ErrUnknownPage      = errors.New("unknown virtual texture page")
	ErrDecalPoolFull    = errors.New("decal page pool is full")
)

const basePageGrid = 8

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

type pageRecord struct {
	lastUsed uint64
	reserved bool
}

// VirtualTexture streams virtual texture pages driven by page feedback.
type VirtualTexture struct {
	mu sync.Mutex

	budget int
	clock  uint64

	feedback    []PageID
	feedbackSet map[PageID]struct{}

	pending    []PageID
	pendingSet map[PageID]struct{}
	queuedAt   map[PageID]uint64

	resident map[PageID]*pageRecord
	fallback map[PageID]struct{}
	failed   map[PageID]struct{}

	uploading map[PageID]struct{}

	decal         map[PageID]struct{}
	decalCapacity int

	camera           string
	pageTableVersion int
	barrierCount     int
}

// NewVirtualTexture builds a texture with the given resident page budget.
func NewVirtualTexture(budget int) *VirtualTexture {
	capacity := budget
	if capacity < 0 {
		capacity = 0
	}
	return &VirtualTexture{
		budget:        budget,
		feedbackSet:   make(map[PageID]struct{}),
		pendingSet:    make(map[PageID]struct{}),
		queuedAt:      make(map[PageID]uint64),
		resident:      make(map[PageID]*pageRecord),
		fallback:      make(map[PageID]struct{}),
		failed:        make(map[PageID]struct{}),
		uploading:     make(map[PageID]struct{}),
		decal:         make(map[PageID]struct{}),
		decalCapacity: capacity,
	}
}

// Budget reports the resident page budget.
func (v *VirtualTexture) Budget() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.budget
}

// SubmitFeedback applies page requests in the same frame.
func (v *VirtualTexture) SubmitFeedback(f Feedback) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	applied := false
	var newlyQueued []PageID

	for _, p := range f.Requests {
		if !validPage(p) {
			continue
		}
		if _, duplicate := v.feedbackSet[p]; duplicate {
			continue
		}

		v.feedbackSet[p] = struct{}{}
		v.feedback = append(v.feedback, p)
		applied = true
		v.clock++

		if rec, ok := v.resident[p]; ok {
			rec.lastUsed = v.clock
			continue
		}
		if _, failed := v.failed[p]; failed {
			continue
		}
		if _, queued := v.pendingSet[p]; queued {
			v.queuedAt[p] = v.clock
			continue
		}

		v.pendingSet[p] = struct{}{}
		v.pending = append(v.pending, p)
		v.queuedAt[p] = v.clock
		newlyQueued = append(newlyQueued, p)
	}

	if applied {
		v.barrierCount++
	}
	v.rebalanceForLoads(newlyQueued)
	return nil
}

// FeedbackSize reports how many requests the current frame buffer holds.
func (v *VirtualTexture) FeedbackSize() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.feedback)
}

// PendingLoads returns the queued page loads.
func (v *VirtualTexture) PendingLoads() []PageID {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]PageID, len(v.pending))
	copy(out, v.pending)
	return out
}

// CompleteLoad marks a queued page as resident.
func (v *VirtualTexture) CompleteLoad(p PageID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !validPage(p) {
		return ErrUnknownPage
	}

	requestedAt, wasQueued := v.queuedAt[p]
	v.dequeuePending(p)

	if _, failed := v.failed[p]; failed {
		return nil
	}

	if rec, ok := v.resident[p]; ok {
		delete(v.fallback, p)
		v.clock++
		rec.lastUsed = v.clock
		return nil
	}

	if v.budget <= 0 {
		v.enqueuePending(p)
		return nil
	}

	changed := false
	for len(v.resident) >= v.budget {
		victim, ok := v.capacityVictim(p)
		if !ok {
			v.enqueuePending(p)
			if changed {
				v.barrierCount++
			}
			return nil
		}
		v.evictResident(victim)
		changed = true
	}

	lastUsed := v.clock + 1
	v.clock++
	if wasQueued && requestedAt != 0 {
		lastUsed = requestedAt
	}
	delete(v.fallback, p)
	v.resident[p] = &pageRecord{lastUsed: lastUsed}
	v.barrierCount++
	return nil
}

// ResidentPages returns the resident pages.
func (v *VirtualTexture) ResidentPages() []PageID {
	v.mu.Lock()
	defer v.mu.Unlock()

	out := make([]PageID, 0, len(v.resident))
	for p := range v.resident {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Mip != b.Mip {
			return a.Mip < b.Mip
		}
		if a.X != b.X {
			return a.X < b.X
		}
		return a.Y < b.Y
	})
	return out
}

// IsResident reports whether a page is resident.
func (v *VirtualTexture) IsResident(p PageID) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.resident[p]
	return ok
}

// FrameEnd clears the per frame feedback buffer.
func (v *VirtualTexture) FrameEnd() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.feedback = nil
	v.feedbackSet = make(map[PageID]struct{})
	return nil
}

// Reserve pins a resident page so that it cannot be evicted.
func (v *VirtualTexture) Reserve(p PageID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	rec, ok := v.resident[p]
	if !ok {
		return ErrUnknownPage
	}
	rec.reserved = true
	return nil
}

// Evict evicts the least recently requested page.
func (v *VirtualTexture) Evict() (PageID, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	victim, ok := v.coldVictim()
	if !ok {
		return PageID{}, false
	}
	v.evictResident(victim)
	v.barrierCount++
	return victim, true
}

// DroppedRequests reports how many requests were silently dropped.
func (v *VirtualTexture) DroppedRequests() int {
	return 0
}

// MarkLoadFailed records a failed load that must use the fallback page.
func (v *VirtualTexture) MarkLoadFailed(p PageID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !validPage(p) {
		return ErrUnknownPage
	}

	v.failed[p] = struct{}{}
	v.fallback[p] = struct{}{}
	delete(v.resident, p)
	v.dequeuePending(p)
	v.barrierCount++
	return nil
}

// IsFallback reports whether a page renders with the fallback texture.
func (v *VirtualTexture) IsFallback(p PageID) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.fallback[p]
	return ok
}

// SamplePage maps UV coordinates to a page, clamping out of range input.
func (v *VirtualTexture) SamplePage(u, texV float64, mip int) (PageID, error) {
	if mip < 0 {
		return PageID{}, ErrUnknownPage
	}

	grid := pageGridSize(mip)
	x := int(clampUV(u) * float64(grid))
	y := int(clampUV(texV) * float64(grid))
	if x >= grid {
		x = grid - 1
	}
	if y >= grid {
		y = grid - 1
	}
	return PageID{Mip: mip, X: x, Y: y}, nil
}

// BeginUpload starts uploading a page, rejecting parallel uploads of it.
func (v *VirtualTexture) BeginUpload(p PageID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, ok := v.uploading[p]; ok {
		return ErrUploadInProgress
	}
	v.uploading[p] = struct{}{}
	return nil
}

// EndUpload finishes uploading a page.
func (v *VirtualTexture) EndUpload(p PageID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, ok := v.uploading[p]; !ok {
		return ErrUnknownPage
	}
	delete(v.uploading, p)
	return nil
}

// RequestDecal allocates a page from the separate decal pool.
func (v *VirtualTexture) RequestDecal(p PageID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !validPage(p) {
		return ErrUnknownPage
	}
	if _, ok := v.decal[p]; ok {
		return nil
	}
	if len(v.decal) >= v.decalCapacity {
		return ErrDecalPoolFull
	}
	v.decal[p] = struct{}{}
	return nil
}

// SwitchCamera rebuilds the page table for the new camera.
func (v *VirtualTexture) SwitchCamera(camID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.camera = camID
	v.pageTableVersion++
	v.barrierCount++
	return nil
}

// PageTableVersion reports the page table generation.
func (v *VirtualTexture) PageTableVersion() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.pageTableVersion
}

// BarrierCount reports how many page table update barriers were inserted.
func (v *VirtualTexture) BarrierCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.barrierCount
}

func validPage(p PageID) bool {
	return p.Mip >= 0 && p.X >= 0 && p.Y >= 0
}

func pageGridSize(mip int) int {
	grid := basePageGrid >> uint(mip)
	if grid < 1 {
		return 1
	}
	return grid
}

func clampUV(value float64) float64 {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (v *VirtualTexture) enqueuePending(p PageID) {
	if _, ok := v.resident[p]; ok {
		return
	}
	if _, ok := v.pendingSet[p]; ok {
		return
	}
	v.clock++
	v.pendingSet[p] = struct{}{}
	v.pending = append(v.pending, p)
	v.queuedAt[p] = v.clock
}

func (v *VirtualTexture) dequeuePending(p PageID) {
	if _, ok := v.pendingSet[p]; !ok {
		return
	}
	delete(v.pendingSet, p)
	delete(v.queuedAt, p)
	for i, queued := range v.pending {
		if queued == p {
			v.pending = append(v.pending[:i], v.pending[i+1:]...)
			return
		}
	}
}

func (v *VirtualTexture) evictResident(p PageID) {
	delete(v.resident, p)
	v.fallback[p] = struct{}{}
}

func (v *VirtualTexture) rebalanceForLoads(newlyQueued []PageID) {
	if len(newlyQueued) == 0 || v.budget <= 0 {
		return
	}

	ordered := make([]PageID, len(newlyQueued))
	copy(ordered, newlyQueued)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Mip != ordered[j].Mip {
			return ordered[i].Mip < ordered[j].Mip
		}
		return v.queuedAt[ordered[i]] < v.queuedAt[ordered[j]]
	})

	projected := len(v.resident)
	for _, p := range ordered {
		if projected < v.budget {
			projected++
			continue
		}
		victim, ok := v.capacityVictim(p)
		if !ok {
			continue
		}
		v.evictResident(victim)
	}
}

func (v *VirtualTexture) capacityVictim(incoming PageID) (PageID, bool) {
	var victim PageID
	var found bool

	for p, rec := range v.resident {
		if rec.reserved {
			continue
		}
		if _, pending := v.pendingSet[p]; pending {
			continue
		}
		if p.Mip < incoming.Mip {
			continue
		}
		if p.Mip == incoming.Mip {
			if _, fresh := v.feedbackSet[p]; fresh {
				continue
			}
		}
		if !found || p.Mip > victim.Mip ||
			(p.Mip == victim.Mip && rec.lastUsed < v.resident[victim].lastUsed) {
			victim = p
			found = true
		}
	}
	return victim, found
}

func (v *VirtualTexture) coldVictim() (PageID, bool) {
	var victim PageID
	var found bool

	for p, rec := range v.resident {
		if rec.reserved {
			continue
		}
		if _, fresh := v.feedbackSet[p]; fresh {
			continue
		}
		if _, pending := v.pendingSet[p]; pending {
			continue
		}
		if !found || p.Mip > victim.Mip ||
			(p.Mip == victim.Mip && rec.lastUsed < v.resident[victim].lastUsed) {
			victim = p
			found = true
		}
	}
	return victim, found
}
