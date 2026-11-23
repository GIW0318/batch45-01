package task

import "errors"

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrInvalidGrid    = errors.New("invalid cluster grid")
	ErrInvalidCamera  = errors.New("invalid camera frustum")
	ErrUnknownLight   = errors.New("unknown light")
)

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
type LightBinner struct{}

// NewLightBinner builds a binner for the frustum and grid resolution.
func NewLightBinner(cam Camera, nx, ny, nz int) (*LightBinner, error) {
	return nil, ErrNotImplemented
}

// SetCamera replaces the camera and rebuilds the clusters immediately.
func (b *LightBinner) SetCamera(cam Camera) error { return ErrNotImplemented }

// SetGrid changes the cluster grid resolution and rebuilds.
func (b *LightBinner) SetGrid(nx, ny, nz int) error { return ErrNotImplemented }

// Grid reports the current cluster grid resolution.
func (b *LightBinner) Grid() (int, int, int) { return 0, 0, 0 }

// SetClusterCapacity caps how many lights one cluster may hold.
func (b *LightBinner) SetClusterCapacity(n int) error { return ErrNotImplemented }

// AddLight adds a light and bins it, returning its stable index.
func (b *LightBinner) AddLight(l Light) (int, error) { return 0, ErrNotImplemented }

// UpdateLight replaces a light and rebins it.
func (b *LightBinner) UpdateLight(id string, l Light) error { return ErrNotImplemented }

// RemoveLight removes a light and recycles its index.
func (b *LightBinner) RemoveLight(id string) error { return ErrNotImplemented }

// LightIndex returns the index of a light.
func (b *LightBinner) LightIndex(id string) (int, bool) { return 0, false }

// Clusters returns the light indices per cluster.
func (b *LightBinner) Clusters() map[ClusterKey][]int { return nil }

// ClusterAt returns the light indices of one cluster.
func (b *LightBinner) ClusterAt(k ClusterKey) []int { return nil }

// Overflows reports how many cluster assignments were dropped.
func (b *LightBinner) Overflows() int { return 0 }

// ShadowSlot maps a light index to its shadow map slot.
func (b *LightBinner) ShadowSlot(index int) int { return 0 }

// Attenuation evaluates the light falloff at a sample position.
func (b *LightBinner) Attenuation(l Light, sample Vec3) float64 { return 0 }

// TransparentPassLights reports how many lights are bound to the transparent pass.
func (b *LightBinner) TransparentPassLights() int { return 0 }

// Rebuild recomputes all cluster assignments.
func (b *LightBinner) Rebuild() error { return ErrNotImplemented }
