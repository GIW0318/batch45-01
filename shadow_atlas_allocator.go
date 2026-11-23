package task

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrNotImplemented   = errors.New("not implemented")
	ErrAlreadyAllocated = errors.New("light already has an atlas page")
	ErrAtlasFull        = errors.New("shadow atlas is full")
	ErrUnknownLight     = errors.New("unknown light")
	ErrAtlasBusy        = errors.New("atlas rebuild blocked by in-flight pages")
	ErrInvalidSize      = errors.New("invalid page size")
	ErrPageBusy         = errors.New("page is already being updated")
)

// Rect is a page rectangle inside the atlas.
type Rect struct {
	X, Y, W, H int
}

// Region is a dedicated area of the shadow atlas.
type Region int

const (
	// RegionStatic holds pages of static shadow casters.
	RegionStatic Region = iota
	// RegionDynamic holds pages of dynamic shadow casters.
	RegionDynamic
	// RegionCascade holds cascade shadow pages.
	RegionCascade
)

// Page is one allocated atlas page.
type Page struct {
	Index   int
	Rect    Rect
	LightID string
	Region  Region
	Cascade int
	Cleared bool
}

// ShadowAtlas allocates shadow pages inside one texture atlas.
//
// The page table has one slot per allocation index. Slots are kept after a
// release as cleared tombstones so PageAt can observe the release, then are
// reused by the next allocation. All reads of the table take RLock; rebuild
// prepares a complete replacement table and swaps it in one write section.
type ShadowAtlas struct {
	mu sync.RWMutex

	width  int
	height int
	cell   int

	pages []Page
	used  []bool
	busy  []bool
	align []int
	light map[string]int

	inFlight int
	barriers int
	overflow int
	version  uint64
}

// NewShadowAtlas builds an atlas of the given size and page cell.
func NewShadowAtlas(width, height, cell int) (*ShadowAtlas, error) {
	if width <= 0 || height <= 0 || cell <= 0 {
		return nil, ErrInvalidSize
	}
	return &ShadowAtlas{
		width:  width,
		height: height,
		cell:   cell,
		light:  make(map[string]int),
	}, nil
}

// Width reports the atlas width.
func (a *ShadowAtlas) Width() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.width
}

// Height reports the atlas height.
func (a *ShadowAtlas) Height() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.height
}

// Allocate reserves one aligned page for a light.
func (a *ShadowAtlas) Allocate(lightID string, size, align int) (Page, error) {
	return a.allocate(lightID, size, align, RegionDynamic, 0)
}

// AllocateIn reserves one aligned page in the given region.
func (a *ShadowAtlas) AllocateIn(lightID string, size, align int, region Region) (Page, error) {
	return a.allocate(lightID, size, align, region, 0)
}

// AllocateCascade reserves one page for a cascade level.
func (a *ShadowAtlas) AllocateCascade(lightID string, size, align, cascade int) (Page, error) {
	if cascade < 0 {
		return Page{}, ErrInvalidSize
	}
	return a.allocate(lightID, size, align, RegionCascade, cascade)
}

