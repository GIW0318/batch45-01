package task

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

// ErrAtlasFull is returned when every slot is pinned and no glyph can be
// evicted without disturbing a frame that is still on screen.
var ErrAtlasFull = errors.New("glyph atlas is full: every slot is pinned")

const rasterBudgetPerFrame = 3

// GlyphKey identifies one rasterised glyph in the atlas.
type GlyphKey struct {
	Rune   rune
	Size   int
	Weight int
}

// Glyph is one rasterised glyph stored in the atlas. A placeholder has zero
// dimensions until it is filled on a later frame.
type Glyph struct {
	Key      GlyphKey
	Width    int
	Height   int
	Advance  float64
	BearingX float64
	BearingY float64
	Color    bool
}

type atlasSnapshot struct {
	generation int
	glyphs     map[GlyphKey]*Glyph
}

// GlyphAtlas caches rasterised glyphs for the text layer.
type GlyphAtlas struct {
	mu         sync.Mutex
	capacity   int
	glyphs     map[GlyphKey]*Glyph
	pinned     map[GlyphKey]bool
	lastUsed   map[GlyphKey]uint64
	clock      uint64
	pending    []GlyphKey
	pendingSet map[GlyphKey]bool

	publishMu sync.Mutex
	buffers   [2]*atlasSnapshot
	published atomic.Pointer[atlasSnapshot]
}

// NewGlyphAtlas creates an atlas with capacity slots.
func NewGlyphAtlas(capacity int) *GlyphAtlas {
	if capacity < 0 {
		capacity = 0
	}
	initial := &atlasSnapshot{glyphs: make(map[GlyphKey]*Glyph)}
	a := &GlyphAtlas{
		capacity:   capacity,
		glyphs:     make(map[GlyphKey]*Glyph),
		pinned:     make(map[GlyphKey]bool),
		lastUsed:   make(map[GlyphKey]uint64),
		pendingSet: make(map[GlyphKey]bool),
	}
	a.buffers[0] = initial
	a.published.Store(initial)
	return a
}

// KeyFor builds the cache key of a character at a size and weight.
func (a *GlyphAtlas) KeyFor(r rune, size, weight int) GlyphKey {
	return GlyphKey{Rune: r, Size: size, Weight: weight}
}

// RowBytes returns the padded row stride of a glyph bitmap width.
func (a *GlyphAtlas) RowBytes(width int) int {
	if width <= 0 {
		return 0
	}
	return (width + 3) &^ 3
}

// Load rasterises the glyph for key, or returns the cached bitmap.
func (a *GlyphAtlas) Load(key GlyphKey) (*Glyph, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if glyph, ok := a.glyphs[key]; ok {
		a.touchLocked(key)
		return glyph, nil
	}
	if a.capacity == 0 {
		return nil, ErrAtlasFull
	}
	if len(a.glyphs) >= a.capacity {
		victim, ok := a.oldestEvictableLocked()
		if !ok {
			return nil, ErrAtlasFull
		}
		a.removeLocked(victim)
	}

	glyph := a.placeholderLocked(key)
	a.glyphs[key] = glyph
	a.touchLocked(key)
	if !a.pendingSet[key] {
		a.pendingSet[key] = true
		a.pending = append(a.pending, key)
	}
	return glyph, nil
}

// Get returns the cached glyph for key without rasterising.
func (a *GlyphAtlas) Get(key GlyphKey) (*Glyph, bool) {
	snapshot := a.published.Load()
	usePublished := snapshot != nil && snapshot.generation > 0

	a.mu.Lock()
	_, cached := a.glyphs[key]
	if cached {
		a.touchLocked(key)
	}
	a.mu.Unlock()

	if !cached {
		return nil, false
	}
	if usePublished {
		glyph, ok := snapshot.glyphs[key]
		return glyph, ok
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	glyph, ok := a.glyphs[key]
	return glyph, ok
}

// Pin marks a glyph as being displayed so it cannot be evicted.
func (a *GlyphAtlas) Pin(key GlyphKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pinned[key] = true
	if _, ok := a.glyphs[key]; ok {
		a.touchLocked(key)
	}
}

// Unpin clears the visible mark of a glyph.
func (a *GlyphAtlas) Unpin(key GlyphKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pinned, key)
}

