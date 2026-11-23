package task

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

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

	// mins is the parallel nearest-depth pyramid. Levels stores the farthest
	// depth of each covered tile so an object is only culled conservatively.
	mins [][]float32
}

type visibilityHistory struct {
	frames []bool
}

type cameraState struct {
	depth   DepthTarget
	history map[int]*visibilityHistory
}

// OcclusionCuller culls objects with GPU occlusion queries.
type OcclusionCuller struct {
	mu sync.Mutex

	cameraCount int
	queryPool   int
	nextQuery   int

	objects map[int]Object
	cameras []cameraState
}

// NewOcclusionCuller returns a culler that serves cameraCount cameras.
func NewOcclusionCuller(cameraCount int) *OcclusionCuller {
	if cameraCount < 0 {
		cameraCount = 0
	}

	cameras := make([]cameraState, cameraCount)
	for i := range cameras {
		cameras[i] = cameraState{
			depth:   DepthTarget{Camera: i, Generation: 1},
			history: make(map[int]*visibilityHistory),
		}
	}

	return &OcclusionCuller{
		cameraCount: cameraCount,
		queryPool:   4096,
		objects:     make(map[int]Object),
		cameras:     cameras,
	}
}

// AddObject registers obj so later passes can look it up by ID.
func (c *OcclusionCuller) AddObject(obj Object) error {
	if obj.ID < 0 {
		return fmt.Errorf("invalid object id %d", obj.ID)
	}
	if !validAABB(obj.Bounds) {
		return fmt.Errorf("invalid bounds for object %d", obj.ID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[obj.ID] = obj
	return nil
}

// UpdateSkinnedBounds refreshes the bounds of a skinned object and returns them.
func (c *OcclusionCuller) UpdateSkinnedBounds(id int, bounds AABB) (AABB, error) {
	if !validAABB(bounds) {
		return AABB{}, fmt.Errorf("invalid bounds for object %d", id)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	obj, ok := c.objects[id]
	if !ok {
		return AABB{}, fmt.Errorf("object %d is not registered", id)
	}
	if !obj.Skinned {
		return AABB{}, fmt.Errorf("object %d is not skinned", id)
	}

	obj.Bounds = bounds
	c.objects[id] = obj
	return bounds, nil
}

// DilateDepth widens every non-empty depth texel by radius texels.
func (c *OcclusionCuller) DilateDepth(depth []float32, w, h, radius int) ([]float32, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid depth dimensions %dx%d", w, h)
	}
	if radius < 0 {
		return nil, fmt.Errorf("invalid dilation radius %d", radius)
	}
	if len(depth) != w*h {
		return nil, fmt.Errorf("depth buffer has %d texels, want %d", len(depth), w*h)
	}

	horizontal := make([]float32, len(depth))
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			lo := x - radius
			if lo < 0 {
				lo = 0
			}
			hi := x + radius
			if hi >= w {
				hi = w - 1
			}

			var widest float32
			for xx := lo; xx <= hi; xx++ {
				if d := depth[row+xx]; d > widest {
					widest = d
				}
			}
			horizontal[row+x] = widest
		}
	}

	out := make([]float32, len(depth))
	for y := 0; y < h; y++ {
		lo := y - radius
		if lo < 0 {
			lo = 0
		}
		hi := y + radius
		if hi >= h {
			hi = h - 1
		}
		for x := 0; x < w; x++ {
			var widest float32
			for yy := lo; yy <= hi; yy++ {
				if d := horizontal[yy*w+x]; d > widest {
					widest = d
				}
			}
			out[y*w+x] = widest
		}
	}

	// Dilation only grows the silhouette; occupied texels keep their original
	// depth so a farther neighbour cannot weaken a real occluder sample.
	for i, d := range depth {
		if d > 0 {
			out[i] = d
		}
	}
	return out, nil
}

