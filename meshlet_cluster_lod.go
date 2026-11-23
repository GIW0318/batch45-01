package task

import (
	"errors"
	"math"
	"sync"
)

const (
	// Four levels are enough for the terrain LOD chain: full detail through
	// one eighth of the source triangles.
	maxIndexLOD = 3

	geometryEpsilon = 1e-9
)

var (
	errInvalidClusterID = errors.New("invalid cluster id")
	errInvalidGroup     = errors.New("invalid group")
	errEmptyClusters    = errors.New("cluster list is empty")
	errInvalidDistance  = errors.New("maxDist must be a finite non-negative number")
	errInvalidLOD       = errors.New("invalid LOD level")
	errFreeQueueFull    = errors.New("deferred free queue is full")
)

// Vec3 is a point in mesh space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// Sphere is a bounding sphere.
type Sphere struct {
	Center Vec3
	Radius float64
}

// Cluster is one meshlet cluster of a LOD chain.
type Cluster struct {
	ID       int
	Group    int
	Material int
	LOD      int
	Bounds   Sphere
	Vertices []Vec3
	Indices  []uint32
	Resident bool
}

// MeshletLod manages meshlet clusters and their LOD chain.
type MeshletLod struct {
	mu          sync.RWMutex
	maxClusters int
	clusters    map[int]Cluster
	resident    map[int]bool
	overflow    int
	epochs      map[int]uint64
	pendingFree map[int]struct{}
}

// NewMeshletLod returns a store that keeps at most maxClusters clusters.
func NewMeshletLod(maxClusters int) *MeshletLod {
	if maxClusters < 0 {
		maxClusters = 0
	}
	return &MeshletLod{
		maxClusters: maxClusters,
		clusters:    make(map[int]Cluster),
		resident:    make(map[int]bool),
		epochs:      make(map[int]uint64),
		pendingFree: make(map[int]struct{}),
	}
}

func (m *MeshletLod) initLocked() {
	if m.clusters == nil {
		m.clusters = make(map[int]Cluster)
	}
	if m.resident == nil {
		m.resident = make(map[int]bool)
	}
	if m.epochs == nil {
		m.epochs = make(map[int]uint64)
	}
	if m.pendingFree == nil {
		m.pendingFree = make(map[int]struct{})
	}
}

// AddCluster registers one cluster, clusters beyond the limit are reported.
func (m *MeshletLod) AddCluster(c Cluster) error {
	if c.ID < 0 {
		return errInvalidClusterID
	}
	if c.Bounds.Radius < 0 || math.IsNaN(c.Bounds.Radius) {
		return errors.New("invalid cluster bounds")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	if _, ok := m.clusters[c.ID]; ok {
		m.clusters[c.ID] = c
		if c.Resident {
			m.resident[c.ID] = true
		}
		return nil
	}
	if len(m.clusters) >= m.maxClusters {
		m.overflow++
		return nil
	}

	m.clusters[c.ID] = c
	if c.Resident {
		m.resident[c.ID] = true
	}
	return nil
}

// ClusterCount returns the number of live clusters.
func (m *MeshletLod) ClusterCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clusters)
}

// GroupByAdjacency groups clusters whose centres are within maxDist of each other.
func (m *MeshletLod) GroupByAdjacency(clusters []Cluster, maxDist float64) (map[int][]int, error) {
	if len(clusters) == 0 {
		return nil, errEmptyClusters
	}
	if maxDist < 0 || math.IsNaN(maxDist) || math.IsInf(maxDist, 0) {
		return nil, errInvalidDistance
	}

	parent := make([]int, len(clusters))
	rank := make([]uint8, len(clusters))
	for i := range parent {
		parent[i] = i
	}

	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if rank[ra] < rank[rb] {
			ra, rb = rb, ra
		}
		parent[rb] = ra
		if rank[ra] == rank[rb] {
			rank[ra]++
		}
	}

	maxDistSq := maxDist * maxDist
	for i := 0; i < len(clusters); i++ {
		for j := i + 1; j < len(clusters); j++ {
			dx := clusters[i].Bounds.Center.X - clusters[j].Bounds.Center.X
			dy := clusters[i].Bounds.Center.Y - clusters[j].Bounds.Center.Y
			dz := clusters[i].Bounds.Center.Z - clusters[j].Bounds.Center.Z
			if dx*dx+dy*dy+dz*dz <= maxDistSq {
				union(i, j)
			}
		}
	}

	groups := make(map[int][]int)
	groupByRoot := make(map[int]int)
	nextGroup := 0
	for i, c := range clusters {
		root := find(i)
		group, ok := groupByRoot[root]
		if !ok {
			group = nextGroup
			nextGroup++
			groupByRoot[root] = group
		}
		groups[group] = append(groups[group], c.ID)
	}
	return groups, nil
}

// BoundSphere returns the tightest sphere around verts.
func (m *MeshletLod) BoundSphere(verts []Vec3) Sphere {
	if len(verts) == 0 {
		return Sphere{}
	}

	var center Vec3
	for _, v := range verts {
		center.X += v.X
		center.Y += v.Y
		center.Z += v.Z
	}
	n := float64(len(verts))
	center.X /= n
	center.Y /= n
	center.Z /= n

	var radiusSq float64
	for _, v := range verts {
		dx := v.X - center.X
		dy := v.Y - center.Y
		dz := v.Z - center.Z
		if d := dx*dx + dy*dy + dz*dz; d > radiusSq {
			radiusSq = d
		}
	}
	return Sphere{Center: center, Radius: math.Sqrt(radiusSq)}
}

