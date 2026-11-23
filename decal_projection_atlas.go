package task

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var (
	ErrNotImplemented  = errors.New("not implemented")
	ErrInvalidArgument = errors.New("invalid decal atlas argument")
	ErrDuplicateID     = errors.New("duplicate decal id")
	ErrDecalLimit      = errors.New("decal limit reached")
	ErrDecalPoolFull   = errors.New("decal page pool is full")
	ErrUnknownDecal    = errors.New("unknown decal")
	ErrInvalidPage     = errors.New("invalid decal page")
	ErrInvalidSlot     = errors.New("invalid decal slot")
	ErrSlotBusy        = errors.New("decal slot is already being written")
	ErrAtlasShrink     = errors.New("cannot shrink atlas below used pages")
	ErrDecalFading     = errors.New("decal is fading out")
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

type decalEntry struct {
	decal      Decal
	slot       int
	platformID string
	fading     bool
	sequence   uint64
}

type atlasSlot struct {
	id      string
	active  bool
	writing bool
}

type particleEntry struct {
	position Vec3
}

type platformEntry struct {
	position Vec3
	decals   map[string]struct{}
}

// DecalAtlas manages decal slots, projection and the decal atlas pages.
type DecalAtlas struct {
	mu     sync.RWMutex
	byID   map[string]*decalEntry
	slots  []atlasSlot
	next   int
	free   []int
	nextID uint64

	particles map[string]*particleEntry
	platforms map[string]*platformEntry

	maxDecals int
	pages     int
	usedPages int
	restored  map[string]bool
}

// NewDecalAtlas builds an atlas with a decal limit and a page pool.
func NewDecalAtlas(maxDecals, pages int) (*DecalAtlas, error) {
	if maxDecals < 0 || pages < 0 {
		return nil, ErrInvalidArgument
	}
	return &DecalAtlas{
		byID:      make(map[string]*decalEntry),
		slots:     make([]atlasSlot, maxDecals),
		particles: make(map[string]*particleEntry),
		platforms: make(map[string]*platformEntry),
		maxDecals: maxDecals,
		pages:     pages,
		restored:  make(map[string]bool),
	}, nil
}

// Add registers a decal in a slot and returns the slot index.
func (a *DecalAtlas) Add(id string, page int, pos, normal Vec3) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.byID[id]; ok {
		return -1, ErrDuplicateID
	}
	if _, ok := a.particles[id]; ok {
		return -1, ErrDuplicateID
	}
	if len(a.byID) >= a.maxDecals {
		return -1, ErrDecalLimit
	}
	if a.usedPages >= a.pages {
		return -1, ErrDecalPoolFull
	}
	if page < 0 || page >= a.pages {
		return -1, ErrInvalidPage
	}

	slot, ok := a.allocateSlotLocked()
	if !ok {
		return -1, ErrDecalLimit
	}

	entry := &decalEntry{
		decal: Decal{
			ID:        id,
			Page:      page,
			Position:  pos,
			Normal:    normal,
			BoundsMin: pos,
			BoundsMax: pos,
		},
		slot:     slot,
		sequence: a.nextID,
	}
	a.nextID++
	a.byID[id] = entry
	a.usedPages++
	a.slots[slot] = atlasSlot{id: id, active: true}
	delete(a.restored, id)
	return slot, nil
}

func (a *DecalAtlas) allocateSlotLocked() (int, bool) {
	if n := len(a.free); n > 0 {
		slot := a.free[n-1]
		a.free = a.free[:n-1]
		return slot, true
	}
	if a.next < len(a.slots) {
		slot := a.next
		a.next++
		return slot, true
	}
	return -1, false
}

// SetBounds sets the world space bounding box of a decal.
func (a *DecalAtlas) SetBounds(id string, min, max Vec3) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry, ok := a.byID[id]
	if !ok {
		return ErrUnknownDecal
	}
	entry.decal.BoundsMin, entry.decal.BoundsMax = orderedBounds(min, max)
	return nil
}

// Move changes the decal position.
func (a *DecalAtlas) Move(id string, pos Vec3) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry, ok := a.byID[id]
	if !ok {
		return ErrUnknownDecal
	}
	translateEntry(entry, subVec(pos, entry.decal.Position))
	return nil
}

// ProjectionMatrix returns the decal projection matrix including its position.
func (a *DecalAtlas) ProjectionMatrix(id string) ([16]float64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.byID[id]
	if !ok {
		return [16]float64{}, ErrUnknownDecal
	}

	var matrix [16]float64
	matrix[0] = 1
	matrix[5] = 1
	matrix[10] = 1
	matrix[15] = 1
	matrix[12] = entry.decal.Position.X
	matrix[13] = entry.decal.Position.Y
	matrix[14] = entry.decal.Position.Z
	return matrix, nil
}

// AddPlatform registers a moving platform.
func (a *DecalAtlas) AddPlatform(platformID string, pos Vec3) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	platform, ok := a.platforms[platformID]
	if !ok {
		a.platforms[platformID] = &platformEntry{
			position: pos,
			decals:   make(map[string]struct{}),
		}
		return nil
	}

	delta := subVec(pos, platform.position)
	platform.position = pos
	for id := range platform.decals {
		if entry, ok := a.byID[id]; ok {
			translateEntry(entry, delta)
		}
	}
	return nil
}