// Count returns how many glyph slots are occupied.
func (a *GlyphAtlas) Count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.glyphs)
}

// Coverage returns the fraction of the atlas that is occupied.
func (a *GlyphAtlas) Coverage() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.capacity <= 0 {
		return 0
	}
	coverage := float64(len(a.glyphs)) / float64(a.capacity)
	if coverage > 1 {
		return 1
	}
	return coverage
}

// Cluster splits text into glyph clusters so ligatures stay in one slot.
func (a *GlyphAtlas) Cluster(text string) []string {
	if text == "" {
		return nil
	}

	runes := []rune(text)
	clusters := make([]string, 0, len(runes))
	current := make([]rune, 0, 4)
	prevZWJ := false
	regionalIndicators := 0

	flush := func() {
		if len(current) > 0 {
			clusters = append(clusters, string(current))
			current = current[:0]
		}
		prevZWJ = false
		regionalIndicators = 0
	}

	for i := 0; i < len(runes); {
		r := runes[i]

		if r == 'f' {
			flush()
			if length, ok := ligatureLength(runes[i:]); ok {
				current = append(current, runes[i:i+length]...)
				i += length
				continue
			}
		}

		switch {
		case len(current) == 0:
			current = append(current, r)
			prevZWJ = r == '\u200d'
			if isRegionalIndicator(r) {
				regionalIndicators = 1
			}
		case isClusterExtend(r) || prevZWJ || (isRegionalIndicator(r) && regionalIndicators == 1):
			current = append(current, r)
			prevZWJ = r == '\u200d'
			if isRegionalIndicator(r) {
				regionalIndicators++
			} else if !isClusterExtend(r) {
				regionalIndicators = 0
			}
		default:
			flush()
			current = append(current, r)
			prevZWJ = r == '\u200d'
			if isRegionalIndicator(r) {
				regionalIndicators = 1
			}
		}
		i++
	}
	flush()
	return clusters
}

func ligatureLength(runes []rune) (int, bool) {
	if len(runes) >= 3 && runes[0] == 'f' && runes[1] == 'f' && (runes[2] == 'i' || runes[2] == 'l') {
		return 3, true
	}
	if len(runes) >= 2 && runes[0] == 'f' && (runes[1] == 'i' || runes[1] == 'l' || runes[1] == 'f') {
		return 2, true
	}
	return 0, false
}

func isClusterExtend(r rune) bool {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r) {
		return true
	}
	switch {
	case r == '\u200c' || r == '\u200d':
		return true
	case r == '\u20e3':
		return true
	case r >= 0xfe00 && r <= 0xfe0f:
		return true
	case r >= 0xe0100 && r <= 0xe01ef:
		return true
	case r >= 0x1f3fb && r <= 0x1f3ff:
		return true
	case r >= 0xe0020 && r <= 0xe007f:
		return true
	case r >= 0x1100 && r <= 0x11ff:
		return true
	case r >= 0xd7b0 && r <= 0xd7ff:
		return true
	}
	return false
}

func isRegionalIndicator(r rune) bool {
	return r >= 0x1f1e6 && r <= 0x1f1ff
}

// FallbackChain returns the font fallback chain for a language.
func (a *GlyphAtlas) FallbackChain(lang string) []string {
	primary := strings.ToLower(lang)
	if index := strings.IndexAny(primary, "-_"); index >= 0 {
		primary = primary[:index]
	}

	var chain []string
	switch primary {
	case "zh":
		chain = []string{"NotoSansCJK", "NotoSansSC", "NotoSans", "DejaVuSans"}
	case "ja":
		chain = []string{"NotoSansCJK", "NotoSansJP", "NotoSans", "DejaVuSans"}
	case "ko":
		chain = []string{"NotoSansCJK", "NotoSansKR", "NotoSans", "DejaVuSans"}
	case "ar", "fa", "ur":
		chain = []string{"NotoSansArabic", "NotoSans", "DejaVuSans"}
	case "he":
		chain = []string{"NotoSansHebrew", "NotoSans", "DejaVuSans"}
	case "hi", "mr", "ne":
		chain = []string{"NotoSansDevanagari", "NotoSans", "DejaVuSans"}
	case "th":
		chain = []string{"NotoSansThai", "NotoSans", "DejaVuSans"}
	default:
		chain = []string{"NotoSans", "DejaVuSans"}
	}

	result := make([]string, len(chain))
	copy(result, chain)
	return result
}

