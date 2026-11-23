package task

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrInvalidGrid    = errors.New("invalid cluster grid")
	ErrInvalidCamera  = errors.New("invalid camera frustum")
	ErrUnknownLight   = errors.New("unknown light")
	// ErrDuplicateLight is returned when a light ID is already registered.
	ErrDuplicateLight = errors.New("duplicate light")
	// ErrInvalidCapacity is returned for a non-positive cluster capacity.
	ErrInvalidCapacity = errors.New("invalid cluster capacity")
)

// defaultClusterCapacity is the per-cluster light cap used until
// SetClusterCapacity overrides it.
const defaultClusterCapacity = 64

// Vec3 is a three component vector.
type Vec3 struct {
	X, Y, Z float64
}

// LightType distinguishes the supported light shapes.
type LightType int

const (
	// PointLight has an omnidirectional falloff.
	PointLight LightType = iota
	// SpotLight has a cone shaped falloff.
	SpotLight
)

// Light is one scene light.
type Light struct {
	ID        string
	Type      LightType
	Position  Vec3
	Direction Vec3
	Radius    float64
	CosCutoff float64
	Intensity float64
}

// Camera is an axis aligned view frustum box.
type Camera struct {
	Min Vec3
	Max Vec3
}

// ClusterKey identifies one cluster of the 3D grid.
type ClusterKey struct {
	X, Y, Z int
}

// LightBinner bins lights into the clusters of the camera frustum.
//
// Cluster assignments live in two flat, preallocated slices (counts and
// members) so a steady-state Rebuild performs zero heap allocations.
// All exported methods are safe for concurrent use.
type LightBinner struct {
	mu         sync.RWMutex
	cam        Camera
	nx, ny, nz int
	capacity   int

	lights  []Light
	active  []bool
	idToIdx map[string]int
	freeIdx []int // ascending pool of recycled light indices

	counts    []int // per-cell light count, len == nx*ny*nz
	members   []int // per-cell light indices, len == nx*ny*nz*capacity
	overflows int   // assignments dropped since the last rebuild
	visible   int   // lights passing the frustum test on the last rebuild
}

func validCamera(cam Camera) bool {
	return cam.Min.X <= cam.Max.X && cam.Min.Y <= cam.Max.Y && cam.Min.Z <= cam.Max.Z
}

func validGrid(nx, ny, nz int) bool {
	return nx >= 1 && ny >= 1 && nz >= 1
}

// NewLightBinner builds a binner for the frustum and grid resolution.
func NewLightBinner(cam Camera, nx, ny, nz int) (*LightBinner, error) {
	if !validGrid(nx, ny, nz) {
		return nil, ErrInvalidGrid
	}
	if !validCamera(cam) {
		return nil, ErrInvalidCamera
	}
	b := &LightBinner{
		cam:      cam,
		nx:       nx,
		ny:       ny,
		nz:       nz,
		capacity: defaultClusterCapacity,
		idToIdx:  make(map[string]int),
	}
	b.allocBuffers()
	return b, nil
}

// allocBuffers (re)creates the flat cluster storage for the current grid
// and capacity. Callers must hold b.mu.
func (b *LightBinner) allocBuffers() {
	cells := b.nx * b.ny * b.nz
	b.counts = make([]int, cells)
	b.members = make([]int, cells*b.capacity)
}

