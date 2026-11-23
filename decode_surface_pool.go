package task

import (
	"errors"
	"sync"
)

var (
	ErrPoolFull          = errors.New("surface pool is full")
	ErrInvalidSurface    = errors.New("surface is not owned by this pool")
	ErrSurfaceNotInUse   = errors.New("surface is not in use")
	ErrGPUNotDone        = errors.New("gpu work on surface has not completed")
	ErrGPUAlreadyDone    = errors.New("gpu work on surface was already completed")
	ErrRefsHeld          = errors.New("surface still has references")
	ErrFrameBusy         = errors.New("frame is already owned by another decoder")
	ErrInvalidDimensions = errors.New("surface dimensions must be positive")
	ErrInvalidTrack      = errors.New("track id must be non-negative")
)

const idleKeep = 2

// Surface is one GPU decode surface owned by the pool.
type Surface struct {
	ID       int
	Width    int
	Height   int
	Format   string
	RefCount int
}

type surfaceState struct {
	inUse      bool
	gpuDone    bool
	frameOwned bool
	frameOwner int
	track      int // -1 means the surface is not reserved for a track
}

type surfaceRequest struct {
	width  int
	height int
	format string
}

// SurfacePool allocates and recycles hardware decode surfaces.
//
// All mutable state, including Surface.RefCount and the free lists, is guarded
// by mu. Surfaces are materialized lazily; maxSize is the hard ceiling on the
// number of live Surface objects, not merely a limit on acquisitions.
type SurfacePool struct {
	mu sync.Mutex

	maxSize int
	nextID  int

	surfaces map[*Surface]*surfaceState
	idle     []*Surface

	used  int
	limit int

	currentFormat string
	lastRequest   surfaceRequest
	trackRequests map[int]surfaceRequest
	trackUsed     map[int]int

	peakAllocated int
	peakUsed      int
}

// NewSurfacePool creates a pool that never holds more than maxSize surfaces.
func NewSurfacePool(maxSize int) *SurfacePool {
	if maxSize < 0 {
		maxSize = 0
	}
	return &SurfacePool{
		maxSize:       maxSize,
		limit:         maxSize,
		surfaces:      make(map[*Surface]*surfaceState),
		trackRequests: make(map[int]surfaceRequest),
		trackUsed:     make(map[int]int),
	}
}

// Acquire returns a surface of the requested resolution and format.
// It never waits for a surface: a full pool returns ErrPoolFull.
func (p *SurfacePool) Acquire(width, height int, format string) (*Surface, error) {
	if width <= 0 || height <= 0 {
		return nil, ErrInvalidDimensions
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.armLocked()

	if s := p.findIdleLocked(-1, width, height, format); s != nil {
		p.leaseLocked(s)
		p.currentFormat = format
		p.lastRequest = surfaceRequest{width: width, height: height, format: format}
		return s, nil
	}

	if len(p.surfaces) >= p.maxSize {
		if p.reclaimAllIdleLocked() == 0 {
			return nil, ErrPoolFull
		}
	}
	if len(p.surfaces) >= p.maxSize {
		return nil, ErrPoolFull
	}

	s := p.materializeLocked(width, height, format, -1, true)
	p.currentFormat = format
	p.lastRequest = surfaceRequest{width: width, height: height, format: format}
	return s, nil
}

// AcquireForTrack returns a surface reserved for one video track. Idle
// surfaces belonging to other tracks are never leased across the boundary.
func (p *SurfacePool) AcquireForTrack(track, width, height int) (*Surface, error) {
	if track < 0 {
		return nil, ErrInvalidTrack
	}
	if width <= 0 || height <= 0 {
		return nil, ErrInvalidDimensions
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.armLocked()
	format := p.currentFormat

	if s := p.findIdleLocked(track, width, height, format); s != nil {
		p.leaseLocked(s)
		p.trackRequests[track] = surfaceRequest{width: width, height: height, format: format}
		return s, nil
	}

	if len(p.surfaces) >= p.maxSize {
		if p.reclaimAllIdleLocked() == 0 {
			return nil, ErrPoolFull
		}
	}
	if len(p.surfaces) >= p.maxSize {
		return nil, ErrPoolFull
	}

	s := p.materializeLocked(width, height, format, track, true)
	p.trackRequests[track] = surfaceRequest{width: width, height: height, format: format}
	return s, nil
}

// Release returns a surface to the pool after its GPU work completed.
func (p *SurfacePool) Release(s *Surface) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.surfaces[s]
	if !ok {
		return ErrInvalidSurface
	}
	if !st.inUse {
		return ErrSurfaceNotInUse
	}
	if !st.gpuDone {
		return ErrGPUNotDone
	}
	if s.RefCount != 0 {
		return ErrRefsHeld
	}

	p.unleaseLocked(s, st)
	p.idle = append(p.idle, s)
	p.shrinkIdleLocked()
	return nil
}

// TryAcquire returns a surface if one is available without waiting.
func (p *SurfacePool) TryAcquire(width, height int) (*Surface, bool) {
	if width <= 0 || height <= 0 {
		return nil, false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.armLocked()
	format := p.currentFormat

	if s := p.findIdleLocked(-1, width, height, format); s != nil {
		p.leaseLocked(s)
		p.lastRequest = surfaceRequest{width: width, height: height, format: format}
		return s, true
	}

	if len(p.surfaces) >= p.maxSize {
		if p.reclaimAllIdleLocked() == 0 {
			return nil, false
		}
	}
	if len(p.surfaces) >= p.maxSize {
		return nil, false
	}

	s := p.materializeLocked(width, height, format, -1, true)
	p.lastRequest = surfaceRequest{width: width, height: height, format: format}
	return s, true
}

// Stats returns the number of surfaces in use and available capacity.
// Free capacity includes recycled idle surfaces and slots that can still be
// materialized without exceeding maxSize.
func (p *SurfacePool) Stats() (used, free int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	used = p.used
	if p.limit > used {
		free = p.limit - used
	}
	return used, free
}

// Total returns the number of surfaces currently accounted for by the pool.
func (p *SurfacePool) Total() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.limit > p.used {
		return p.limit
	}
	return p.used
}

// Peak returns the largest number of materialized surfaces observed since the
// pool was created or reset.
func (p *SurfacePool) Peak() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peakAllocated
}

