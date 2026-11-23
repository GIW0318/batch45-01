package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Surface is one GPU decode surface owned by the pool.
type Surface struct {
	ID       int
	Width    int
	Height   int
	Format   string
	RefCount int
}

// SurfacePool allocates and recycles hardware decode surfaces.
type SurfacePool struct{}

// NewSurfacePool creates a pool that never holds more than maxSize surfaces.
func NewSurfacePool(maxSize int) *SurfacePool {
	return &SurfacePool{}
}

// Acquire returns a surface of the requested resolution and format.
func (p *SurfacePool) Acquire(width, height int, format string) (*Surface, error) {
	return nil, errNotImplemented
}

// AcquireForTrack returns a surface reserved for one video track.
func (p *SurfacePool) AcquireForTrack(track, width, height int) (*Surface, error) {
	return nil, errNotImplemented
}

// Release returns a surface to the pool after its GPU work completed.
func (p *SurfacePool) Release(s *Surface) error {
	return errNotImplemented
}

// TryAcquire returns a surface if one is available without blocking.
func (p *SurfacePool) TryAcquire(width, height int) (*Surface, bool) {
	return nil, false
}

// Stats returns the number of surfaces in use and idle.
func (p *SurfacePool) Stats() (used, free int) {
	return 0, 0
}

// Total returns the number of surfaces owned by the pool.
func (p *SurfacePool) Total() int {
	return 0
}

// Reclaim frees idle surfaces that no longer match the current stream and
// reports how many were freed.
func (p *SurfacePool) Reclaim() int {
	return 0
}

// Grow allocates one additional surface up to the pool limit.
func (p *SurfacePool) Grow(width, height int) error {
	return errNotImplemented
}

// Reset tears the pool down and rebuilds it empty.
func (p *SurfacePool) Reset() error {
	return errNotImplemented
}

// UsedByTrack returns how many surfaces a track holds.
func (p *SurfacePool) UsedByTrack(track int) int {
	return 0
}

// FormatChanged invalidates surfaces allocated with an older pixel format.
func (p *SurfacePool) FormatChanged(format string) error {
	return errNotImplemented
}

// AddRef increments the reference count of a surface.
func (p *SurfacePool) AddRef(s *Surface) error {
	return errNotImplemented
}

// GPUDone marks the GPU work on a surface as complete.
func (p *SurfacePool) GPUDone(s *Surface) error {
	return errNotImplemented
}

// FailDecode releases the surface held by a failed decode.
func (p *SurfacePool) FailDecode(s *Surface) error {
	return errNotImplemented
}

// AcquireFrame makes decoder the owner of a frame stored in s.
func (p *SurfacePool) AcquireFrame(s *Surface, decoder int) error {
	return errNotImplemented
}
