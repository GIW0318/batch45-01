package task

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// Mat4 is a column major 4x4 matrix, translation lives at indices 12, 13, 14.
type Mat4 [16]float64

const weightEpsilon = 1e-6

// extent is a half-open range of palette slots.
type extent struct {
	start int
	end   int
}

// character holds the CPU-side source of truth for one character's palette
// region. The region always contains one extra guard slot after the bone rows.
type character struct {
	id        int
	base      int
	boneCount int
	cpu       []Mat4
	lodMaps   map[int]map[int]int
	dirty     bool
	published bool
}

// SkinPalette allocates and updates the bone matrices of many characters in a
// shared palette texture.
type SkinPalette struct {
	mu         sync.RWMutex
	texWidth   int
	maxBones   int
	usedBones  int
	chars      map[int]*character
	free       []extent
	gpu        map[int]Mat4
	generation atomic.Uint64
}

// NewSkinPalette returns a palette backed by a texWidth wide texture that may
// hold maxBones bone matrices in total. Every character also owns one guard row
// so an off-by-one sample stays inside that character's palette region.
func NewSkinPalette(texWidth, maxBones int) *SkinPalette {
	p := &SkinPalette{
		texWidth: texWidth,
		maxBones: maxBones,
		chars:    make(map[int]*character),
		gpu:      make(map[int]Mat4),
	}
	if maxBones > 0 {
		p.free = []extent{{start: 0, end: maxSlots(texWidth, maxBones)}}
	}
	return p
}

// maxSlots bounds the physical slot space. There is one guard row per
// character and at most maxBones characters (each needs at least one bone).
// A row-boundary jump can waste less than one texture row per character, so
// this leaves enough physical room to use the whole advertised bone budget.
func maxSlots(texWidth, maxBones int) int {
	const maxInt = int(^uint(0) >> 1)
	if texWidth <= 0 || maxBones <= 0 {
		return 0
	}
	if texWidth > maxInt-2 {
		return maxInt
	}
	factor := texWidth + 2
	if maxBones > maxInt/factor {
		return maxInt
	}
	return maxBones * factor
}

// TextureWidth returns the palette texture width.
func (p *SkinPalette) TextureWidth() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.texWidth
}

// Allocate reserves boneCount consecutive slots for a character and returns the
// base slot. The allocation is rejected if the bone range would cross a texture
// row boundary, because that is exactly the out-of-range wrap that corrupts the
// neighbouring character.
func (p *SkinPalette) Allocate(characterID, boneCount int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.texWidth <= 0 {
		return 0, errors.New("palette texture width must be positive")
	}
	if p.maxBones <= 0 {
		return 0, errors.New("palette bone capacity must be positive")
	}
	if boneCount <= 0 {
		return 0, fmt.Errorf("bone count %d must be positive", boneCount)
	}
	if boneCount > p.maxBones {
		return 0, fmt.Errorf("bone count %d exceeds palette capacity %d", boneCount, p.maxBones)
	}
	if boneCount > p.texWidth {
		return 0, fmt.Errorf("bone count %d is wider than texture width %d", boneCount, p.texWidth)
	}
	if _, exists := p.chars[characterID]; exists {
		return 0, fmt.Errorf("character %d already owns palette slots", characterID)
	}
	if boneCount > p.maxBones-p.usedBones {
		return 0, fmt.Errorf("palette has %d free bones, character needs %d", p.maxBones-p.usedBones, boneCount)
	}

	// One extra slot is the isolation row immediately after the bone rows.
	size := boneCount + 1
	if size <= boneCount {
		return 0, errors.New("palette allocation size overflow")
	}

	base, freeIndex, ok := p.findSlotLocked(size, boneCount)
	if !ok {
		return 0, errors.New("palette does not have a contiguous slot range for this character")
	}
	p.takeSlotLocked(freeIndex, base, size)

	p.chars[characterID] = &character{
		id:        characterID,
		base:      base,
		boneCount: boneCount,
		cpu:       make([]Mat4, boneCount),
		lodMaps:   make(map[int]map[int]int),
		dirty:     true,
	}
	p.usedBones += boneCount
	return base, nil
}

// findSlotLocked returns the first free position whose bone rows stay within a
// single texture row.
func (p *SkinPalette) findSlotLocked(size, boneCount int) (int, int, bool) {
	for i, e := range p.free {
		if e.end-e.start < size {
			continue
		}
		for pos := e.start; pos+size <= e.end; {
			column := pos % p.texWidth
			if column+boneCount <= p.texWidth {
				return pos, i, true
			}
			// Positions later in this row can only get closer to the edge,
			// so continue at the first column of the next texture row.
			step := p.texWidth - column
			if step <= 0 {
				step = 1
			}
			pos += step
		}
	}
	return 0, 0, false
}

