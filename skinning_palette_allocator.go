package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Mat4 is a column major 4x4 matrix, translation lives at indices 12, 13, 14.
type Mat4 [16]float64

// SkinPalette allocates and updates the bone matrices of many characters in a
// shared palette texture.
type SkinPalette struct {
	texWidth int
	maxBones int
}

// NewSkinPalette returns a palette backed by a texWidth wide texture that may
// hold maxBones bone matrices in total.
func NewSkinPalette(texWidth, maxBones int) *SkinPalette {
	return &SkinPalette{}
}

// TextureWidth returns the palette texture width.
func (p *SkinPalette) TextureWidth() int {
	return 0
}

// Allocate reserves boneCount consecutive slots for a character and returns the
// base slot.
func (p *SkinPalette) Allocate(characterID, boneCount int) (int, error) {
	return 0, errNotImplemented
}

// SlotOffset returns the base slot of a character.
func (p *SkinPalette) SlotOffset(characterID int) (int, error) {
	return 0, errNotImplemented
}

// WriteMatrices uploads the bone matrices of one character.
func (p *SkinPalette) WriteMatrices(characterID int, bones []Mat4) error {
	return errNotImplemented
}

// ReadBack returns the bone matrices currently stored for a character.
func (p *SkinPalette) ReadBack(characterID int, boneCount int) ([]Mat4, error) {
	return nil, errNotImplemented
}

// Transpose returns row major copies of the bone matrices for the GPU layout.
func (p *SkinPalette) Transpose(bones []Mat4) []Mat4 {
	return nil
}

// Barrier makes the latest palette writes visible to the sampling stage.
func (p *SkinPalette) Barrier() error {
	return errNotImplemented
}

// PaletteGeneration returns how often the palette was published.
func (p *SkinPalette) PaletteGeneration() uint64 {
	return 0
}

// RemapBoneIndex maps an authoured bone index to the palette index of a LOD.
func (p *SkinPalette) RemapBoneIndex(characterID, oldIndex, lod int) (int, error) {
	return 0, errNotImplemented
}

// ValidateWeights checks that the skin weights of one vertex sum to one.
func (p *SkinPalette) ValidateWeights(weights []float64) error {
	return errNotImplemented
}

// Release frees the slots of a character.
func (p *SkinPalette) Release(characterID int) error {
	return errNotImplemented
}

// ActiveCharacters returns the number of characters owning palette slots.
func (p *SkinPalette) ActiveCharacters() int {
	return 0
}

// SplitRootMotion separates root motion from the bone matrices.
func (p *SkinPalette) SplitRootMotion(root Mat4, bones []Mat4) (Mat4, []Mat4, error) {
	return Mat4{}, nil, errNotImplemented
}

// RecomputeUV returns the U coordinate step of one palette slot.
func (p *SkinPalette) RecomputeUV(texWidth int) (float64, error) {
	return 0, errNotImplemented
}

// CPUGPUConsistent reports whether the GPU copy matches the CPU copy.
func (p *SkinPalette) CPUGPUConsistent(characterID int) bool {
	return false
}

// WrapSafely rejects a slot that would wrap around the texture width.
func (p *SkinPalette) WrapSafely(slot int) (int, error) {
	return 0, errNotImplemented
}