// SetCamera replaces the camera and rebuilds the clusters immediately.
func (b *LightBinner) SetCamera(cam Camera) error {
	if !validCamera(cam) {
		return ErrInvalidCamera
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cam = cam
	b.rebuildLocked()
	return nil
}

// SetGrid changes the cluster grid resolution and rebuilds.
func (b *LightBinner) SetGrid(nx, ny, nz int) error {
	if !validGrid(nx, ny, nz) {
		return ErrInvalidGrid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nx, b.ny, b.nz = nx, ny, nz
	b.allocBuffers()
	b.rebuildLocked()
	return nil
}

// Grid reports the current cluster grid resolution.
func (b *LightBinner) Grid() (int, int, int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.nx, b.ny, b.nz
}

// SetClusterCapacity caps how many lights one cluster may hold.
func (b *LightBinner) SetClusterCapacity(n int) error {
	if n < 1 {
		return ErrInvalidCapacity
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.capacity = n
	b.allocBuffers()
	b.rebuildLocked()
	return nil
}

// AddLight adds a light and bins it, returning its stable index.
func (b *LightBinner) AddLight(l Light) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.idToIdx[l.ID]; exists {
		return 0, ErrDuplicateLight
	}
	var idx int
	if len(b.freeIdx) > 0 {
		idx = b.freeIdx[0]
		b.freeIdx = b.freeIdx[1:]
		b.lights[idx] = l
		b.active[idx] = true
	} else {
		idx = len(b.lights)
		b.lights = append(b.lights, l)
		b.active = append(b.active, true)
	}
	b.idToIdx[l.ID] = idx
	b.rebuildLocked()
	return idx, nil
}

// UpdateLight replaces a light and rebins it.
func (b *LightBinner) UpdateLight(id string, l Light) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx, ok := b.idToIdx[id]
	if !ok {
		return ErrUnknownLight
	}
	if l.ID != id {
		if _, taken := b.idToIdx[l.ID]; taken {
			return ErrDuplicateLight
		}
		delete(b.idToIdx, id)
		b.idToIdx[l.ID] = idx
	}
	b.lights[idx] = l
	b.rebuildLocked()
	return nil
}

// RemoveLight removes a light and recycles its index.
func (b *LightBinner) RemoveLight(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx, ok := b.idToIdx[id]
	if !ok {
		return ErrUnknownLight
	}
	delete(b.idToIdx, id)
	b.active[idx] = false
	pos := sort.SearchInts(b.freeIdx, idx)
	b.freeIdx = append(b.freeIdx, 0)
	copy(b.freeIdx[pos+1:], b.freeIdx[pos:])
	b.freeIdx[pos] = idx
	b.rebuildLocked()
	return nil
}

// LightIndex returns the index of a light.
func (b *LightBinner) LightIndex(id string) (int, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idx, ok := b.idToIdx[id]
	return idx, ok
}

// Clusters returns the light indices per cluster.
func (b *LightBinner) Clusters() map[ClusterKey][]int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[ClusterKey][]int)
	for z := 0; z < b.nz; z++ {
		for y := 0; y < b.ny; y++ {
			for x := 0; x < b.nx; x++ {
				cell := (z*b.ny+y)*b.nx + x
				n := b.counts[cell]
				if n == 0 {
					continue
				}
				idxs := make([]int, n)
				copy(idxs, b.members[cell*b.capacity:cell*b.capacity+n])
				out[ClusterKey{X: x, Y: y, Z: z}] = idxs
			}
		}
	}
	return out
}

// ClusterAt returns the light indices of one cluster.
func (b *LightBinner) ClusterAt(k ClusterKey) []int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if k.X < 0 || k.X >= b.nx || k.Y < 0 || k.Y >= b.ny || k.Z < 0 || k.Z >= b.nz {
		return nil
	}
	cell := (k.Z*b.ny+k.Y)*b.nx + k.X
	n := b.counts[cell]
	if n == 0 {
		return nil
	}
	out := make([]int, n)
	copy(out, b.members[cell*b.capacity:cell*b.capacity+n])
	return out
}

// Overflows reports how many cluster assignments were dropped.
func (b *LightBinner) Overflows() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.overflows
}

// ShadowSlot maps a light index to its shadow map slot. Slots are dense
// over the active lights: a light's slot is its rank among active indices.
func (b *LightBinner) ShadowSlot(index int) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if index < 0 {
		return -1
	}
	slot := 0
	for i := 0; i < index && i < len(b.active); i++ {
		if b.active[i] {
			slot++
		}
	}
	return slot
}

// Attenuation evaluates the light falloff at a sample position.
//
// Point lights fall off as intensity/(1+d^2). Spot lights multiply that
// by a cone factor that ramps from 0 at CosCutoff to 1 straight ahead.
func (b *LightBinner) Attenuation(l Light, sample Vec3) float64 {
	dx := sample.X - l.Position.X
	dy := sample.Y - l.Position.Y
	dz := sample.Z - l.Position.Z
	d2 := dx*dx + dy*dy + dz*dz
	att := l.Intensity / (1 + d2)
	if l.Type != SpotLight {
		return att
	}
	if d2 == 0 {
		return att
	}
	dl := math.Sqrt(l.Direction.X*l.Direction.X + l.Direction.Y*l.Direction.Y + l.Direction.Z*l.Direction.Z)
	if dl == 0 {
		return att
	}
	d := math.Sqrt(d2)
	cos := (dx*l.Direction.X + dy*l.Direction.Y + dz*l.Direction.Z) / (d * dl)
	if cos < l.CosCutoff {
		return 0
	}
	if width := 1 - l.CosCutoff; width > 0 {
		f := (cos - l.CosCutoff) / width
		if f > 1 {
			f = 1
		}
		att *= f
	}
	return att
}