// takeSlotLocked removes [start, start+size) from the free extent at index i.
func (p *SkinPalette) takeSlotLocked(i, start, size int) {
	e := p.free[i]
	end := start + size

	replacement := make([]extent, 0, 2)
	if start > e.start {
		replacement = append(replacement, extent{start: e.start, end: start})
	}
	if end < e.end {
		replacement = append(replacement, extent{start: end, end: e.end})
	}

	next := make([]extent, 0, len(p.free)-1+len(replacement))
	next = append(next, p.free[:i]...)
	next = append(next, replacement...)
	next = append(next, p.free[i+1:]...)
	p.free = next
}

// addFreeLocked inserts a released range and merges adjacent free ranges.
func (p *SkinPalette) addFreeLocked(start, size int) {
	if size <= 0 {
		return
	}
	p.free = append(p.free, extent{start: start, end: start + size})
	sort.Slice(p.free, func(i, j int) bool {
		return p.free[i].start < p.free[j].start
	})

	merged := p.free[:0]
	for _, e := range p.free {
		n := len(merged)
		if n > 0 && merged[n-1].end >= e.start {
			if e.end > merged[n-1].end {
				merged[n-1].end = e.end
			}
			continue
		}
		merged = append(merged, e)
	}
	p.free = merged
}

// SlotOffset returns the base slot of a character.
func (p *SkinPalette) SlotOffset(characterID int) (int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ch, ok := p.chars[characterID]
	if !ok {
		return 0, fmt.Errorf("character %d has no palette slots", characterID)
	}
	return ch.base, nil
}

// WriteMatrices uploads the bone matrices of one character. The matrices stay
// on the CPU side until Barrier publishes them to the sampling copy.
func (p *SkinPalette) WriteMatrices(characterID int, bones []Mat4) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, ok := p.chars[characterID]
	if !ok {
		return fmt.Errorf("character %d has no palette slots", characterID)
	}
	if len(bones) != ch.boneCount {
		return fmt.Errorf("character %d owns %d bones, got %d matrices", characterID, ch.boneCount, len(bones))
	}
	copy(ch.cpu, bones)
	ch.dirty = true
	return nil
}

// ReadBack returns the bone matrices currently stored for a character. The
// result is a copy, so callers cannot mutate the palette without WriteMatrices.
func (p *SkinPalette) ReadBack(characterID int, boneCount int) ([]Mat4, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ch, ok := p.chars[characterID]
	if !ok {
		return nil, fmt.Errorf("character %d has no palette slots", characterID)
	}
	if boneCount < 0 || boneCount > ch.boneCount {
		return nil, fmt.Errorf("read of %d bones is outside character %d's %d bone range", boneCount, characterID, ch.boneCount)
	}
	out := make([]Mat4, boneCount)
	copy(out, ch.cpu[:boneCount])
	return out, nil
}

// Transpose returns row major copies of the bone matrices for the GPU layout.
func (p *SkinPalette) Transpose(bones []Mat4) []Mat4 {
	return transposeMatrices(bones)
}

func transposeMatrices(bones []Mat4) []Mat4 {
	out := make([]Mat4, len(bones))
	for i, m := range bones {
		var t Mat4
		for row := 0; row < 4; row++ {
			for col := 0; col < 4; col++ {
				t[row*4+col] = m[col*4+row]
			}
		}
		out[i] = t
	}
	return out
}

func transposeMatrix(m Mat4) Mat4 {
	var t Mat4
	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			t[row*4+col] = m[col*4+row]
		}
	}
	return t
}

// Barrier makes the latest palette writes visible to the sampling stage. Only
// this publish step advances PaletteGeneration.
func (p *SkinPalette) Barrier() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, ch := range p.chars {
		if !ch.dirty {
			continue
		}
		gpuBones := transposeMatrices(ch.cpu)
		for i, m := range gpuBones {
			p.gpu[ch.base+i] = m
		}
		// The isolation row mirrors the last bone. An off-by-one sample can
		// only read this character's own matrix, never the next character.
		p.gpu[ch.base+len(gpuBones)] = gpuBones[len(gpuBones)-1]
		ch.published = true
		ch.dirty = false
	}
	p.generation.Add(1)
	return nil
}

// PaletteGeneration returns how often the palette was published.
func (p *SkinPalette) PaletteGeneration() uint64 {
	return p.generation.Load()
}

