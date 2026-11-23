package task

import (
	"errors"
	"math"
	"sync"
)

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrChunkNotLoaded = errors.New("chunk not loaded")
	ErrInvalidConfig  = errors.New("invalid terrain config")
	ErrInvalidLOD     = errors.New("invalid lod level")
)

const (
	// projectionScale turns a chunk's world-space error into a
	// distance-attenuated screen-space error.  It only has to be stable across
	// frames so that the hysteresis bands have a consistent meaning.
	projectionScale = 8.0
	// minDistance avoids a division by zero when the camera is exactly on a
	// chunk's bounding rectangle.
	minDistance = 1e-9
	// A fixed mesh budget keeps a fast camera from enqueuing an unbounded
	// amount of work in one frame.  Left-over work is drained by subsequent
	// Update calls (or by Flush).
	maxMeshRebuildsPerFrame = 64
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

type terrainVertex struct {
	X, Y, Z    float64
	NX, NY, NZ float64
	U, V       float64
}

// gpuMesh is the CPU-side mirror of one chunk's private GPU buffer.  Every
// build creates fresh slices; a chunk never writes into another chunk's
// buffer, even when several Update calls overlap.
type gpuMesh struct {
	lod      int
	vertices []terrainVertex
	indices  []uint32
}

type chunkState struct {
	coord      ChunkCoord
	lod        int
	forced     bool
	mesh       *gpuMesh
	gpuBuffers int
}

type rebuildTask struct {
	coord ChunkCoord
	lod   int
	gen   uint64
}

// TerrainLod stitches the LOD levels of neighbouring terrain chunks.
type TerrainLod struct {
	cfg Config

	mu        sync.Mutex
	updateMu  sync.Mutex
	chunks    map[ChunkCoord]*chunkState
	loadOrder []ChunkCoord
	camera    Vec3

	// generation changes whenever the camera moves.  A task carrying an old
	// generation is stale and is discarded instead of rebuilding a mesh for a
	// camera position that no longer exists.
	generation uint64

	pending        bool
	pendingUpdates int
	tasks          map[ChunkCoord]rebuildTask
	taskOrder      []ChunkCoord
}

// NewTerrainLod builds a terrain lod manager.
func NewTerrainLod(cfg Config) (*TerrainLod, error) {
	if cfg.ChunkSize <= 0 || cfg.MaxLOD < 0 || cfg.MaxLOD > 30 {
		return nil, ErrInvalidConfig
	}
	if !isFinite(cfg.SkirtHeight) || cfg.SkirtHeight < 0 ||
		!isFinite(cfg.Hysteresis) || cfg.Hysteresis < 0 ||
		!isFinite(cfg.HeightScale) {
		return nil, ErrInvalidConfig
	}

	return &TerrainLod{
		cfg:       cfg,
		chunks:    make(map[ChunkCoord]*chunkState),
		tasks:     make(map[ChunkCoord]rebuildTask),
		camera:    Vec3{},
		taskOrder: make([]ChunkCoord, 0),
	}, nil
}

// LoadChunk loads a chunk and allocates its own vertex buffer.
func (t *TerrainLod) LoadChunk(c ChunkCoord) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.chunks[c]; ok {
		return nil
	}

	// A chunk loaded after the camera has already moved starts at the level
	// selected from the current camera rather than flashing at level zero.
	ch := &chunkState{
		coord:      c,
		lod:        0,
		gpuBuffers: 1,
	}
	ch.lod = t.selectLODLocked(ch, t.camera)

	// Build into a fresh slice.  This is intentionally not a shared scratch
	// buffer: concurrent loads/updates can never alias mutable vertex data.
	ch.mesh = t.buildMesh(c, ch.lod)
	t.chunks[c] = ch
	t.loadOrder = append(t.loadOrder, c)
	return nil
}

// UnloadChunk unloads a chunk and releases its GPU buffer.
func (t *TerrainLod) UnloadChunk(c ChunkCoord) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.chunks[c]; !ok {
		return ErrChunkNotLoaded
	}

	delete(t.chunks, c)
	delete(t.tasks, c)
	for i, loaded := range t.loadOrder {
		if loaded == c {
			t.loadOrder = append(t.loadOrder[:i], t.loadOrder[i+1:]...)
			break
		}
	}
	// The chunk and its buffer are gone.  Tasks referring to it are dropped
	// by the next task-order compaction as well.
	return nil
}