// TransparentPassLights reports how many lights are bound to the transparent pass.
func (b *LightBinner) TransparentPassLights() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.visible
}

// Rebuild recomputes all cluster assignments.
//
// It reuses the preallocated counts/members buffers and performs no heap
// allocations in steady state.
func (b *LightBinner) Rebuild() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rebuildLocked()
	return nil
}

// sphereIntersectsBox reports whether a light's bounding sphere touches or
// overlaps the camera box. Boundary contact counts as a hit.
func sphereIntersectsBox(center Vec3, radius float64, cam Camera) bool {
	if radius < 0 {
		radius = 0
	}
	d2 := 0.0
	ps := [3]float64{center.X, center.Y, center.Z}
	mins := [3]float64{cam.Min.X, cam.Min.Y, cam.Min.Z}
	maxs := [3]float64{cam.Max.X, cam.Max.Y, cam.Max.Z}
	for i := 0; i < 3; i++ {
		p := ps[i]
		if p < mins[i] {
			d := mins[i] - p
			d2 += d * d
		} else if p > maxs[i] {
			d := p - maxs[i]
			d2 += d * d
		}
	}
	return d2 <= radius*radius
}

// axisCell maps a coordinate already clamped to the box onto a cell index.
func axisCell(p, min, size float64, n int) int {
	if size <= 0 {
		return 0
	}
	c := int((p - min) / size)
	if c < 0 {
		return 0
	}
	if c >= n {
		return n - 1
	}
	return c
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// rebuildLocked recomputes every cluster assignment. Callers must hold b.mu.
func (b *LightBinner) rebuildLocked() {
	for i := range b.counts {
		b.counts[i] = 0
	}
	b.overflows = 0
	b.visible = 0

	sx := (b.cam.Max.X - b.cam.Min.X) / float64(b.nx)
	sy := (b.cam.Max.Y - b.cam.Min.Y) / float64(b.ny)
	sz := (b.cam.Max.Z - b.cam.Min.Z) / float64(b.nz)

	for idx := range b.lights {
		if !b.active[idx] {
			continue
		}
		l := b.lights[idx]
		r := l.Radius
		if r < 0 {
			r = 0
		}
		if !sphereIntersectsBox(l.Position, r, b.cam) {
			continue
		}
		b.visible++

		x0 := axisCell(clampf(l.Position.X-r, b.cam.Min.X, b.cam.Max.X), b.cam.Min.X, sx, b.nx)
		x1 := axisCell(clampf(l.Position.X+r, b.cam.Min.X, b.cam.Max.X), b.cam.Min.X, sx, b.nx)
		y0 := axisCell(clampf(l.Position.Y-r, b.cam.Min.Y, b.cam.Max.Y), b.cam.Min.Y, sy, b.ny)
		y1 := axisCell(clampf(l.Position.Y+r, b.cam.Min.Y, b.cam.Max.Y), b.cam.Min.Y, sy, b.ny)
		z0 := axisCell(clampf(l.Position.Z-r, b.cam.Min.Z, b.cam.Max.Z), b.cam.Min.Z, sz, b.nz)
		z1 := axisCell(clampf(l.Position.Z+r, b.cam.Min.Z, b.cam.Max.Z), b.cam.Min.Z, sz, b.nz)

		for z := z0; z <= z1; z++ {
			for y := y0; y <= y1; y++ {
				for x := x0; x <= x1; x++ {
					cell := (z*b.ny+y)*b.nx + x
					if b.counts[cell] < b.capacity {
						b.members[cell*b.capacity+b.counts[cell]] = idx
						b.counts[cell]++
					} else {
						b.overflows++
					}
				}
			}
		}
	}
}