func (a *ShadowAtlas) allocate(lightID string, size, align int, region Region, cascade int) (Page, error) {
	if size <= 0 || align <= 0 {
		return Page{}, ErrInvalidSize
	}
	if region < RegionStatic || region > RegionCascade {
		return Page{}, ErrInvalidSize
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.light[lightID]; ok {
		return Page{}, ErrAlreadyAllocated
	}

	w := roundUp(size, a.cell)
	h := w
	rect, ok := findRect(a.width, a.height, a.activePagesLocked(), w, h, align, region)
	if !ok {
		a.overflow++
		return Page{}, ErrAtlasFull
	}

	index := a.freeSlotLocked()
	if index == len(a.pages) {
		a.pages = append(a.pages, Page{})
		a.used = append(a.used, false)
		a.busy = append(a.busy, false)
		a.align = append(a.align, 0)
	}

	p := Page{
		Index:   index,
		Rect:    rect,
		LightID: lightID,
		Region:  region,
		Cascade: cascade,
		Cleared: true,
	}
	a.pages[index] = p
	a.used[index] = true
	a.busy[index] = false
	a.align[index] = align
	a.light[lightID] = index
	a.version++
	return p, nil
}

// Release frees the page of a light and clears its depth content.
func (a *ShadowAtlas) Release(lightID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	index, ok := a.light[lightID]
	if !ok {
		return ErrUnknownLight
	}
	if a.busy[index] {
		a.busy[index] = false
		a.inFlight--
	}

	p := a.pages[index]
	p.LightID = ""
	p.Cleared = true
	a.pages[index] = p
	a.used[index] = false
	delete(a.light, lightID)
	a.version++
	return nil
}

// PageOf returns the page of a light.
func (a *ShadowAtlas) PageOf(lightID string) (Page, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	index, ok := a.light[lightID]
	if !ok {
		return Page{}, false
	}
	return a.pages[index], true
}

// PageAt returns the page stored in a slot index.
func (a *ShadowAtlas) PageAt(index int) (Page, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if index < 0 || index >= len(a.pages) {
		return Page{}, false
	}
	return a.pages[index], true
}

// Pages returns all allocated pages.
func (a *ShadowAtlas) Pages() []Page {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]Page, 0, len(a.light))
	for i := range a.pages {
		if a.used[i] {
			out = append(out, a.pages[i])
		}
	}
	return out
}

// FreePages reports how many pages are still available.
func (a *ShadowAtlas) FreePages() int {
	a.mu.RLock()
	defer a.mu.RUnlock()

	capacity := (a.width / a.cell) * (a.height / a.cell)
	used := 0
	for i := range a.pages {
		if !a.used[i] {
			continue
		}
		r := a.pages[i].Rect
		used += ((r.W + a.cell - 1) / a.cell) * ((r.H + a.cell - 1) / a.cell)
	}
	free := capacity - used
	if free < 0 {
		return 0
	}
	return free
}

// Overflow reports how many allocations failed because the atlas was full.
func (a *ShadowAtlas) Overflow() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.overflow
}

// UVRect returns the UV rectangle of a light page with border padding.
func (a *ShadowAtlas) UVRect(lightID string) (u0, v0, u1, v1 float64, err error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	index, ok := a.light[lightID]
	if !ok {
		return 0, 0, 0, 0, ErrUnknownLight
	}
	u0, v0, u1, v1 = uvRect(a.pages[index].Rect, a.width, a.height)
	return u0, v0, u1, v1, nil
}