// SwapBuffer publishes the updated atlas to the render thread.
func (a *GlyphAtlas) SwapBuffer() error {
	a.publishMu.Lock()
	defer a.publishMu.Unlock()

	a.mu.Lock()
	a.backfillLocked(rasterBudgetPerFrame)
	next := a.nextSnapshotLocked()
	a.mu.Unlock()

	a.storePublished(next)
	return nil
}

// Generation returns the number of published atlas buffers.
func (a *GlyphAtlas) Generation() int {
	snapshot := a.published.Load()
	if snapshot == nil {
		return 0
	}
	return snapshot.generation
}

// Baseline returns the baseline offset used for glyph metrics at a size.
func (a *GlyphAtlas) Baseline(size int) float64 {
	if size <= 0 {
		return 0
	}
	return 0.8 * float64(size)
}

// Resize changes the atlas capacity and invalidates the old layout.
func (a *GlyphAtlas) Resize(capacity int) error {
	if capacity < 0 {
		capacity = 0
	}

	a.publishMu.Lock()
	defer a.publishMu.Unlock()

	a.mu.Lock()
	a.capacity = capacity
	a.glyphs = make(map[GlyphKey]*Glyph)
	a.pinned = make(map[GlyphKey]bool)
	a.lastUsed = make(map[GlyphKey]uint64)
	a.clock = 0
	a.pending = nil
	a.pendingSet = make(map[GlyphKey]bool)
	next := a.nextSnapshotLocked()
	a.mu.Unlock()

	a.storePublished(next)
	return nil
}

// Wrap breaks text into lines that fit maxWidth columns using rune widths.
func (a *GlyphAtlas) Wrap(text string, maxWidth int) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var lines []string
	var line []rune
	lineWidth := 0

	flush := func(force bool) {
		if len(line) == 0 && !force {
			return
		}
		lines = append(lines, string(line))
		line = line[:0]
		lineWidth = 0
	}

	clusters := a.Cluster(text)
	for _, cluster := range clusters {
		if cluster == "\n" || cluster == "\r" || cluster == "\r\n" {
			flush(true)
			continue
		}

		width := 0
		for _, r := range cluster {
			width += a.RuneAdvance(r)
		}

		if maxWidth > 0 && lineWidth > 0 && lineWidth+width > maxWidth {
			flush(false)
		}
		line = append(line, []rune(cluster)...)
		lineWidth += width

		if maxWidth > 0 && lineWidth > maxWidth && len(line) > 0 {
			flush(false)
		}
	}
	flush(false)
	return lines
}

// IsColorRune reports whether the character must be rasterised as colour.
func (a *GlyphAtlas) IsColorRune(r rune) bool {
	switch {
	case r >= 0x1f1e6 && r <= 0x1f1ff:
		return true
	case r >= 0x1f300 && r <= 0x1faff:
		return true
	case r >= 0x2600 && r <= 0x27bf:
		return true
	case r >= 0x2b00 && r <= 0x2bff:
		return true
	case r == 0x203c || r == 0x2049 || r == 0x2122 || r == 0x2139:
		return true
	case r >= 0x2194 && r <= 0x21aa:
		return true
	case r >= 0x231a && r <= 0x23ff:
		return true
	case r >= 0x25aa && r <= 0x25fe:
		return true
	case r >= 0x2934 && r <= 0x2935:
		return true
	case r >= 0x3030 && r <= 0x303d:
		return true
	case r >= 0x3297 && r <= 0x3299:
		return true
	}
	return false
}