// PeakUsed returns the largest number of concurrently leased surfaces.
func (p *SurfacePool) PeakUsed() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peakUsed
}

// Allocated returns the current number of materialized surfaces.
func (p *SurfacePool) Allocated() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.surfaces)
}

// Idle returns the current number of recycled, immediately reusable surfaces.
func (p *SurfacePool) Idle() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}

// Reclaim frees idle surfaces that no longer match the current stream and
// reports how many were freed.
func (p *SurfacePool) Reclaim() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	freed := 0
	idle := p.idle
	kept := idle[:0]
	for _, s := range idle {
		st := p.surfaces[s]
		if st == nil {
			continue
		}
		if p.matchesCurrentLocked(s, st) {
			kept = append(kept, s)
			continue
		}
		delete(p.surfaces, s)
		freed++
	}
	clear(idle[len(kept):])
	p.idle = kept

	if freed > 0 && p.limit > len(p.surfaces) {
		p.limit = len(p.surfaces)
	}
	return freed
}

// Grow allocates one additional surface up to the pool limit.
func (p *SurfacePool) Grow(width, height int) error {
	if width <= 0 || height <= 0 {
		return ErrInvalidDimensions
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.armLocked()
	if len(p.surfaces) >= p.maxSize {
		return ErrPoolFull
	}

	p.materializeLocked(width, height, p.currentFormat, -1, false)
	return nil
}

// Reset tears the pool down and rebuilds it empty.
func (p *SurfacePool) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.surfaces = make(map[*Surface]*surfaceState)
	p.idle = nil
	p.used = 0
	p.limit = 0
	p.currentFormat = ""
	p.lastRequest = surfaceRequest{}
	p.trackRequests = make(map[int]surfaceRequest)
	p.trackUsed = make(map[int]int)
	p.peakAllocated = 0
	p.peakUsed = 0
	return nil
}

// UsedByTrack returns how many surfaces a track currently holds.
func (p *SurfacePool) UsedByTrack(track int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.trackUsed[track]
}

// FormatChanged invalidates surfaces allocated with an older pixel format.
func (p *SurfacePool) FormatChanged(format string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldFormat := p.currentFormat
	p.currentFormat = format
	p.lastRequest.format = format
	for track, req := range p.trackRequests {
		req.format = format
		p.trackRequests[track] = req
	}

	idle := p.idle
	kept := idle[:0]
	for _, s := range idle {
		if s.Format == format {
			kept = append(kept, s)
			continue
		}
		delete(p.surfaces, s)
	}
	clear(idle[len(kept):])
	p.idle = kept

	if format != oldFormat {
		p.limit = 0
	}
	return nil
}

// AddRef increments the reference count of a surface.
func (p *SurfacePool) AddRef(s *Surface) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.surfaces[s]
	if !ok {
		return ErrInvalidSurface
	}
	if !st.inUse {
		return ErrSurfaceNotInUse
	}
	s.RefCount++
	return nil
}

// GPUDone marks the GPU work on a surface as complete.
func (p *SurfacePool) GPUDone(s *Surface) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.surfaces[s]
	if !ok {
		return ErrInvalidSurface
	}
	if !st.inUse {
		return ErrSurfaceNotInUse
	}
	if st.gpuDone {
		return ErrGPUAlreadyDone
	}

	st.gpuDone = true
	st.frameOwned = false
	st.frameOwner = 0
	return nil
}

