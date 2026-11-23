package task

import "errors"

var errNotImplemented = errors.New("not implemented")

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
}

// NewMorphMixer creates a mixer bound to one character.
func NewMorphMixer(characterID string) *MorphMixer {
	return &MorphMixer{CharacterID: characterID}
}

// Normalize clamps negative weights to zero and scales the rest so the sum is 1.
func (m *MorphMixer) Normalize(weights []float32) []float32 {
	return nil
}

// BlendOrder returns a deterministic order for expressions blended in one frame.
func (m *MorphMixer) BlendOrder(names []string) []string {
	return nil
}

// SyncTimeline converts a frame number authored at boneFPS to morphFPS.
func (m *MorphMixer) SyncTimeline(frame int, boneFPS, morphFPS float64) float64 {
	return 0
}

// Crossfade blends from one expression weight to another with t in [0, 1].
func (m *MorphMixer) Crossfade(from, to float32, t float64) float32 {
	return 0
}

// SetTarget registers the vertex indices of name for the given LOD.
func (m *MorphMixer) SetTarget(lod int, name string, indices []int) error {
	return errNotImplemented
}

// TargetForLOD returns the vertex indices of name at the given LOD.
func (m *MorphMixer) TargetForLOD(name string, lod int) ([]int, error) {
	return nil, errNotImplemented
}

// SetWeight sets the weight of one expression channel.
func (m *MorphMixer) SetWeight(name string, w float32) error {
	return errNotImplemented
}

// Weight returns the current weight of one expression channel.
func (m *MorphMixer) Weight(name string) float32 {
	return 0
}

// CacheKey returns the cache key for an expression of this character.
func (m *MorphMixer) CacheKey(name string) string {
	return ""
}

// UpdateWeights adds the given deltas to the current weights.
func (m *MorphMixer) UpdateWeights(delta map[string]float32) {
}

// Snapshot returns a copy of the current weight table.
func (m *MorphMixer) Snapshot() map[string]float32 {
	return nil
}

// OnFinished resets an expression after its clip finished playing.
func (m *MorphMixer) OnFinished(name string) {
}

// IsActive reports whether name currently contributes to the pose.
func (m *MorphMixer) IsActive(name string) bool {
	return false
}

// DecompressWeight expands a quantized morph delta back to a float weight.
func (m *MorphMixer) DecompressWeight(q int16, scale float32) float32 {
	return 0
}

// LipSync returns the morph clock value that keeps the mouth on the audio clock.
func (m *MorphMixer) LipSync(morphTime, audioTime float64) float64 {
	return 0
}

// PassWeights returns the weights used by a render pass ("color", "shadow", ...).
func (m *MorphMixer) PassWeights(pass string) map[string]float32 {
	return nil
}
