package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// RGB is a colour in the working colour space.
type RGB struct {
	R float64
	G float64
	B float64
}

// Lut3D is a cubic colour lookup table, index = r + g*Size + b*Size*Size.
type Lut3D struct {
	Size int
	Data []RGB
}

// LutSampler samples and updates 3D colour LUTs.
type LutSampler struct {
	size int
}

// NewLutSampler returns a sampler for LUTs of the given edge size.
func NewLutSampler(size int) *LutSampler {
	return &LutSampler{}
}

// Size returns the LUT edge size the sampler was built for.
func (s *LutSampler) Size() int {
	return 0
}

// Sample evaluates the LUT with trilinear interpolation inside a single cell.
func (s *LutSampler) Sample(lut Lut3D, c RGB) (RGB, error) {
	return RGB{}, errNotImplemented
}

// Update replaces the LUT contents atomically.
func (s *LutSampler) Update(lut *Lut3D, data []RGB) error {
	return errNotImplemented
}

// Version returns the version of the last completed update.
func (s *LutSampler) Version() uint64 {
	return 0
}

// SRGBToLinear converts one sRGB encoded colour into linear space.
func (s *LutSampler) SRGBToLinear(c RGB) RGB {
	return RGB{}
}

// SpaceIsLinear reports whether the LUT is applied in linear space.
func (s *LutSampler) SpaceIsLinear() bool {
	return false
}

// SetScene binds the sampler to one scene.
func (s *LutSampler) SetScene(scene int) error {
	return errNotImplemented
}

// SceneID returns the scene currently bound to the sampler.
func (s *LutSampler) SceneID() int {
	return -1
}

// SceneGeneration returns how often the scene changed.
func (s *LutSampler) SceneGeneration() uint64 {
	return 0
}

// PreviewMatchesFinal reports whether the preview uses the same sampling as the
// final frame.
func (s *LutSampler) PreviewMatchesFinal() bool {
	return false
}

// AdvanceAnimation advances the LUT animation by dt seconds.
func (s *LutSampler) AdvanceAnimation(dt float64) error {
	return errNotImplemented
}

// AnimationTime returns the accumulated LUT animation time in seconds.
func (s *LutSampler) AnimationTime() float64 {
	return 0
}

// AllocatePage reserves an atlas page for one LUT and returns its index.
func (s *LutSampler) AllocatePage(w, h int) (int, error) {
	return 0, errNotImplemented
}

// ExportQuantized exports the LUT with the given bit depth per channel.
func (s *LutSampler) ExportQuantized(lut Lut3D, bits int) ([]uint16, error) {
	return nil, errNotImplemented
}

// Lock takes the LUT update lock.
func (s *LutSampler) Lock() error {
	return errNotImplemented
}

// Unlock releases the LUT update lock.
func (s *LutSampler) Unlock() error {
	return errNotImplemented
}

// AppliesBeforeUI reports whether the LUT is applied before the UI pass.
func (s *LutSampler) AppliesBeforeUI() bool {
	return false
}