// Resize changes the atlas size and recomputes all UVs.
func (a *ShadowAtlas) Resize(width, height int) error {
	if width <= 0 || height <= 0 {
		return ErrInvalidSize
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.inFlight != 0 {
		return ErrAtlasBusy
	}
	for i := range a.pages {
		if !a.used[i] {
			continue
		}
		r := a.pages[i].Rect
		if r.X+r.W > width || r.Y+r.H > height {
			return ErrAtlasFull
		}
	}
	if a.width == width && a.height == height {
		return nil
	}
	a.width = width
	a.height = height
	a.barriers++
	a.version++
	return nil
}

// BeginUpdate marks a page as being rendered.
func (a *ShadowAtlas) BeginUpdate(lightID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	index, ok := a.light[lightID]
	if !ok {
		return ErrUnknownLight
	}
	if a.busy[index] {
		return ErrPageBusy
	}
	a.busy[index] = true
	a.inFlight++
	return nil
}

// EndUpdate marks a page as renderable again.
func (a *ShadowAtlas) EndUpdate(lightID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	index, ok := a.light[lightID]
	if !ok {
		return ErrUnknownLight
	}
	if !a.busy[index] {
		return ErrPageBusy
	}
	a.busy[index] = false
	a.inFlight--
	if a.inFlight < 0 {
		a.inFlight = 0
	}
	a.pages[index].Cleared = false
	a.barriers++
	return nil
}

// Rebuild recreates the atlas, refusing while pages are in flight.
//
// Existing lights keep their slot indices. Pages are compacted by region
// (static at the top, cascades next, dynamic at the bottom), the replacement
// table is built from a snapshot while readers keep using the old table, and
// the new table is swapped in once. All page contents are invalidated.
func (a *ShadowAtlas) Rebuild() error {
	const maxAttempts = 16
	for attempt := 0; attempt < maxAttempts; attempt++ {
		snap, ok := a.rebuildSnapshot()
		if !ok {
			return ErrAtlasBusy
		}

		newPages, newUsed := packSnapshot(snap)

		a.mu.Lock()
		if a.inFlight != 0 {
			a.mu.Unlock()
			return ErrAtlasBusy
		}
		if a.version != snap.version {
			a.mu.Unlock()
			continue
		}
		a.installRebuildLocked(newPages, newUsed)
		a.mu.Unlock()
		return nil
	}

	// Under sustained mutation, finish with an exclusive snapshot/swap. This
	// still exposes either the old or the new table to readers, never both.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inFlight != 0 {
		return ErrAtlasBusy
	}
	snap := a.snapshotLocked()
	newPages, newUsed := packSnapshot(snap)
	a.installRebuildLocked(newPages, newUsed)
	return nil
}

// BarrierCount reports how many sampling barriers were inserted.
func (a *ShadowAtlas) BarrierCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.barriers
}

func (a *ShadowAtlas) activePagesLocked() []Page {
	out := make([]Page, 0, len(a.light))
	for i := range a.pages {
		if a.used[i] {
			out = append(out, a.pages[i])
		}
	}
	return out
}

func (a *ShadowAtlas) freeSlotLocked() int {
	for i := range a.used {
		if !a.used[i] {
			return i
		}
	}
	return len(a.pages)
}

type rebuildSnapshot struct {
	width   int
	height  int
	cell    int
	slots   int
	active  []Page
	align   []int
	version uint64
}

func (a *ShadowAtlas) rebuildSnapshot() (rebuildSnapshot, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.inFlight != 0 {
		return rebuildSnapshot{}, false
	}
	return a.snapshotLocked(), true
}

func (a *ShadowAtlas) snapshotLocked() rebuildSnapshot {
	return rebuildSnapshot{
		width:   a.width,
		height:  a.height,
		cell:    a.cell,
		slots:   len(a.pages),
		active:  a.activePagesLocked(),
		align:   append([]int(nil), a.align...),
		version: a.version,
	}
}

func (a *ShadowAtlas) installRebuildLocked(pages []Page, used []bool) {
	a.pages = pages
	a.used = used
	a.busy = make([]bool, len(pages))
	a.inFlight = 0
	a.barriers++
	a.version++
}

func packSnapshot(snap rebuildSnapshot) ([]Page, []bool) {
	if len(snap.active) == 0 {
		pages := make([]Page, snap.slots)
		for i := range pages {
			pages[i] = Page{Index: i, Cleared: true}
		}
		return pages, make([]bool, len(pages))
	}

	ordered := append([]Page(nil), snap.active...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ri, rj := regionRank(ordered[i].Region), regionRank(ordered[j].Region)
		if ri != rj {
			return ri < rj
		}
		if ordered[i].Cascade != ordered[j].Cascade {
			return ordered[i].Cascade < ordered[j].Cascade
		}
		return ordered[i].Index < ordered[j].Index
	})

	size := snap.slots
	for _, p := range snap.active {
		if p.Index+1 > size {
			size = p.Index + 1
		}
	}
	newPages := make([]Page, size)
	newUsed := make([]bool, size)
	for i := range newPages {
		newPages[i] = Page{Index: i, Cleared: true}
	}

	placed := make([]Page, 0, len(ordered))
	for _, old := range ordered {
		align := snap.cell
		if old.Index >= 0 && old.Index < len(snap.align) && snap.align[old.Index] > 0 {
			align = snap.align[old.Index]
		}
		if align <= 0 {
			align = 1
		}
		rect, ok := findRect(snap.width, snap.height, placed, old.Rect.W, old.Rect.H, align, old.Region)
		if !ok {
			// The old placement is known to be valid. Keep it if canonical
			// compaction cannot improve on it.
			return oldPlacement(snap)
		}
		p := old
		p.Rect = rect
		p.Cleared = true
		newPages[p.Index] = p
		newUsed[p.Index] = true
		placed = append(placed, p)
	}
	return newPages, newUsed
}