// BuildHiZ builds a depth pyramid from a full resolution depth buffer.
func (c *OcclusionCuller) BuildHiZ(depth []float32, w, h int) (*HiZPyramid, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid depth dimensions %dx%d", w, h)
	}
	if len(depth) != w*h {
		return nil, fmt.Errorf("depth buffer has %d texels, want %d", len(depth), w*h)
	}

	p := &HiZPyramid{
		Widths:  []int{w},
		Heights: []int{h},
		Levels:  [][]float32{append([]float32(nil), depth...)},
		mins:    [][]float32{append([]float32(nil), depth...)},
	}

	levelWidth, levelHeight := w, h
	for levelWidth > 1 || levelHeight > 1 {
		nextWidth := (levelWidth + 1) / 2
		nextHeight := (levelHeight + 1) / 2
		nextMax := make([]float32, nextWidth*nextHeight)
		nextMin := make([]float32, nextWidth*nextHeight)

		maxSource := p.Levels[len(p.Levels)-1]
		minSource := p.mins[len(p.mins)-1]
		for y := 0; y < nextHeight; y++ {
			for x := 0; x < nextWidth; x++ {
				first := true
				var tileMax, tileMin float32
				for dy := 0; dy < 2; dy++ {
					sy := 2*y + dy
					if sy >= levelHeight {
						continue
					}
					for dx := 0; dx < 2; dx++ {
						sx := 2*x + dx
						if sx >= levelWidth {
							continue
						}
						index := sy*levelWidth + sx
						maxValue := maxSource[index]
						minValue := minSource[index]
						if first {
							tileMax, tileMin = maxValue, minValue
							first = false
							continue
						}
						if maxValue > tileMax {
							tileMax = maxValue
						}
						if minValue < tileMin {
							tileMin = minValue
						}
					}
				}
				nextMax[y*nextWidth+x] = tileMax
				nextMin[y*nextWidth+x] = tileMin
			}
		}

		p.Widths = append(p.Widths, nextWidth)
		p.Heights = append(p.Heights, nextHeight)
		p.Levels = append(p.Levels, nextMax)
		p.mins = append(p.mins, nextMin)
		levelWidth, levelHeight = nextWidth, nextHeight
	}

	return p, nil
}

// ConservativeRange returns the nearest and farthest depth of the texel area
// covered by (x, y) at the given level.
func (p *HiZPyramid) ConservativeRange(level, x, y int) (float64, float64, error) {
	if p == nil {
		return 0, 0, errors.New("nil HiZ pyramid")
	}
	if level < 0 || level >= len(p.Widths) || level >= len(p.Heights) || level >= len(p.Levels) {
		return 0, 0, fmt.Errorf("HiZ level %d is out of range", level)
	}
	if x < 0 || x >= p.Widths[level] || y < 0 || y >= p.Heights[level] {
		return 0, 0, fmt.Errorf("HiZ coordinate (%d,%d) is out of range at level %d", x, y, level)
	}

	index := y*p.Widths[level] + x
	if index >= len(p.Levels[level]) {
		return 0, 0, errors.New("malformed HiZ level")
	}

	farthest := p.Levels[level][index]
	nearest := farthest
	if level < len(p.mins) && index < len(p.mins[level]) {
		nearest = p.mins[level][index]
	}
	return float64(nearest), float64(farthest), nil
}

