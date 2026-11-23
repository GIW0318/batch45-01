package task

import "errors"

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
type ShadowAtlas struct{}

// NewShadowAtlas builds an atlas of the given size and page cell.
func NewShadowAtlas(width, height, cell int) (*ShadowAtlas, error) {
	return nil, ErrNotImplemented
}

// Width reports the atlas width.
func (a *ShadowAtlas) Width() int { return 0 }

// Height reports the atlas height.
func (a *ShadowAtlas) Height() int { return 0 }

// Allocate reserves one aligned page for a light.
func (a *ShadowAtlas) Allocate(lightID string, size, align int) (Page, error) {
	return Page{}, ErrNotImplemented
}

// AllocateIn reserves one aligned page in the given region.
func (a *ShadowAtlas) AllocateIn(lightID string, size, align int, region Region) (Page, error) {
	return Page{}, ErrNotImplemented
}

// AllocateCascade reserves one page for a cascade level.
func (a *ShadowAtlas) AllocateCascade(lightID string, size, align, cascade int) (Page, error) {
	return Page{}, ErrNotImplemented
}

// Release frees the page of a light and clears its depth content.
func (a *ShadowAtlas) Release(lightID string) error { return ErrNotImplemented }

// PageOf returns the page of a light.
func (a *ShadowAtlas) PageOf(lightID string) (Page, bool) { return Page{}, false }

// PageAt returns the page stored in a slot index.
func (a *ShadowAtlas) PageAt(index int) (Page, bool) { return Page{}, false }

// Pages returns all allocated pages.
func (a *ShadowAtlas) Pages() []Page { return nil }

// FreePages reports how many pages are still available.
func (a *ShadowAtlas) FreePages() int { return 0 }

// Overflow reports how many allocations failed because the atlas was full.
func (a *ShadowAtlas) Overflow() int { return 0 }

// UVRect returns the UV rectangle of a light page with border padding.
func (a *ShadowAtlas) UVRect(lightID string) (u0, v0, u1, v1 float64, err error) {
	return 0, 0, 0, 0, ErrNotImplemented
}

// Resize changes the atlas size and recomputes all UVs.
func (a *ShadowAtlas) Resize(width, height int) error { return ErrNotImplemented }

// BeginUpdate marks a page as being rendered.
func (a *ShadowAtlas) BeginUpdate(lightID string) error { return ErrNotImplemented }

// EndUpdate marks a page as renderable again.
func (a *ShadowAtlas) EndUpdate(lightID string) error { return ErrNotImplemented }

// Rebuild recreates the atlas, refusing while pages are in flight.
func (a *ShadowAtlas) Rebuild() error { return ErrNotImplemented }

// BarrierCount reports how many sampling barriers were inserted.
func (a *ShadowAtlas) BarrierCount() int { return 0 }
