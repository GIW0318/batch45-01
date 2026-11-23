package task

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"sync"
)

const (
	// targetTableSize is the maximum number of morph targets tracked for one LOD.
	targetTableSize = 4096
	// maxCacheKeysPerCharacter bounds the per-character expression key cache.
	maxCacheKeysPerCharacter = 256
	// quantizedWeightScale is the range of an int16 morph weight.
	quantizedWeightScale = 32768.0
)

var (
	errNegativeWeight  = errors.New("morph mixer: negative weight")
	errInvalidLOD      = errors.New("morph mixer: invalid LOD")
	errTargetTableFull = errors.New("morph mixer: target table full")
	errTargetNotFound  = errors.New("morph mixer: target not registered for LOD")
)

// MorphTarget is one facial morph target. VertexIndices are LOD specific and
// must be remapped when the renderer switches LOD.
type MorphTarget struct {
	Name          string
	VertexIndices []int
	Weight        float32
}

// BlendChannel is one expression channel sampled in the current frame.
type BlendChannel struct {
	Name   string
	Weight float32
	Order  int
}

// MorphMixer blends facial morph targets for a single character.
type MorphMixer struct {
	CharacterID string

	// weights is a double buffer. The active buffer is the immutable view for
	// the current frame; writers build the other buffer and swap it in.
	mu      sync.RWMutex
	weights [2]map[string]float32
	active  int

	targetsMu sync.RWMutex
	targets   map[int]map[string][]int

	cacheMu    sync.Mutex
	cacheOrder []string
	cacheSet   map[string]struct{}
}

// NewMorphMixer creates a mixer bound to one character.
func NewMorphMixer(characterID string) *MorphMixer {
	return &MorphMixer{CharacterID: characterID}
}

// Normalize clamps negative weights to zero and scales the rest so the sum is 1.
func (m *MorphMixer) Normalize(weights []float32) []float32 {
	out := make([]float32, len(weights))
	var sum float64
	for i, w := range weights {
		if w > 0 {
			out[i] = w
			sum += float64(w)
		}
	}
	if sum == 0 {
		return out
	}
	if math.IsInf(sum, 1) {
		var infinite int
		for _, w := range out {
			if math.IsInf(float64(w), 1) {
				infinite++
			}
		}
		if infinite > 0 {
			share := float32(1) / float32(infinite)
			for i, w := range out {
				if math.IsInf(float64(w), 1) {
					out[i] = share
				}
			}
			return out
		}
	}
	scale := float32(1 / sum)
	for i := range out {
		out[i] *= scale
	}
	return out
}

// BlendOrder returns a deterministic order for expressions blended in one frame.
func (m *MorphMixer) BlendOrder(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// SyncTimeline converts a frame number authored at boneFPS to morphFPS.
func (m *MorphMixer) SyncTimeline(frame int, boneFPS, morphFPS float64) float64 {
	if boneFPS <= 0 || morphFPS <= 0 {
		return 0
	}
	return float64(frame) * morphFPS / boneFPS
}

// Crossfade blends from one expression weight to another with t in [0, 1].
func (m *MorphMixer) Crossfade(from, to float32, t float64) float32 {
	if t <= 0 {
		return from
	}
	if t >= 1 {
		return to
	}
	return from + (to-from)*float32(t)
}

// SetTarget registers the vertex indices of name for the given LOD.
func (m *MorphMixer) SetTarget(lod int, name string, indices []int) error {
	if lod < 0 {
		return errInvalidLOD
	}

	m.targetsMu.Lock()
	defer m.targetsMu.Unlock()

	if m.targets == nil {
		m.targets = make(map[int]map[string][]int)
	}
	table := m.targets[lod]
	if table == nil {
		table = make(map[string][]int, targetTableSize)
		m.targets[lod] = table
	}
	if _, exists := table[name]; !exists && len(table) >= targetTableSize {
		return errTargetTableFull
	}

	remapped := make([]int, len(indices))
	copy(remapped, indices)
	table[name] = remapped
	return nil
}

// TargetForLOD returns the vertex indices of name at the given LOD.
func (m *MorphMixer) TargetForLOD(name string, lod int) ([]int, error) {
	if lod < 0 {
		return nil, errInvalidLOD
	}

	m.targetsMu.RLock()
	defer m.targetsMu.RUnlock()

	table := m.targets[lod]
	indices, ok := table[name]
	if !ok {
		return nil, errTargetNotFound
	}
	out := make([]int, len(indices))
	copy(out, indices)
	return out, nil
}

// SetWeight sets the weight of one expression channel.
func (m *MorphMixer) SetWeight(name string, w float32) error {
	if w < 0 || math.IsNaN(float64(w)) {
		return errNegativeWeight
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.weights[m.prepareWriteLocked()][name] = w
	return nil
}

// Weight returns the current weight of one expression channel.
func (m *MorphMixer) Weight(name string) float32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.weights[m.active][name]
}

// CacheKey returns the cache key for an expression of this character.
func (m *MorphMixer) CacheKey(name string) string {
	key := strconv.Itoa(len(m.CharacterID)) + ":" + m.CharacterID + ":" + name

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheSet == nil {
		m.cacheSet = make(map[string]struct{}, maxCacheKeysPerCharacter)
	}
	if _, ok := m.cacheSet[key]; !ok {
		if len(m.cacheOrder) >= maxCacheKeysPerCharacter {
			oldest := m.cacheOrder[0]
			m.cacheOrder = m.cacheOrder[1:]
			delete(m.cacheSet, oldest)
		}
		m.cacheOrder = append(m.cacheOrder, key)
		m.cacheSet[key] = struct{}{}
	}
	return key
}

// UpdateWeights adds the given deltas to the current weights.
func (m *MorphMixer) UpdateWeights(delta map[string]float32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.weights[m.prepareWriteLocked()]
	for name, d := range delta {
		w := next[name] + d
		if w < 0 || math.IsNaN(float64(w)) {
			w = 0
		}
		next[name] = w
	}
}

// Snapshot returns a copy of the current weight table.
func (m *MorphMixer) Snapshot() map[string]float32 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	current := m.weights[m.active]
	out := make(map[string]float32, len(current))
	for name, w := range current {
		out[name] = w
	}
	return out
}

// OnFinished resets an expression after its clip finished playing.
func (m *MorphMixer) OnFinished(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.weights[m.prepareWriteLocked()][name] = 0
}

// IsActive reports whether name currently contributes to the pose.
func (m *MorphMixer) IsActive(name string) bool {
	return m.Weight(name) > 0
}

// DecompressWeight expands a quantized morph delta back to a float weight.
func (m *MorphMixer) DecompressWeight(q int16, scale float32) float32 {
	return float32(q) / quantizedWeightScale * scale
}

// LipSync returns the morph clock value that keeps the mouth on the audio clock.
func (m *MorphMixer) LipSync(morphTime, audioTime float64) float64 {
	return audioTime
}

// PassWeights returns the weights used by a render pass ("color", "shadow", ...).
func (m *MorphMixer) PassWeights(pass string) map[string]float32 {
	return m.Snapshot()
}

// prepareWriteLocked copies the active view into the inactive buffer and makes
// that buffer the active one. The caller must hold m.mu for writing.
func (m *MorphMixer) prepareWriteLocked() int {
	for i := range m.weights {
		if m.weights[i] == nil {
			m.weights[i] = make(map[string]float32)
		}
	}

	next := 1 - m.active
	dst := m.weights[next]
	clear(dst)
	for name, w := range m.weights[m.active] {
		dst[name] = w
	}
	m.active = next
	return next
}