func oldPlacement(snap rebuildSnapshot) ([]Page, []bool) {
	size := snap.slots
	for _, p := range snap.active {
		if p.Index+1 > size {
			size = p.Index + 1
		}
	}
	pages := make([]Page, size)
	used := make([]bool, size)
	for i := range pages {
		pages[i] = Page{Index: i, Cleared: true}
	}
	for _, p := range snap.active {
		p.Cleared = true
		pages[p.Index] = p
		used[p.Index] = true
	}
	return pages, used
}

func regionRank(r Region) int {
	switch r {
	case RegionStatic:
		return 0
	case RegionCascade:
		return 1
	default:
		return 2
	}
}

func findRect(atlasW, atlasH int, occupied []Page, w, h, align int, region Region) (Rect, bool) {
	if w <= 0 || h <= 0 || align <= 0 || w > atlasW || h > atlasH {
		return Rect{}, false
	}

	maxX := atlasW - w
	maxY := atlasH - h
	if maxX < 0 || maxY < 0 {
		return Rect{}, false
	}

	var staticBottom, dynamicTop int
	haveStatic, haveDynamic := false, false
	for _, p := range occupied {
		switch p.Region {
		case RegionStatic:
			bottom := p.Rect.Y + p.Rect.H
			if !haveStatic || bottom > staticBottom {
				staticBottom = bottom
			}
			haveStatic = true
		case RegionDynamic:
			if !haveDynamic || p.Rect.Y < dynamicTop {
				dynamicTop = p.Rect.Y
			}
			haveDynamic = true
		}
	}

	startY, endY, step := 0, maxY, align
	if region == RegionDynamic {
		startY = (maxY / align) * align
		endY = 0
		step = -align
	}

	for y := startY; ; y += step {
		if y < 0 || y > maxY || (step < 0 && y < endY) {
			break
		}
		if region == RegionStatic && haveDynamic && y+h > dynamicTop {
			continue
		}
		if region == RegionDynamic && haveStatic && y < staticBottom {
			continue
		}
		for x := 0; x <= maxX; x += align {
			r := Rect{X: x, Y: y, W: w, H: h}
			clear := true
			for _, p := range occupied {
				if rectsOverlap(r, p.Rect) {
					clear = false
					break
				}
			}
			if clear {
				return r, true
			}
		}
		if step < 0 && y == 0 {
			break
		}
	}
	return Rect{}, false
}

func rectsOverlap(a, b Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

func roundUp(value, unit int) int {
	if unit <= 1 {
		return value
	}
	return ((value + unit - 1) / unit) * unit
}

func uvRect(r Rect, atlasW, atlasH int) (u0, v0, u1, v1 float64) {
	if atlasW <= 0 || atlasH <= 0 {
		return 0, 0, 0, 0
	}

	insetX := 1
	if r.W <= 2 {
		insetX = 0
	}
	insetY := 1
	if r.H <= 2 {
		insetY = 0
	}

	u0 = clamp01(float64(r.X+insetX) / float64(atlasW))
	v0 = clamp01(float64(r.Y+insetY) / float64(atlasH))
	u1 = clamp01(float64(r.X+r.W-insetX) / float64(atlasW))
	v1 = clamp01(float64(r.Y+r.H-insetY) / float64(atlasH))
	return u0, v0, u1, v1
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