// AttachToPlatform makes a decal follow a moving platform.
func (a *DecalAtlas) AttachToPlatform(decalID, platformID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry, ok := a.byID[decalID]
	if !ok {
		return ErrUnknownDecal
	}
	platform, ok := a.platforms[platformID]
	if !ok {
		return ErrUnknownPlatform
	}

	if entry.platformID != "" {
		if previous, ok := a.platforms[entry.platformID]; ok {
			delete(previous.decals, decalID)
		}
	}
	entry.platformID = platformID
	platform.decals[decalID] = struct{}{}
	return nil
}

// MovePlatform moves a platform together with its attached decals.
func (a *DecalAtlas) MovePlatform(platformID string, pos Vec3) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	platform, ok := a.platforms[platformID]
	if !ok {
		return ErrUnknownPlatform
	}

	delta := subVec(pos, platform.position)
	platform.position = pos
	for id := range platform.decals {
		if entry, ok := a.byID[id]; ok {
			translateEntry(entry, delta)
		}
	}
	return nil
}

// SortedIDs returns decal ids sorted back to front for the camera.
func (a *DecalAtlas) SortedIDs(camera Vec3) []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	type sortable struct {
		id       string
		distance float64
	}
	decals := make([]sortable, 0, len(a.byID))
	for id, entry := range a.byID {
		if entry.fading {
			continue
		}
		decals = append(decals, sortable{
			id:       id,
			distance: distanceSquared(entry.decal.Position, camera),
		})
	}
	sort.Slice(decals, func(i, j int) bool {
		if decals[i].distance != decals[j].distance {
			return decals[i].distance > decals[j].distance
		}
		return decals[i].id < decals[j].id
	})

	ids := make([]string, len(decals))
	for i := range decals {
		ids[i] = decals[i].id
	}
	return ids
}

// ProjectMeshes projects a decal onto the given number of meshes.
func (a *DecalAtlas) ProjectMeshes(id string, meshes int) (int, error) {
	if meshes < 0 {
		return 0, ErrInvalidArgument
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.byID[id]
	if !ok {
		return 0, ErrUnknownDecal
	}
	if entry.fading {
		return 0, ErrDecalFading
	}
	return meshes, nil
}

// BlendNormal blends a surface normal with a decal normal.
func (a *DecalAtlas) BlendNormal(surface, decalNormal Vec3) Vec3 {
	blended := addVec(surface, decalNormal)
	if length := vectorLength(blended); length > 1e-12 {
		blended = scaleVec(blended, 1/length)
		if dotVec(blended, decalNormal) < 0 {
			if dl := vectorLength(decalNormal); dl > 1e-12 {
				return scaleVec(decalNormal, 1/dl)
			}
		}
		return blended
	}
	if dl := vectorLength(decalNormal); dl > 1e-12 {
		return scaleVec(decalNormal, 1/dl)
	}
	if sl := vectorLength(surface); sl > 1e-12 {
		return scaleVec(surface, 1/sl)
	}
	return Vec3{}
}

// AddParticle registers a particle effect that is not a decal.
func (a *DecalAtlas) AddParticle(id string, pos Vec3) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.byID[id]; ok {
		return ErrDuplicateID
	}
	if _, ok := a.particles[id]; ok {
		return ErrDuplicateID
	}
	a.particles[id] = &particleEntry{position: pos}
	return nil
}

// PassOf returns the render pass of a decal or particle.
func (a *DecalAtlas) PassOf(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if _, ok := a.byID[id]; ok {
		return "decal"
	}
	if _, ok := a.particles[id]; ok {
		return "particles"
	}
	return ""
}

// Visible reports whether the decal bounding box intersects the camera box.
func (a *DecalAtlas) Visible(id string, camMin, camMax Vec3) (bool, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.byID[id]
	if !ok {
		return false, ErrUnknownDecal
	}
	camMin, camMax = orderedBounds(camMin, camMax)
	min := entry.decal.BoundsMin
	max := entry.decal.BoundsMax
	return min.X <= camMax.X && max.X >= camMin.X &&
		min.Y <= camMax.Y && max.Y >= camMin.Y &&
		min.Z <= camMax.Z && max.Z >= camMin.Z, nil
}

// Count reports how many decals are registered.
func (a *DecalAtlas) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.byID)
}

// Delete removes a decal and restores the depth buffer.
func (a *DecalAtlas) Delete(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry, ok := a.byID[id]
	if !ok {
		return ErrUnknownDecal
	}
	a.releaseEntryLocked(id, entry)
	return nil
}