// Loaded reports whether a chunk is loaded.
func (t *TerrainLod) Loaded(c ChunkCoord) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.chunks[c]
	return ok
}

// SetCamera moves the camera and queues one coalesced lod update.
func (t *TerrainLod) SetCamera(pos Vec3) error {
	if !isFinite(pos.X) || !isFinite(pos.Y) || !isFinite(pos.Z) {
		return ErrInvalidConfig
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.camera = pos
	// Invalidate all queued work.  The next Update recomputes the desired
	// levels from the latest camera position, so calling SetCamera repeatedly
	// only leaves one logical update queued.
	t.generation++
	t.tasks = make(map[ChunkCoord]rebuildTask)
	t.taskOrder = t.taskOrder[:0]
	t.pending = true
	t.pendingUpdates = 1
	return nil
}

// Update applies the queued lod update.
func (t *TerrainLod) Update() error {
	t.updateMu.Lock()
	defer t.updateMu.Unlock()
	t.updateOnce(maxMeshRebuildsPerFrame)
	return nil
}

// LODOf returns the lod level of a chunk.
func (t *TerrainLod) LODOf(c ChunkCoord) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.chunks[c]
	if !ok {
		return 0, ErrChunkNotLoaded
	}
	return ch.lod, nil
}

// StitchedLOD returns the lod level aligned with the neighbouring chunks.
func (t *TerrainLod) StitchedLOD(c ChunkCoord) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.chunks[c]
	if !ok {
		return 0, ErrChunkNotLoaded
	}

	// Unloaded neighbours behave exactly like self, which means they never
	// make the result coarser than the chunk's own selected level.
	stitched := ch.lod
	for _, n := range neighbours(c) {
		if nch, ok := t.chunks[n]; ok && nch.lod > stitched {
			stitched = nch.lod
		}
	}
	return stitched, nil
}

// SkirtHeight returns the skirt height that hides gaps at the chunk edges.
func (t *TerrainLod) SkirtHeight(c ChunkCoord) (float64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.chunks[c]
	if !ok {
		return 0, ErrChunkNotLoaded
	}
	// Each successive level doubles the skirt.  A coarse level is therefore
	// never less than twice a directly finer level, and the skirt grows with
	// the geometric error instead of being a fixed world-space constant.
	return t.cfg.SkirtHeight * float64(uint64(1)<<uint(ch.lod)), nil
}

// HeightAt samples the height field, clamping out of range coordinates.
func (t *TerrainLod) HeightAt(x, z float64) float64 {
	return t.heightAt(x, z)
}

// Vertices returns the vertex count of a chunk at its current lod.
func (t *TerrainLod) Vertices(c ChunkCoord) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.chunks[c]
	if !ok {
		return 0, ErrChunkNotLoaded
	}
	return t.vertexCount(ch.lod), nil
}

// IndexOffset returns the base vertex index of a chunk inside the shared buffer.
func (t *TerrainLod) IndexOffset(c ChunkCoord) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.chunks[c]; !ok {
		return 0, ErrChunkNotLoaded
	}

	offset := 0
	for _, loaded := range t.loadOrder {
		if loaded == c {
			return offset, nil
		}
		if ch, ok := t.chunks[loaded]; ok {
			offset += t.vertexCount(ch.lod)
		}
	}
	return 0, ErrChunkNotLoaded
}

// UVAt maps chunk local coordinates to a continuous world texture UV.
func (t *TerrainLod) UVAt(c ChunkCoord, localX, localY float64) (float64, float64, error) {
	t.mu.Lock()
	_, ok := t.chunks[c]
	t.mu.Unlock()
	if !ok {
		return 0, 0, ErrChunkNotLoaded
	}

	size := float64(t.cfg.ChunkSize)
	// World-space mapping.  At a shared border the left chunk's localX=size
	// and the right chunk's localX=0 evaluate to the same world coordinate,
	// so no LOD-dependent UV seam is introduced.
	u := (float64(c.X)*size + localX) / size
	v := (float64(c.Y)*size + localY) / size
	return u, v, nil
}