// CullVisible returns the IDs of objects that survive occlusion culling.
func (c *OcclusionCuller) CullVisible(camera int, objects []Object) ([]int, error) {
	if camera < 0 || camera >= c.cameraCount {
		return nil, fmt.Errorf("camera %d is out of range", camera)
	}

	resolved := make([]Object, 0, len(objects))
	seen := make(map[int]bool, len(objects))
	for _, obj := range objects {
		if obj.ID < 0 {
			return nil, fmt.Errorf("invalid object id %d", obj.ID)
		}
		if !validAABB(obj.Bounds) {
			return nil, fmt.Errorf("invalid bounds for object %d", obj.ID)
		}
		if seen[obj.ID] {
			continue
		}
		seen[obj.ID] = true
		resolved = append(resolved, obj)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cam := &c.cameras[camera]
	if len(resolved) == 0 {
		return []int{}, nil
	}

	for i := range resolved {
		obj := resolved[i]
		if stored, ok := c.objects[obj.ID]; ok && (stored.Skinned || obj.Skinned) {
			obj.Bounds = stored.Bounds
			obj.Skinned = true
			resolved[i] = obj
		}
		c.objects[obj.ID] = obj
	}

	ids := make([]int, len(resolved))
	for i, obj := range resolved {
		ids[i] = obj.ID
	}

	// Queries are simulated for the visibility decision. If the fixed pool
	// has no room left, fail open instead of dropping objects.
	if _, err := c.acquireLocked(len(resolved)); err != nil {
		c.observeAll(cam, resolved, true)
		return ids, nil
	}
	c.releaseLocked(len(resolved))

	cameraInside := false
	for _, obj := range resolved {
		if obj.Occluder && !obj.Transparent && pointInAABB(Vec3{}, obj.Bounds) {
			cameraInside = true
			break
		}
	}
	if cameraInside {
		c.observeAll(cam, resolved, true)
		return ids, nil
	}

	visible := make([]int, 0, len(resolved))
	for _, target := range resolved {
		result := target.Occluder
		if !result {
			result = true
			for _, occluder := range resolved {
				if occluder.ID == target.ID || !occluder.Occluder || occluder.Transparent {
					continue
				}
				if aabbOccludes(occluder.Bounds, target.Bounds) {
					result = false
					break
				}
			}
		}

		// Readback arrives late. Keep a small history and only reject an object
		// after it has tested hidden for two held frames (three observations).
		if cam.observe(target.ID, result) {
			visible = append(visible, target.ID)
		}
	}
	return visible, nil
}

func (c *OcclusionCuller) observeAll(cam *cameraState, objects []Object, visible bool) {
	for _, obj := range objects {
		cam.observe(obj.ID, visible)
	}
}

func (cam *cameraState) observe(id int, current bool) bool {
	history, ok := cam.history[id]
	if !ok {
		// There are no results for the two frames before an object first
		// appears, so start in the conservative visible state.
		history = &visibilityHistory{frames: []bool{true, true}}
		cam.history[id] = history
	}

	history.frames = append(history.frames, current)
	if len(history.frames) > 3 {
		history.frames = history.frames[len(history.frames)-3:]
	}
	for _, frameVisible := range history.frames {
		if frameVisible {
			return true
		}
	}
	return false
}

// TransparentCanOcclude reports whether transparent geometry may act as an occluder.
func (c *OcclusionCuller) TransparentCanOcclude() bool {
	return false
}

// MergeDelayed merges the query results of the last frames into one visible set.
func (c *OcclusionCuller) MergeDelayed(results []FrameResult) map[int]bool {
	merged := make(map[int]bool)
	for _, result := range results {
		for id, visible := range result.Visible {
			if visible {
				merged[id] = true
			} else if _, exists := merged[id]; !exists {
				merged[id] = false
			}
		}
	}
	return merged
}

// DepthTargetFor returns the depth target of one camera.
func (c *OcclusionCuller) DepthTargetFor(camera int) (DepthTarget, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if camera < 0 || camera >= c.cameraCount {
		return DepthTarget{}, fmt.Errorf("camera %d is out of range", camera)
	}
	return c.cameras[camera].depth, nil
}

// ReadbackByObjectID maps raw query results back to object IDs.
func (c *OcclusionCuller) ReadbackByObjectID(queries []Query, raw []uint64) (map[int]bool, error) {
	if len(queries) != len(raw) {
		return nil, fmt.Errorf("readback has %d query results for %d queries", len(raw), len(queries))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	visible := make(map[int]bool, len(queries))
	for i, query := range queries {
		if query.ObjectID < 0 {
			return nil, fmt.Errorf("invalid object id %d", query.ObjectID)
		}
		if query.Camera < 0 || query.Camera >= c.cameraCount {
			return nil, fmt.Errorf("camera %d is out of range", query.Camera)
		}

		result := raw[i] != 0
		if previous, ok := visible[query.ObjectID]; ok {
			visible[query.ObjectID] = previous || result
		} else {
			visible[query.ObjectID] = result
		}
	}
	return visible, nil
}

// AcquireQueries reserves n queries from the pool and returns the first index.
func (c *OcclusionCuller) AcquireQueries(n int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acquireLocked(n)
}

func (c *OcclusionCuller) acquireLocked(n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("invalid query count %d", n)
	}
	if n > c.queryPool || n > c.queryPool-c.nextQuery {
		return 0, fmt.Errorf("query pool exhausted: %d requested, %d available", n, c.queryPool-c.nextQuery)
	}

	first := c.nextQuery
	c.nextQuery += n
	return first, nil
}

func (c *OcclusionCuller) releaseLocked(n int) {
	if n <= 0 || n > c.nextQuery {
		return
	}
	c.nextQuery -= n
}

// QueryPoolCapacity returns the number of queries the pool holds.
func (c *OcclusionCuller) QueryPoolCapacity() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queryPool
}

