package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// GlyphKey identifies one rasterised glyph in the atlas.
type GlyphKey struct {
	Rune   rune
	Size   int
	Weight int
}

// Glyph is one rasterised glyph stored in the atlas.
type Glyph struct {
	Key      GlyphKey
	Width    int
	Height   int
	Advance  float64
	BearingX float64
	BearingY float64
	Color    bool
}

// GlyphAtlas caches rasterised glyphs for the text layer.
type GlyphAtlas struct{}

// NewGlyphAtlas creates an atlas with capacity slots.
func NewGlyphAtlas(capacity int) *GlyphAtlas {
	return &GlyphAtlas{}
}

// KeyFor builds the cache key of a character at a size and weight.
func (a *GlyphAtlas) KeyFor(r rune, size, weight int) GlyphKey {
	return GlyphKey{}
}

// RowBytes returns the padded row stride of a glyph bitmap width.
func (a *GlyphAtlas) RowBytes(width int) int {
	return 0
}

// Load rasterises the glyph for key, or returns the cached bitmap.
func (a *GlyphAtlas) Load(key GlyphKey) (*Glyph, error) {
	return nil, errNotImplemented
}

// Get returns the cached glyph for key without rasterising.
func (a *GlyphAtlas) Get(key GlyphKey) (*Glyph, bool) {
	return nil, false
}

// Pin marks a glyph as being displayed so it cannot be evicted.
func (a *GlyphAtlas) Pin(key GlyphKey) {
}

// Unpin clears the visible mark of a glyph.
func (a *GlyphAtlas) Unpin(key GlyphKey) {
}

// Count returns how many glyph slots are occupied.
func (a *GlyphAtlas) Count() int {
	return 0
}

// Coverage returns the fraction of the atlas that is occupied.
func (a *GlyphAtlas) Coverage() float64 {
	return 0
}

// Cluster splits text into glyph clusters so ligatures stay in one slot.
func (a *GlyphAtlas) Cluster(text string) []string {
	return nil
}

// FallbackChain returns the font fallback chain for a language.
func (a *GlyphAtlas) FallbackChain(lang string) []string {
	return nil
}

// SwapBuffer publishes the updated atlas to the render thread.
func (a *GlyphAtlas) SwapBuffer() error {
	return errNotImplemented
}

// Generation returns the number of published atlas buffers.
func (a *GlyphAtlas) Generation() int {
	return 0
}

// Baseline returns the baseline offset used for glyph metrics at a size.
func (a *GlyphAtlas) Baseline(size int) float64 {
	return 0
}

// Resize changes the atlas capacity and invalidates the old layout.
func (a *GlyphAtlas) Resize(capacity int) error {
	return errNotImplemented
}

// Wrap breaks text into lines that fit maxWidth columns using rune widths.
func (a *GlyphAtlas) Wrap(text string, maxWidth int) []string {
	return nil
}

// IsColorRune reports whether the character must be rasterised as colour.
func (a *GlyphAtlas) IsColorRune(r rune) bool {
	return false
}

// RuneAdvance returns the column width of a rune in the text layout.
func (a *GlyphAtlas) RuneAdvance(r rune) int {
	return 0
}