// FailDecode releases the surface held by a failed decode.
func (p *SurfacePool) FailDecode(s *Surface) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.surfaces[s]
	if !ok {
		return ErrInvalidSurface
	}
	if !st.inUse {
		return ErrSurfaceNotInUse
	}

	s.RefCount = 0
	p.unleaseLocked(s, st)
	p.idle = append(p.idle, s)
	p.shrinkIdleLocked()
	return nil
}

// AcquireFrame makes decoder the owner of a frame stored in s.
func (p *SurfacePool) AcquireFrame(s *Surface, decoder int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.surfaces[s]
	if !ok {
		return ErrInvalidSurface
	}
	if !st.inUse {
		return ErrSurfaceNotInUse
	}
	if st.frameOwned && st.frameOwner != decoder {
		return ErrFrameBusy
	}

	st.frameOwned = true
	st.frameOwner = decoder
	return nil
}

func (p *SurfacePool) armLocked() {
	if p.limit == 0 && p.maxSize > 0 {
		p.limit = p.maxSize
	}
}

func (p *SurfacePool) materializeLocked(width, height int, format string, track int, inUse bool) *Surface {
	p.nextID++
	s := &Surface{
		ID:     p.nextID,
		Width:  width,
		Height: height,
		Format: format,
	}
	st := &surfaceState{
		track:   track,
		gpuDone: !inUse,
	}
	p.surfaces[s] = st

	if len(p.surfaces) > p.peakAllocated {
		p.peakAllocated = len(p.surfaces)
	}
	if p.limit > 0 && p.limit < len(p.surfaces) {
		p.limit = len(p.surfaces)
	}

	if inUse {
		p.leaseLocked(s)
	} else {
		p.idle = append(p.idle, s)
	}
	return s
}

func (p *SurfacePool) leaseLocked(s *Surface) {
	st := p.surfaces[s]
	st.inUse = true
	st.gpuDone = false
	st.frameOwned = false
	st.frameOwner = 0
	s.RefCount = 0

	p.used++
	if st.track >= 0 {
		p.trackUsed[st.track]++
	}
	if p.used > p.peakUsed {
		p.peakUsed = p.used
	}
	if p.limit > 0 && p.limit < p.used {
		p.limit = p.used
	}
}

func (p *SurfacePool) unleaseLocked(s *Surface, st *surfaceState) {
	st.inUse = false
	st.gpuDone = true
	st.frameOwned = false
	st.frameOwner = 0
	s.RefCount = 0

	p.used--
	if p.used < 0 {
		p.used = 0
	}
	if st.track >= 0 {
		if p.trackUsed[st.track] > 0 {
			p.trackUsed[st.track]--
		}
		if p.trackUsed[st.track] == 0 {
			delete(p.trackUsed, st.track)
		}
	}
}

func (p *SurfacePool) findIdleLocked(track, width, height int, format string) *Surface {
	for i, s := range p.idle {
		st := p.surfaces[s]
		if st == nil || st.inUse || st.track != track {
			continue
		}
		if s.Width != width || s.Height != height || s.Format != format {
			continue
		}
		copy(p.idle[i:], p.idle[i+1:])
		p.idle[len(p.idle)-1] = nil
		p.idle = p.idle[:len(p.idle)-1]
		return s
	}
	return nil
}

func (p *SurfacePool) reclaimAllIdleLocked() int {
	freed := len(p.idle)
	for _, s := range p.idle {
		delete(p.surfaces, s)
	}
	p.idle = nil
	return freed
}

func (p *SurfacePool) shrinkIdleLocked() {
	keep := idleKeep
	if keep > p.maxSize {
		keep = p.maxSize
	}
	if keep < 0 {
		keep = 0
	}

	closed := false
	for len(p.idle) > keep {
		victim := p.idle[0]
		p.idle[0] = nil
		p.idle = p.idle[1:]
		delete(p.surfaces, victim)
		closed = true
	}
	if closed && p.limit > len(p.surfaces) {
		p.limit = len(p.surfaces)
	}
}

func (p *SurfacePool) matchesCurrentLocked(s *Surface, st *surfaceState) bool {
	req := p.lastRequest
	if st.track >= 0 {
		if trackReq, ok := p.trackRequests[st.track]; ok {
			req = trackReq
		} else {
			req = surfaceRequest{format: p.currentFormat}
		}
	}
	if req.format == "" {
		req.format = p.currentFormat
	}
	if req.width > 0 && (s.Width != req.width || s.Height != req.height) {
		return false
	}
	if req.format != "" && s.Format != req.format {
		return false
	}
	return true
}