// NormalAt returns the interpolated terrain normal at a chunk local position.
func (t *TerrainLod) NormalAt(c ChunkCoord, localX, localY float64) (Vec3, error) {
	t.mu.Lock()
	_, ok := t.chunks[c]
	t.mu.Unlock()
	if !ok {
		return Vec3{}, ErrChunkNotLoaded
	}

	size := float64(t.cfg.ChunkSize)
	x := float64(c.X)*size + localX
	z := float64(c.Y)*size + localY
	return t.normalAtWorld(x, z), nil
}

// ShadowLOD returns the lod level used for shadow rendering.
func (t *TerrainLod) ShadowLOD(c ChunkCoord) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.chunks[c]
	if !ok {
		return 0, ErrChunkNotLoaded
	}
	// Shadows always use the same level as the main pass.  Keeping this as a
	// method (rather than a cached copy) makes the two impossible to drift
	// apart after a LOD switch.
	return ch.lod, nil
}

// GPUBuffers reports the number of GPU buffers owned by a chunk.
func (t *TerrainLod) GPUBuffers(c ChunkCoord) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.chunks[c]
	if !ok {
		return 0, ErrChunkNotLoaded
	}
	return ch.gpuBuffers, nil
}

// PendingUpdates reports how many lod updates are queued.
func (t *TerrainLod) PendingUpdates() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.pending {
		// All consecutive SetCamera calls collapse into one logical update.
		return 1
	}
	return len(t.tasks)
}

// ScreenError returns the screen space error of a chunk.
func (t *TerrainLod) ScreenError(c ChunkCoord) (float64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.chunks[c]; !ok {
		return 0, ErrChunkNotLoaded
	}
	return t.screenErrorLocked(c), nil
}

// ForceLOD overrides the lod level of a chunk (debug and stitching aid).
func (t *TerrainLod) ForceLOD(c ChunkCoord, lod int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.chunks[c]
	if !ok {
		return ErrChunkNotLoaded
	}
	if lod < 0 || lod > t.cfg.MaxLOD {
		return ErrInvalidLOD
	}

	ch.forced = true
	ch.lod = lod
	if ch.mesh == nil || ch.mesh.lod != lod {
		t.enqueueLocked(c, lod, t.generation)
	}
	return nil
}

// Flush applies every queued update.
func (t *TerrainLod) Flush() error {
	t.updateMu.Lock()
	defer t.updateMu.Unlock()

	// One uncapped update drains the normal case.  The small loop handles a
	// camera move that races with a build without ever spinning forever.
	for i := 0; i < 16; i++ {
		t.updateOnce(int(^uint(0) >> 1))

		t.mu.Lock()
		busy := t.pending || len(t.tasks) > 0
		t.mu.Unlock()
		if !busy {
			break
		}
	}
	return nil
}

