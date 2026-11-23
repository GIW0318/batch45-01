package task

import "errors"

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrChunkNotLoaded = errors.New("chunk not loaded")
	ErrInvalidConfig  = errors.New("invalid terrain config")
	ErrInvalidLOD     = errors.New("invalid lod level")
)

// Vec3 is a three component vector.
type Vec3 struct {
	X, Y, Z float64
}

// ChunkCoord identifies one terrain chunk.
type ChunkCoord struct {
	X, Y int
}

// Config is the terrain lod configuration.
type Config struct {
	ChunkSize   int
	MaxLOD      int
	SkirtHeight float64
	Hysteresis  float64
	HeightScale float64
}

// TerrainLod stitches the LOD levels of neighbouring terrain chunks.
type TerrainLod struct{}

// NewTerrainLod builds a terrain lod manager.
func NewTerrainLod(cfg Config) (*TerrainLod, error) { return nil, ErrNotImplemented }

// LoadChunk loads a chunk and allocates its own vertex buffer.
func (t *TerrainLod) LoadChunk(c ChunkCoord) error { return ErrNotImplemented }

// UnloadChunk unloads a chunk and releases its GPU buffer.
func (t *TerrainLod) UnloadChunk(c ChunkCoord) error { return ErrNotImplemented }

// Loaded reports whether a chunk is loaded.
func (t *TerrainLod) Loaded(c ChunkCoord) bool { return false }

// SetCamera moves the camera and queues one coalesced lod update.
func (t *TerrainLod) SetCamera(pos Vec3) error { return ErrNotImplemented }

// Update applies the queued lod update.
func (t *TerrainLod) Update() error { return ErrNotImplemented }

// LODOf returns the lod level of a chunk.
func (t *TerrainLod) LODOf(c ChunkCoord) (int, error) { return 0, ErrNotImplemented }

// StitchedLOD returns the lod level aligned with the neighbouring chunks.
func (t *TerrainLod) StitchedLOD(c ChunkCoord) (int, error) { return 0, ErrNotImplemented }

// SkirtHeight returns the skirt height that hides gaps at the chunk edges.
func (t *TerrainLod) SkirtHeight(c ChunkCoord) (float64, error) { return 0, ErrNotImplemented }

// HeightAt samples the height field, clamping out of range coordinates.
func (t *TerrainLod) HeightAt(x, z float64) float64 { return 0 }

// Vertices returns the vertex count of a chunk at its current lod.
func (t *TerrainLod) Vertices(c ChunkCoord) (int, error) { return 0, ErrNotImplemented }

// IndexOffset returns the base vertex index of a chunk inside the shared buffer.
func (t *TerrainLod) IndexOffset(c ChunkCoord) (int, error) { return 0, ErrNotImplemented }

// UVAt maps chunk local coordinates to a continuous world texture UV.
func (t *TerrainLod) UVAt(c ChunkCoord, localX, localY float64) (float64, float64, error) {
	return 0, 0, ErrNotImplemented
}

// NormalAt returns the interpolated terrain normal at a chunk local position.
func (t *TerrainLod) NormalAt(c ChunkCoord, localX, localY float64) (Vec3, error) {
	return Vec3{}, ErrNotImplemented
}

// ShadowLOD returns the lod level used for shadow rendering.
func (t *TerrainLod) ShadowLOD(c ChunkCoord) (int, error) { return 0, ErrNotImplemented }

// GPUBuffers reports the number of GPU buffers owned by a chunk.
func (t *TerrainLod) GPUBuffers(c ChunkCoord) (int, error) { return 0, ErrNotImplemented }

// PendingUpdates reports how many lod updates are queued.
func (t *TerrainLod) PendingUpdates() int { return 0 }

// ScreenError returns the screen space error of a chunk.
func (t *TerrainLod) ScreenError(c ChunkCoord) (float64, error) { return 0, ErrNotImplemented }

// ForceLOD overrides the lod level of a chunk (debug and stitching aid).
func (t *TerrainLod) ForceLOD(c ChunkCoord, lod int) error { return ErrNotImplemented }

// Flush applies every queued update.
func (t *TerrainLod) Flush() error { return ErrNotImplemented }