// ShadowCameraIndex returns the camera the shadow pass must use for a main camera.
func (c *OcclusionCuller) ShadowCameraIndex(mainCamera int) int {
	if mainCamera < 0 || mainCamera >= c.cameraCount {
		return -1
	}
	return mainCamera
}

// CameraInsideOccluder reports whether cam lies inside occ.
func (c *OcclusionCuller) CameraInsideOccluder(cam Vec3, occ AABB) bool {
	return pointInAABB(cam, occ)
}

func validAABB(box AABB) bool {
	values := [...]float64{
		box.Min.X, box.Min.Y, box.Min.Z,
		box.Max.X, box.Max.Y, box.Max.Z,
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return box.Min.X <= box.Max.X &&
		box.Min.Y <= box.Max.Y &&
		box.Min.Z <= box.Max.Z
}

func pointInAABB(point Vec3, box AABB) bool {
	return point.X >= box.Min.X && point.X <= box.Max.X &&
		point.Y >= box.Min.Y && point.Y <= box.Max.Y &&
		point.Z >= box.Min.Z && point.Z <= box.Max.Z
}

// aabbOccludes is a conservative CPU-side stand-in for the depth query used
// by the renderer. The default camera is at the origin looking down +Z; a box
// can hide another only when it is wholly in front and projects around it.
func aabbOccludes(occluder, target AABB) bool {
	if occluder.Max.Z <= 0 || target.Min.Z <= 0 || occluder.Max.Z > target.Min.Z {
		return false
	}

	occMinX, occMaxX, occMinY, occMaxY, ok := projectedExtents(occluder)
	if !ok {
		return false
	}
	targetMinX, targetMaxX, targetMinY, targetMaxY, ok := projectedExtents(target)
	if !ok {
		return false
	}

	const epsilon = 1e-9
	return occMinX-epsilon <= targetMinX &&
		occMaxX+epsilon >= targetMaxX &&
		occMinY-epsilon <= targetMinY &&
		occMaxY+epsilon >= targetMaxY
}

func projectedExtents(box AABB) (minX, maxX, minY, maxY float64, ok bool) {
	if !validAABB(box) || box.Min.Z <= 0 {
		return 0, 0, 0, 0, false
	}

	first := true
	for _, x := range [...]float64{box.Min.X, box.Max.X} {
		for _, y := range [...]float64{box.Min.Y, box.Max.Y} {
			for _, z := range [...]float64{box.Min.Z, box.Max.Z} {
				projectedX := x / z
				projectedY := y / z
				if first {
					minX, maxX = projectedX, projectedX
					minY, maxY = projectedY, projectedY
					first = false
					continue
				}
				if projectedX < minX {
					minX = projectedX
				}
				if projectedX > maxX {
					maxX = projectedX
				}
				if projectedY < minY {
					minY = projectedY
				}
				if projectedY > maxY {
					maxY = projectedY
				}
			}
		}
	}
	return minX, maxX, minY, maxY, !first
}