// RuneAdvance returns the column width of a rune in the text layout.
func (a *GlyphAtlas) RuneAdvance(r rune) int {
	if isClusterExtend(r) || r == '\u200b' || r == '\r' || r == '\n' {
		return 0
	}
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if a.IsColorRune(r) {
		return 2
	}
	switch {
	case unicode.Is(unicode.Han, r):
		return 2
	case unicode.Is(unicode.Hangul, r):
		return 2
	case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
		return 2
	case unicode.Is(unicode.Bopomofo, r):
		return 2
	case r >= 0x2e80 && r <= 0xa4cf && r != 0x303f:
		return 2
	case r >= 0xac00 && r <= 0xd7a3:
		return 2
	case r >= 0xf900 && r <= 0xfaff:
		return 2
	case r >= 0xfe10 && r <= 0xfe19:
		return 2
	case r >= 0xfe30 && r <= 0xfe6f:
		return 2
	case r >= 0xff00 && r <= 0xff60:
		return 2
	case r >= 0xffe0 && r <= 0xffe6:
		return 2
	case r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

func (a *GlyphAtlas) placeholderLocked(key GlyphKey) *Glyph {
	return &Glyph{
		Key:      key,
		Advance:  a.advanceFor(key),
		BearingY: a.Baseline(key.Size),
		Color:    a.IsColorRune(key.Rune),
	}
}

func (a *GlyphAtlas) rasterizeLocked(key GlyphKey) *Glyph {
	em := float64(key.Size)
	if em < 1 {
		em = 1
	}

	widthScale := 1.0
	switch {
	case key.Weight >= 700:
		widthScale = 1.08
	case key.Weight > 0 && key.Weight <= 300:
		widthScale = 0.94
	}

	columns := a.RuneAdvance(key.Rune)
	color := a.IsColorRune(key.Rune)

	var width, height int
	switch {
	case columns == 0:
		width = int(math.Ceil(em * 0.35))
		height = int(math.Ceil(em * 0.65))
	case columns >= 2 || color:
		width = int(math.Ceil(em * widthScale))
		height = int(math.Ceil(em * 1.2))
	default:
		width = int(math.Ceil(em * 0.55 * widthScale))
		height = int(math.Ceil(em * 1.2))
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	return &Glyph{
		Key:      key,
		Width:    width,
		Height:   height,
		Advance:  a.advanceFor(key),
		BearingX: 0,
		BearingY: a.Baseline(key.Size),
		Color:    color,
	}
}

func (a *GlyphAtlas) advanceFor(key GlyphKey) float64 {
	columns := a.RuneAdvance(key.Rune)
	size := float64(key.Size)
	if size <= 0 {
		size = 1
	}
	switch columns {
	case 0:
		return 0
	case 1:
		return size * 0.5
	default:
		return size
	}
}

func (a *GlyphAtlas) touchLocked(key GlyphKey) {
	a.clock++
	a.lastUsed[key] = a.clock
}

func (a *GlyphAtlas) oldestEvictableLocked() (GlyphKey, bool) {
	var victim GlyphKey
	var oldest uint64
	found := false
	for key := range a.glyphs {
		if a.pinned[key] {
			continue
		}
		used := a.lastUsed[key]
		if !found || used < oldest {
			victim = key
			oldest = used
			found = true
		}
	}
	return victim, found
}

func (a *GlyphAtlas) removeLocked(key GlyphKey) {
	delete(a.glyphs, key)
	delete(a.lastUsed, key)
	delete(a.pendingSet, key)
	if len(a.pending) > 0 {
		compacted := a.pending[:0]
		for _, pendingKey := range a.pending {
			if pendingKey != key {
				compacted = append(compacted, pendingKey)
			}
		}
		a.pending = compacted
	}
}

func (a *GlyphAtlas) backfillLocked(limit int) int {
	filled := 0
	index := 0
	for index < len(a.pending) && filled < limit {
		key := a.pending[index]
		index++
		if !a.pendingSet[key] {
			continue
		}
		delete(a.pendingSet, key)

		glyph, ok := a.glyphs[key]
		if !ok || glyph.Width > 0 {
			continue
		}
		a.glyphs[key] = a.rasterizeLocked(key)
		filled++
	}

	if index > 0 {
		remaining := append([]GlyphKey(nil), a.pending[index:]...)
		a.pending = remaining
	}
	return filled
}

func (a *GlyphAtlas) nextSnapshotLocked() *atlasSnapshot {
	current := a.published.Load()
	generation := 1
	if current != nil {
		generation = current.generation + 1
	}

	glyphs := make(map[GlyphKey]*Glyph, len(a.glyphs))
	for key, glyph := range a.glyphs {
		glyphs[key] = glyph
	}
	return &atlasSnapshot{generation: generation, glyphs: glyphs}
}

func (a *GlyphAtlas) storePublished(snapshot *atlasSnapshot) {
	a.buffers[snapshot.generation%2] = snapshot
	a.published.Store(snapshot)
}