func (t *TerrainLod) updateOnce(limit int) {
	t.mu.Lock()
	gen := t.generation

	if t.pending {
		cam := t.camera
		for _, c := range t.loadOrder {
			ch := t.chunks[c]
			if ch == nil || ch.forced {
				continue
			}
			ch.lod = t.selectLODLocked(ch, cam)
		}
		t.pending = false
		t.pendingUpdates = 0
	}

	// Remove tasks that reference unloaded chunks, an obsolete camera
	// generation, or a level that is no longer current.
	for c, task := range t.tasks {
		ch, ok := t.chunks[c]
		if !ok || task.gen != gen || task.lod != ch.lod ||
			(ch.mesh != nil && ch.mesh.lod == ch.lod) {
			delete(t.tasks, c)
		}
	}
	t.compactTaskOrderLocked()

	// Any chunk whose mesh does not match its selected level needs work.
	for _, c := range t.loadOrder {
		ch := t.chunks[c]
		if ch == nil {
			continue
		}
		if ch.mesh == nil || ch.mesh.lod != ch.lod {
			t.enqueueLocked(c, ch.lod, gen)
		}
	}

	capHint := len(t.taskOrder)
	if limit < capHint {
		capHint = limit
	}
	batch := make([]rebuildTask, 0, capHint)
	for len(batch) < limit && len(t.taskOrder) > 0 {
		c := t.taskOrder[0]
		t.taskOrder = t.taskOrder[1:]
		task, ok := t.tasks[c]
		if !ok {
			continue
		}
		delete(t.tasks, c)
		batch = append(batch, task)
	}
	t.mu.Unlock()

	for _, task := range batch {
		// Check for cancellation before doing the expensive work.
		t.mu.Lock()
		ch := t.chunks[task.coord]
		stale := ch == nil || task.gen != t.generation || ch.lod != task.lod
		t.mu.Unlock()
		if stale {
			continue
		}

		// Build into task-local storage.  No chunk's mutable vertex slice is
		// touched until the completed mesh is attached below.
		mesh := t.buildMesh(task.coord, task.lod)

		t.mu.Lock()
		ch = t.chunks[task.coord]
		if ch != nil && task.gen == t.generation && ch.lod == task.lod {
			ch.mesh = mesh
		}
		t.mu.Unlock()
	}
}

func (t *TerrainLod) enqueueLocked(c ChunkCoord, lod int, gen uint64) {
	if old, ok := t.tasks[c]; ok {
		// Keep the existing queue position; replace only its payload.
		if old.gen == gen && old.lod == lod {
			return
		}
		t.tasks[c] = rebuildTask{coord: c, lod: lod, gen: gen}
		return
	}
	t.tasks[c] = rebuildTask{coord: c, lod: lod, gen: gen}
	t.taskOrder = append(t.taskOrder, c)
}

