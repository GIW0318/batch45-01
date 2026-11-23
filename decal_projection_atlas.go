package task

import "errors"

var (
	ErrNotImplemented  = errors.New("not implemented")
	ErrDecalLimit      = errors.New("decal limit reached")
	ErrUnknownDecal    = errors.New("unknown decal")
	ErrSlotBusy        = errors.New("decal slot is already being written")
	ErrUnknownPlatform = errors.New("unknown platform")
)

// Vec3 is a three component vector.
type Vec3 struct {
	X, Y, Z float64
}

// Decal is one projected decal.
type Decal struct {
	ID        string
	Page      int
	Position  Vec3
	Normal    Vec3
	BoundsMin Vec3
	BoundsMax Vec3
}

// DecalAtlas manages decal slots, projection and the decal atlas pages.
type DecalAtlas struct{}

// NewDecalAtlas builds an atlas with a decal limit and a page pool.
func NewDecalAtlas(maxDecals, pages int) (*DecalAtlas, error) {
	return nil, ErrNotImplemented
}

// Add registers a decal in a slot and returns the slot index.
func (a *DecalAtlas) Add(id string, page int, pos, normal Vec3) (int, error) {
	return 0, ErrNotImplemented
}

// SetBounds sets the world space bounding box of a decal.
func (a *DecalAtlas) SetBounds(id string, min, max Vec3) error { return ErrNotImplemented }

// Move changes the decal position.
func (a *DecalAtlas) Move(id string, pos Vec3) error { return ErrNotImplemented }

// ProjectionMatrix returns the decal projection matrix including its position.
func (a *DecalAtlas) ProjectionMatrix(id string) ([16]float64, error) {
	return [16]float64{}, ErrNotImplemented
}

// AddPlatform registers a moving platform.
func (a *DecalAtlas) AddPlatform(platformID string, pos Vec3) error { return ErrNotImplemented }

// AttachToPlatform makes a decal follow a moving platform.
func (a *DecalAtlas) AttachToPlatform(decalID, platformID string) error { return ErrNotImplemented }

// MovePlatform moves a platform together with its attached decals.
func (a *DecalAtlas) MovePlatform(platformID string, pos Vec3) error { return ErrNotImplemented }

// SortedIDs returns decal ids sorted back to front for the camera.
func (a *DecalAtlas) SortedIDs(camera Vec3) []string { return nil }

// ProjectMeshes projects a decal onto the given number of meshes.
func (a *DecalAtlas) ProjectMeshes(id string, meshes int) (int, error) {
	return 0, ErrNotImplemented
}

// BlendNormal blends a surface normal with a decal normal.
func (a *DecalAtlas) BlendNormal(surface, decalNormal Vec3) Vec3 { return Vec3{} }

// AddParticle registers a particle effect that is not a decal.
func (a *DecalAtlas) AddParticle(id string, pos Vec3) error { return ErrNotImplemented }

// PassOf returns the render pass of a decal or particle.
func (a *DecalAtlas) PassOf(id string) string { return "" }

// Visible reports whether the decal bounding box intersects the camera box.
func (a *DecalAtlas) Visible(id string, camMin, camMax Vec3) (bool, error) {
	return false, ErrNotImplemented
}

// Count reports how many decals are registered.
func (a *DecalAtlas) Count() int { return 0 }

// Delete removes a decal and restores the depth buffer.
func (a *DecalAtlas) Delete(id string) error { return ErrNotImplemented }

// DepthRestored reports whether the depth buffer was restored for the decal.
func (a *DecalAtlas) DepthRestored(id string) bool { return false }

// FreePages reports how many atlas pages are still available.
func (a *DecalAtlas) FreePages() int { return 0 }

// UVRect returns the UV rectangle of the decal atlas page.
func (a *DecalAtlas) UVRect(id string) (u0, v0, u1, v1 float64, err error) {
	return 0, 0, 0, 0, ErrNotImplemented
}

// UpdateAtlas changes the atlas page count and recomputes all UVs.
func (a *DecalAtlas) UpdateAtlas(pages int) error { return ErrNotImplemented }

// BeginWrite locks a decal slot for writing.
func (a *DecalAtlas) BeginWrite(slot int) error { return ErrNotImplemented }

// EndWrite unlocks a decal slot.
func (a *DecalAtlas) EndWrite(slot int) error { return ErrNotImplemented }

// SlotOf returns the slot of a decal.
func (a *DecalAtlas) SlotOf(id string) (int, bool) { return 0, false }
