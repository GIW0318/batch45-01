package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Vec3 is a position or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// BlendMode is the blend state of a particle material.
type BlendMode int

const (
	// BlendAlpha is classic alpha blending.
	BlendAlpha BlendMode = iota
	// BlendAdditive is additive blending.
	BlendAdditive
)

// Particle is one live particle of the simulation.
type Particle struct {
	ID     int
	Pos    Vec3
	Radius float64
	Blend  BlendMode
	Alive  bool
}

// ParticleSorter sorts and batches particles for the transparent pass.
type ParticleSorter struct {
	capacity int
}

// NewParticleSorter returns a sorter with a fixed particle capacity.
func NewParticleSorter(capacity int) *ParticleSorter {
	return &ParticleSorter{}
}

// Capacity returns the number of particle slots.
func (s *ParticleSorter) Capacity() int {
	return 0
}

// SetCamera records the camera used for the following sorts.
func (s *ParticleSorter) SetCamera(cam Vec3) error {
	return errNotImplemented
}

// CameraGeneration returns how many times the camera was moved.
func (s *ParticleSorter) CameraGeneration() uint64 {
	return 0
}

// SortBackToFront orders particles from the farthest to the nearest.
func (s *ParticleSorter) SortBackToFront(parts []Particle) ([]Particle, error) {
	return nil, errNotImplemented
}

// SortKey returns the depth key of a particle, measured at its farthest extent.
func (s *ParticleSorter) SortKey(p Particle) float64 {
	return 0
}

// SortWithCameraStamp sorts with the camera of the given generation and
// rejects a stamp that no longer matches the current camera.
func (s *ParticleSorter) SortWithCameraStamp(parts []Particle, stamp uint64) ([]Particle, error) {
	return nil, errNotImplemented
}

// SortStableTies keeps the emission order of particles with equal depth.
func (s *ParticleSorter) SortStableTies(parts []Particle) ([]Particle, error) {
	return nil, errNotImplemented
}

// BatchByBlend splits particles into per blend state batches.
func (s *ParticleSorter) BatchByBlend(parts []Particle) ([][]Particle, error) {
	return nil, errNotImplemented
}

// SampleSoftDepth returns the average depth around texel (x, y) for soft particles.
func (s *ParticleSorter) SampleSoftDepth(depth []float32, w, h, x, y int) (float32, error) {
	return 0, errNotImplemented
}

// AcquireSlot returns a free particle slot.
func (s *ParticleSorter) AcquireSlot() (int, error) {
	return 0, errNotImplemented
}

// ReleaseSlot frees a particle slot for reuse.
func (s *ParticleSorter) ReleaseSlot(id int) error {
	return errNotImplemented
}

// ActiveSlots returns the number of slots currently in use.
func (s *ParticleSorter) ActiveSlots() int {
	return 0
}

// EmitBatch emits a batch of particles for this frame.
func (s *ParticleSorter) EmitBatch(parts []Particle) error {
	return errNotImplemented
}

// BeginFrame clears the per frame particle buffer.
func (s *ParticleSorter) BeginFrame() error {
	return errNotImplemented
}

// BlendOrderIndex returns the draw order rank of a blend mode.
func (s *ParticleSorter) BlendOrderIndex(m BlendMode) int {
	return -1
}

// OverflowOverwritesActive reports whether a full buffer recycles live particles.
func (s *ParticleSorter) OverflowOverwritesActive() bool {
	return true
}