func (t *TerrainLod) compactTaskOrderLocked() {
	seen := make(map[ChunkCoord]struct{}, len(t.tasks))
	order := t.taskOrder[:0]
	for _, c := range t.taskOrder {
		if _, ok := t.tasks[c]; !ok {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		order = append(order, c)
	}
	// Tasks are normally added through enqueueLocked and are already in the
	// order.  The load-order pass also makes the queue deterministic if a
	// future caller inserts a task directly.
	for _, c := range t.loadOrder {
		if _, ok := t.tasks[c]; !ok {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		order = append(order, c)
	}
	t.taskOrder = order
}

func (t *TerrainLod) selectLODLocked(ch *chunkState, cam Vec3) int {
	se := t.screenErrorForLocked(ch.coord, cam)
	if se <= 0 {
		// A completely flat height field has no simplification error.  Keep
		// the current level rather than producing an unbounded desired level.
		return clampInt(ch.lod, 0, t.cfg.MaxLOD)
	}

	desired := float64(t.cfg.MaxLOD) - math.Log2(se)
	if math.IsNaN(desired) {
		return ch.lod
	}

	var target int
	switch {
	case desired <= 0:
		target = 0
	case desired >= float64(t.cfg.MaxLOD):
		target = t.cfg.MaxLOD
	default:
		target = int(math.Ceil(desired))
	}

	h := t.cfg.Hysteresis
	current := ch.lod
	if target > current {
		// Going coarser: require passing the next integer boundary by the
		// hysteresis band.  Small camera jitter cannot cross it.
		if desired > float64(current)+h {
			return target
		}
		return current
	}
	if target < current {
		// Going finer: use the symmetric boundary below the current level.
		if desired < float64(current-1)-h {
			return target
		}
		return current
	}
	return current
}

func (t *TerrainLod) screenErrorLocked(c ChunkCoord) float64 {
	return t.screenErrorForLocked(c, t.camera)
}

func (t *TerrainLod) screenErrorForLocked(c ChunkCoord, cam Vec3) float64 {
	d := t.chunkDistance(c, cam)
	base := math.Abs(t.cfg.HeightScale) * float64(t.cfg.ChunkSize) * projectionScale
	if base == 0 {
		return 0
	}
	return base / math.Max(d, minDistance)
}

func (t *TerrainLod) chunkDistance(c ChunkCoord, cam Vec3) float64 {
	size := float64(t.cfg.ChunkSize)
	minX := float64(c.X) * size
	maxX := minX + size
	minZ := float64(c.Y) * size
	maxZ := minZ + size

	dx := 0.0
	if cam.X < minX {
		dx = minX - cam.X
	} else if cam.X > maxX {
		dx = cam.X - maxX
	}
	dz := 0.0
	if cam.Z < minZ {
		dz = minZ - cam.Z
	} else if cam.Z > maxZ {
		dz = cam.Z - maxZ
	}
	return math.Hypot(dx, dz)
}

func (t *TerrainLod) vertexCount(lod int) int {
	cells := t.gridCells(lod)
	side := cells + 1
	return side * side
}

func (t *TerrainLod) gridCells(lod int) int {
	step := 1 << uint(lod)
	cells := t.cfg.ChunkSize / step
	if cells < 1 {
		cells = 1
	}
	return cells
}

func (t *TerrainLod) buildMesh(c ChunkCoord, lod int) *gpuMesh {
	cells := t.gridCells(lod)
	side := cells + 1
	size := float64(t.cfg.ChunkSize)
	originX := float64(c.X) * size
	originZ := float64(c.Y) * size

	vertices := make([]terrainVertex, 0, side*side)
	for gy := 0; gy < side; gy++ {
		localZ := float64(gy) * size / float64(cells)
		worldZ := originZ + localZ
		for gx := 0; gx < side; gx++ {
			localX := float64(gx) * size / float64(cells)
			worldX := originX + localX
			n := t.normalAtWorld(worldX, worldZ)
			vertices = append(vertices, terrainVertex{
				X:  worldX,
				Y:  t.heightAt(worldX, worldZ),
				Z:  worldZ,
				NX: n.X,
				NY: n.Y,
				NZ: n.Z,
				U:  worldX / size,
				V:  worldZ / size,
			})
		}
	}

	indices := make([]uint32, 0, cells*cells*6)
	for gy := 0; gy < cells; gy++ {
		for gx := 0; gx < cells; gx++ {
			i0 := uint32(gy*side + gx)
			i1 := i0 + 1
			i2 := i0 + uint32(side)
			i3 := i2 + 1
			indices = append(indices, i0, i2, i1, i1, i2, i3)
		}
	}

	return &gpuMesh{lod: lod, vertices: vertices, indices: indices}
}

func (t *TerrainLod) heightAt(x, z float64) float64 {
	size := float64(t.cfg.ChunkSize)
	x = clampFinite(x, 0, size)
	z = clampFinite(z, 0, size)

	u := x / size
	v := z / size
	h := 0.5 +
		0.25*math.Sin(2*math.Pi*u) +
		0.25*math.Cos(2*math.Pi*v) +
		0.125*math.Sin(4*math.Pi*u+0.7)*math.Cos(2*math.Pi*v-0.3)
	h *= t.cfg.HeightScale
	if math.IsNaN(h) || math.IsInf(h, 0) {
		return 0
	}
	return h
}

func (t *TerrainLod) normalAtWorld(x, z float64) Vec3 {
	eps := float64(t.cfg.ChunkSize) * 1e-4
	if eps < 1e-6 {
		eps = 1e-6
	}

	dhdx := (t.heightAt(x+eps, z) - t.heightAt(x-eps, z)) / (2 * eps)
	dhdz := (t.heightAt(x, z+eps) - t.heightAt(x, z-eps)) / (2 * eps)
	nx, ny, nz := -dhdx, 1.0, -dhdz
	length := math.Sqrt(nx*nx + ny*ny + nz*nz)
	if length == 0 || math.IsNaN(length) || math.IsInf(length, 0) {
		return Vec3{Y: 1}
	}
	return Vec3{X: nx / length, Y: ny / length, Z: nz / length}
}

func neighbours(c ChunkCoord) [4]ChunkCoord {
	return [4]ChunkCoord{
		{X: c.X - 1, Y: c.Y},
		{X: c.X + 1, Y: c.Y},
		{X: c.X, Y: c.Y - 1},
		{X: c.X, Y: c.Y + 1},
	}
}

func clampFinite(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