// RemapBoneIndex maps an authoured bone index to the palette index of a LOD.
// The mapping is cached per (character, LOD, index), which makes LOD switches
// idempotent and stable.
func (p *SkinPalette) RemapBoneIndex(characterID, oldIndex, lod int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, ok := p.chars[characterID]
	if !ok {
		return 0, fmt.Errorf("character %d has no palette slots", characterID)
	}
	if lod < 0 {
		return 0, fmt.Errorf("LOD %d must not be negative", lod)
	}
	if oldIndex < 0 || oldIndex >= ch.boneCount {
		return 0, fmt.Errorf("bone index %d is outside character %d's %d bone range", oldIndex, characterID, ch.boneCount)
	}

	mapping := ch.lodMaps[lod]
	if mapping == nil {
		mapping = make(map[int]int)
		ch.lodMaps[lod] = mapping
	}
	if remapped, ok := mapping[oldIndex]; ok {
		return remapped, nil
	}

	// With no separate LOD skeleton description supplied, authored indices are
	// already in palette order for every LOD.
	remapped := oldIndex
	mapping[oldIndex] = remapped
	return remapped, nil
}

// ValidateWeights checks that the skin weights of one vertex sum to one.
func (p *SkinPalette) ValidateWeights(weights []float64) error {
	if len(weights) == 0 {
		return errors.New("skin weights must not be empty")
	}

	sum := 0.0
	for i, w := range weights {
		if math.IsNaN(w) || math.IsInf(w, 0) {
			return fmt.Errorf("skin weight %d is not finite", i)
		}
		if w < 0 {
			return fmt.Errorf("skin weight %d is negative: %v", i, w)
		}
		sum += w
	}
	if math.Abs(sum-1.0) > weightEpsilon {
		return fmt.Errorf("skin weights sum to %v, want 1", sum)
	}
	return nil
}

// Release frees the slots of a character.
func (p *SkinPalette) Release(characterID int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, ok := p.chars[characterID]
	if !ok {
		return fmt.Errorf("character %d has no palette slots", characterID)
	}

	delete(p.chars, characterID)
	p.usedBones -= ch.boneCount

	size := ch.boneCount + 1
	for slot := ch.base; slot < ch.base+size; slot++ {
		delete(p.gpu, slot)
	}
	p.addFreeLocked(ch.base, size)
	return nil
}

// ActiveCharacters returns the number of characters owning palette slots.
func (p *SkinPalette) ActiveCharacters() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.chars)
}

// SplitRootMotion separates root motion from the bone matrices.
func (p *SkinPalette) SplitRootMotion(root Mat4, bones []Mat4) (Mat4, []Mat4, error) {
	out := make([]Mat4, len(bones))
	for i, bone := range bones {
		local := bone
		local[12] -= root[12]
		local[13] -= root[13]
		local[14] -= root[14]
		out[i] = local
	}
	return root, out, nil
}

// RecomputeUV returns the U coordinate step of one palette slot. A width change
// also updates the stored texture width used by later allocations.
func (p *SkinPalette) RecomputeUV(texWidth int) (float64, error) {
	if texWidth <= 0 {
		return 0, fmt.Errorf("palette texture width %d must be positive", texWidth)
	}

	p.mu.Lock()
	p.texWidth = texWidth
	p.mu.Unlock()
	return 1.0 / float64(texWidth), nil
}

// CPUGPUConsistent reports whether the GPU copy matches the CPU copy.
func (p *SkinPalette) CPUGPUConsistent(characterID int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ch, ok := p.chars[characterID]
	if !ok || ch.dirty || !ch.published || len(ch.cpu) == 0 {
		return false
	}
	for i, cpuMatrix := range ch.cpu {
		gpuMatrix, ok := p.gpu[ch.base+i]
		if !ok || gpuMatrix != transposeMatrix(cpuMatrix) {
			return false
		}
	}
	guard, ok := p.gpu[ch.base+len(ch.cpu)]
	if !ok {
		return false
	}
	return guard == transposeMatrix(ch.cpu[len(ch.cpu)-1])
}

// WrapSafely rejects a slot that would wrap around the texture width.
func (p *SkinPalette) WrapSafely(slot int) (int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.texWidth <= 0 {
		return 0, errors.New("palette texture width must be positive")
	}
	if slot < 0 || slot >= p.texWidth {
		return 0, fmt.Errorf("slot %d wraps a %d wide palette", slot, p.texWidth)
	}
	return slot, nil
}