func (a *DecalAtlas) releaseEntryLocked(id string, entry *decalEntry) {
	if entry.platformID != "" {
		if platform, ok := a.platforms[entry.platformID]; ok {
			delete(platform.decals, id)
		}
	}
	delete(a.byID, id)
	if a.usedPages > 0 {
		a.usedPages--
	}
	a.restored[id] = true

	if entry.slot < 0 || entry.slot >= len(a.slots) {
		return
	}
	slot := &a.slots[entry.slot]
	if slot.active && slot.id != id {
		return
	}
	*slot = atlasSlot{}
	a.free = append(a.free, entry.slot)
}

// FadeOut marks a batch of decals as fading. The batch is atomic: if any id is
// unknown, no decal changes state.
func (a *DecalAtlas) FadeOut(ids ...string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, id := range ids {
		if _, ok := a.byID[id]; !ok {
			return ErrUnknownDecal
		}
	}
	for _, id := range ids {
		a.byID[id].fading = true
	}
	return nil
}

// IsFading reports whether a decal is in its fade-out phase.
func (a *DecalAtlas) IsFading(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.byID[id]
	return ok && entry.fading
}

// ReclaimFaded releases every fading decal in one critical section and reports
// how many decals were reclaimed.
func (a *DecalAtlas) ReclaimFaded() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	faded := make([]*decalEntry, 0)
	for _, entry := range a.byID {
		if entry.fading {
			faded = append(faded, entry)
		}
	}
	sort.Slice(faded, func(i, j int) bool {
		return faded[i].sequence < faded[j].sequence
	})
	for _, entry := range faded {
		a.releaseEntryLocked(entry.decal.ID, entry)
	}
	return len(faded)
}

// DepthRestored reports whether the depth buffer was restored for the decal.
func (a *DecalAtlas) DepthRestored(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.restored[id]
}

// FreePages reports how many atlas pages are still available.
func (a *DecalAtlas) FreePages() int {
	a.mu.RLock()
	defer a.mu.RUnlock()

	free := a.pages - a.usedPages
	if free < 0 {
		return 0
	}
	return free
}

// UVRect returns the UV rectangle of the decal atlas page.
func (a *DecalAtlas) UVRect(id string) (u0, v0, u1, v1 float64, err error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.byID[id]
	if !ok {
		return 0, 0, 0, 0, ErrUnknownDecal
	}
	if a.pages <= 0 {
		return 0, 0, 0, 0, ErrInvalidArgument
	}

	total := float64(a.pages)
	page := float64(entry.decal.Page)
	return page / total, 0, (page + 1) / total, 1, nil
}

// UpdateAtlas changes the atlas page count and recomputes all UVs.
func (a *DecalAtlas) UpdateAtlas(pages int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if pages <= 0 {
		return ErrInvalidArgument
	}
	if pages < a.usedPages {
		return ErrAtlasShrink
	}
	for _, entry := range a.byID {
		if entry.decal.Page < 0 || entry.decal.Page >= pages {
			return ErrAtlasShrink
		}
	}
	a.pages = pages
	return nil
}

// BeginWrite locks a decal slot for writing.
func (a *DecalAtlas) BeginWrite(slot int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if slot < 0 || slot >= len(a.slots) || !a.slots[slot].active {
		return ErrInvalidSlot
	}
	if a.slots[slot].writing {
		return ErrSlotBusy
	}
	a.slots[slot].writing = true
	return nil
}

// EndWrite unlocks a decal slot.
func (a *DecalAtlas) EndWrite(slot int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if slot < 0 || slot >= len(a.slots) || !a.slots[slot].active {
		return ErrInvalidSlot
	}
	a.slots[slot].writing = false
	return nil
}

// SlotOf returns the slot of a decal.
func (a *DecalAtlas) SlotOf(id string) (int, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.byID[id]
	if !ok {
		return 0, false
	}
	return entry.slot, true
}

func orderedBounds(a, b Vec3) (Vec3, Vec3) {
	return Vec3{
			X: math.Min(a.X, b.X),
			Y: math.Min(a.Y, b.Y),
			Z: math.Min(a.Z, b.Z),
		}, Vec3{
			X: math.Max(a.X, b.X),
			Y: math.Max(a.Y, b.Y),
			Z: math.Max(a.Z, b.Z),
		}
}

func translateEntry(entry *decalEntry, delta Vec3) {
	entry.decal.Position = addVec(entry.decal.Position, delta)
	entry.decal.BoundsMin = addVec(entry.decal.BoundsMin, delta)
	entry.decal.BoundsMax = addVec(entry.decal.BoundsMax, delta)
}

func addVec(a, b Vec3) Vec3 {
	return Vec3{X: a.X + b.X, Y: a.Y + b.Y, Z: a.Z + b.Z}
}

func subVec(a, b Vec3) Vec3 {
	return Vec3{X: a.X - b.X, Y: a.Y - b.Y, Z: a.Z - b.Z}
}

func scaleVec(v Vec3, factor float64) Vec3 {
	return Vec3{X: v.X * factor, Y: v.Y * factor, Z: v.Z * factor}
}

func dotVec(a, b Vec3) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}

func vectorLength(v Vec3) float64 {
	return math.Sqrt(dotVec(v, v))
}

func distanceSquared(a, b Vec3) float64 {
	delta := subVec(a, b)
	return dotVec(delta, delta)
}
