package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Vec3 is a point or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// AABB is an axis aligned bounding box.
type AABB struct {
	Min Vec3
	Max Vec3
}

// Object is one renderable submitted to the occlusion culler.
type Object struct {
	ID          int
	Bounds      AABB
	Occluder    bool
	Transparent bool
	Skinned     bool
}

// Query is one GPU occlusion query issued for an object.
type Query struct {
	ObjectID int
	Camera   int
}

// FrameResult is the read back visibility of a single frame.
type FrameResult struct {
	Frame   int
	Visible map[int]bool
}

// DepthTarget identifies the depth buffer of one camera.
type DepthTarget struct {
	Camera     int
	Generation uint64
}

// HiZPyramid is a hierarchical depth pyramid, level 0 is full resolution.
type HiZPyramid struct {
	Widths  []int
	Heights []int
	Levels  [][]float32
}

// OcclusionCuller culls objects with GPU occlusion queries.
type OcclusionCuller struct {
	cameraCount int
	queryPool   int
}

// NewOcclusionCuller returns a culler that serves cameraCount cameras.
func NewOcclusionCuller(cameraCount int) *OcclusionCuller {
	return &OcclusionCuller{}
}

// AddObject registers obj so later passes can look it up by ID.
func (c *OcclusionCuller) AddObject(obj Object) error {
	return errNotImplemented
}

// UpdateSkinnedBounds refreshes the bounds of a skinned object and returns them.
func (c *OcclusionCuller) UpdateSkinnedBounds(id int, bounds AABB) (AABB, error) {
	return AABB{}, errNotImplemented
}

// DilateDepth widens every non-empty depth texel by radius texels.
func (c *OcclusionCuller) DilateDepth(depth []float32, w, h, radius int) ([]float32, error) {
	return nil, errNotImplemented
}

// BuildHiZ builds a depth pyramid from a full resolution depth buffer.
func (c *OcclusionCuller) BuildHiZ(depth []float32, w, h int) (*HiZPyramid, error) {
	return nil, errNotImplemented
}

// ConservativeRange returns the nearest and farthest depth of the texel area
// covered by (x, y) at the given level.
func (p *HiZPyramid) ConservativeRange(level, x, y int) (float64, float64, error) {
	return 0, 0, errNotImplemented
}

// CullVisible returns the IDs of objects that survive occlusion culling.
func (c *OcclusionCuller) CullVisible(camera int, objects []Object) ([]int, error) {
	return nil, errNotImplemented
}

// TransparentCanOcclude reports whether transparent geometry may act as an occluder.
func (c *OcclusionCuller) TransparentCanOcclude() bool {
	return true
}

// MergeDelayed merges the query results of the last frames into one visible set.
func (c *OcclusionCuller) MergeDelayed(results []FrameResult) map[int]bool {
	return nil
}

// DepthTargetFor returns the depth target of one camera.
func (c *OcclusionCuller) DepthTargetFor(camera int) (DepthTarget, error) {
	return DepthTarget{}, errNotImplemented
}

// ReadbackByObjectID maps raw query results back to object IDs.
func (c *OcclusionCuller) ReadbackByObjectID(queries []Query, raw []uint64) (map[int]bool, error) {
	return nil, errNotImplemented
}

// AcquireQueries reserves n queries from the pool and returns the first index.
func (c *OcclusionCuller) AcquireQueries(n int) (int, error) {
	return 0, errNotImplemented
}

// QueryPoolCapacity returns the number of queries the pool holds.
func (c *OcclusionCuller) QueryPoolCapacity() int {
	return 0
}

// ShadowCameraIndex returns the camera the shadow pass must use for a main camera.
func (c *OcclusionCuller) ShadowCameraIndex(mainCamera int) int {
	return -1
}

// CameraInsideOccluder reports whether cam lies inside occ.
func (c *OcclusionCuller) CameraInsideOccluder(cam Vec3, occ AABB) bool {
	return false
}
