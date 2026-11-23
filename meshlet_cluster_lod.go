package task

import "errors"

var errNotImplemented = errors.New("not implemented")

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
	maxClusters int
}

// NewMeshletLod returns a store that keeps at most maxClusters clusters.
func NewMeshletLod(maxClusters int) *MeshletLod {
	return &MeshletLod{}
}

// AddCluster registers one cluster, clusters beyond the limit are reported.
func (m *MeshletLod) AddCluster(c Cluster) error {
	return errNotImplemented
}

// ClusterCount returns the number of live clusters.
func (m *MeshletLod) ClusterCount() int {
	return 0
}

// GroupByAdjacency groups clusters whose centres are within maxDist of each other.
func (m *MeshletLod) GroupByAdjacency(clusters []Cluster, maxDist float64) (map[int][]int, error) {
	return nil, errNotImplemented
}

// BoundSphere returns the tightest sphere around verts.
func (m *MeshletLod) BoundSphere(verts []Vec3) Sphere {
	return Sphere{}
}

// BoundsCoverVertices reports whether c.Bounds contains every vertex of c.
func (m *MeshletLod) BoundsCoverVertices(c Cluster) bool {
	return false
}

// SimplifyPreservesMaterialBoundary reports whether the simplified cluster may
// stand in for orig without crossing into another material.
func (m *MeshletLod) SimplifyPreservesMaterialBoundary(orig, simplified Cluster) bool {
	return false
}

// RebuildIndexBuffer rebuilds the index buffer of c for the given LOD.
func (m *MeshletLod) RebuildIndexBuffer(c Cluster, lod int) ([]uint32, error) {
	return nil, errNotImplemented
}

// ScreenPixelSize returns the on screen diameter of c in pixels.
func (m *MeshletLod) ScreenPixelSize(c Cluster, camPos Vec3, fovDeg, viewportHeight float64) float64 {
	return 0
}

// SelectLOD picks the LOD index for a screen size using descending thresholds.
func (m *MeshletLod) SelectLOD(c Cluster, screenPixels float64, thresholds []float64) int {
	return -1
}

// GroupBufferEpoch returns the version of the shared vertex buffer of a group.
func (m *MeshletLod) GroupBufferEpoch(group int) (uint64, error) {
	return 0, errNotImplemented
}

// SyncGroupBuffer publishes the shared vertex buffer of a group to the GPU.
func (m *MeshletLod) SyncGroupBuffer(group int) error {
	return errNotImplemented
}

// TransitionAlpha returns the blend weight between two LOD levels.
func (m *MeshletLod) TransitionAlpha(fromLOD, toLOD int, progress float64) float64 {
	return 0
}

// ShadowLOD returns the LOD the shadow pass must use for a main pass LOD.
func (m *MeshletLod) ShadowLOD(mainLOD int) int {
	return -1
}

// StreamCluster makes a cluster resident.
func (m *MeshletLod) StreamCluster(id int) error {
	return errNotImplemented
}

// IsResident reports whether a cluster may be rendered.
func (m *MeshletLod) IsResident(id int) bool {
	return false
}

// MaxClusters returns the configured cluster limit.
func (m *MeshletLod) MaxClusters() int {
	return 0
}

// OverflowCount returns how many clusters were dropped because of the limit.
func (m *MeshletLod) OverflowCount() int {
	return 0
}

// DeferFree queues a cluster buffer for release after the current frame.
func (m *MeshletLod) DeferFree(id int) error {
	return errNotImplemented
}

// PendingFrees returns the number of buffers waiting for release.
func (m *MeshletLod) PendingFrees() int {
	return 0
}

// FlushFrees releases all queued buffers.
func (m *MeshletLod) FlushFrees() error {
	return errNotImplemented
}