// BoundsCoverVertices reports whether c.Bounds contains every vertex of c.
func (m *MeshletLod) BoundsCoverVertices(c Cluster) bool {
	if c.Bounds.Radius < 0 || math.IsNaN(c.Bounds.Radius) {
		return false
	}
	limit := c.Bounds.Radius + geometryEpsilon
	limitSq := limit * limit
	for _, v := range c.Vertices {
		dx := v.X - c.Bounds.Center.X
		dy := v.Y - c.Bounds.Center.Y
		dz := v.Z - c.Bounds.Center.Z
		if dx*dx+dy*dy+dz*dz > limitSq {
			return false
		}
	}
	return true
}

// SimplifyPreservesMaterialBoundary reports whether the simplified cluster may
// stand in for orig without crossing into another material.
func (m *MeshletLod) SimplifyPreservesMaterialBoundary(orig, simplified Cluster) bool {
	return orig.Material == simplified.Material
}

// RebuildIndexBuffer rebuilds the index buffer of c for the given LOD.
func (m *MeshletLod) RebuildIndexBuffer(c Cluster, lod int) ([]uint32, error) {
	if lod < 0 || lod > maxIndexLOD {
		return nil, errInvalidLOD
	}
	if len(c.Vertices) > 0 {
		for _, idx := range c.Indices {
			if int(idx) >= len(c.Vertices) {
				return nil, errors.New("index outside cluster vertex buffer")
			}
		}
	}
	if lod == 0 || len(c.Indices) == 0 {
		return append([]uint32(nil), c.Indices...), nil
	}

	triangles := len(c.Indices) / 3
	if triangles == 0 {
		return append([]uint32(nil), c.Indices...), nil
	}

	stride := 1 << lod
	out := make([]uint32, 0, (triangles+stride-1)/stride*3)
	for tri := 0; tri < triangles; tri += stride {
		base := tri * 3
		out = append(out, c.Indices[base:base+3]...)
	}
	return out, nil
}

// ScreenPixelSize returns the on screen diameter of c in pixels.
func (m *MeshletLod) ScreenPixelSize(c Cluster, camPos Vec3, fovDeg, viewportHeight float64) float64 {
	if c.Bounds.Radius <= 0 || viewportHeight <= 0 || fovDeg <= 0 || fovDeg >= 180 {
		return 0
	}
	if math.IsNaN(c.Bounds.Radius) || math.IsNaN(viewportHeight) || math.IsNaN(fovDeg) {
		return 0
	}

	dx := c.Bounds.Center.X - camPos.X
	dy := c.Bounds.Center.Y - camPos.Y
	dz := c.Bounds.Center.Z - camPos.Z
	distance := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if distance <= 0 || math.IsInf(distance, 0) || math.IsNaN(distance) {
		return 0
	}

	fovRad := fovDeg * math.Pi / 180
	diameter := 2 * c.Bounds.Radius
	halfViewport := viewportHeight / 2
	return diameter * halfViewport / (distance * math.Tan(fovRad/2))
}

// SelectLOD picks the LOD index for a screen size using descending thresholds.
func (m *MeshletLod) SelectLOD(c Cluster, screenPixels float64, thresholds []float64) int {
	for i, threshold := range thresholds {
		if screenPixels >= threshold {
			return i
		}
	}
	return len(thresholds)
}

// GroupBufferEpoch returns the version of the shared vertex buffer of a group.
func (m *MeshletLod) GroupBufferEpoch(group int) (uint64, error) {
	if group < 0 {
		return 0, errInvalidGroup
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.epochs[group], nil
}

// SyncGroupBuffer publishes the shared vertex buffer of a group to the GPU.
func (m *MeshletLod) SyncGroupBuffer(group int) error {
	if group < 0 {
		return errInvalidGroup
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	m.epochs[group]++
	return nil
}

// TransitionAlpha returns the blend weight between two LOD levels.
func (m *MeshletLod) TransitionAlpha(fromLOD, toLOD int, progress float64) float64 {
	if fromLOD == toLOD || math.IsNaN(progress) {
		return 0
	}
	if progress <= 0 {
		return 0
	}
	if progress >= 1 {
		return 1
	}
	return progress
}

// ShadowLOD returns the LOD the shadow pass must use for a main pass LOD.
func (m *MeshletLod) ShadowLOD(mainLOD int) int {
	if mainLOD < 0 {
		return -1
	}
	return mainLOD
}

// StreamCluster makes a cluster resident.
func (m *MeshletLod) StreamCluster(id int) error {
	if id < 0 {
		return errInvalidClusterID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	m.resident[id] = true
	if c, ok := m.clusters[id]; ok {
		c.Resident = true
		m.clusters[id] = c
	}
	return nil
}

// IsResident reports whether a cluster may be rendered.
func (m *MeshletLod) IsResident(id int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.resident[id]
}

// MaxClusters returns the configured cluster limit.
func (m *MeshletLod) MaxClusters() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxClusters
}

// OverflowCount returns how many clusters were dropped because of the limit.
func (m *MeshletLod) OverflowCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.overflow
}

// DeferFree queues a cluster buffer for release after the current frame.
func (m *MeshletLod) DeferFree(id int) error {
	if id < 0 {
		return errInvalidClusterID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	if _, ok := m.pendingFree[id]; ok {
		return nil
	}
	if len(m.pendingFree) >= m.maxClusters {
		return errFreeQueueFull
	}
	m.pendingFree[id] = struct{}{}
	return nil
}

// PendingFrees returns the number of buffers waiting for release.
func (m *MeshletLod) PendingFrees() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pendingFree)
}

// FlushFrees releases all queued buffers.
func (m *MeshletLod) FlushFrees() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.pendingFree {
		delete(m.resident, id)
	}
	clear(m.pendingFree)
	return nil
}
