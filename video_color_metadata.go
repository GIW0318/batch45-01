package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// MasteringDisplay is the HDR mastering display metadata of a stream.
type MasteringDisplay struct {
	Primaries    string
	MaxLuminance float64
	MinLuminance float64
}

// ContentLight carries the content light level metadata of a stream.
type ContentLight struct {
	MaxCLL  int
	MaxFALL int
}

// ColorMetadata carries and converts the colour metadata of a transcode.
type ColorMetadata struct{}

// NewColorMetadata creates an empty colour metadata holder.
func NewColorMetadata() *ColorMetadata {
	return &ColorMetadata{}
}

// AttachMastering stores the mastering display metadata.
func (c *ColorMetadata) AttachMastering(md MasteringDisplay) error {
	return errNotImplemented
}

// Mastering returns the stored mastering display metadata.
func (c *ColorMetadata) Mastering() (MasteringDisplay, bool) {
	return MasteringDisplay{}, false
}

// AttachContentLight stores the content light level metadata.
func (c *ColorMetadata) AttachContentLight(cl ContentLight) error {
	return errNotImplemented
}

// ContentLightLevels returns the stored content light metadata.
func (c *ColorMetadata) ContentLightLevels() (ContentLight, bool) {
	return ContentLight{}, false
}

// PrimariesConversion names the conversion applied between two primaries sets.
func (c *ColorMetadata) PrimariesConversion(from, to string) (string, error) {
	return "", errNotImplemented
}

// TonemapCurve maps an HDR luminance to SDR using the content aware curve.
func (c *ColorMetadata) TonemapCurve(input, peak float64) float64 {
	return 0
}

// ChromaLocation returns the chroma sample location tag to write.
func (c *ColorMetadata) ChromaLocation(loc string) string {
	return ""
}

// ColorRangeTag returns the colour range tag to write.
func (c *ColorMetadata) ColorRangeTag(full bool) string {
	return ""
}

// ProcessedTracks returns the indices of every video track that was processed.
func (c *ColorMetadata) ProcessedTracks(n int) []int {
	return nil
}

// MuxMetadata copies all colour metadata keys into the container.
func (c *ColorMetadata) MuxMetadata(md map[string]string) map[string]string {
	return nil
}

// FilterOrder returns the order in which the filters run.
func (c *ColorMetadata) FilterOrder() []string {
	return nil
}

// Dither10to8 converts a 10 bit sample to 8 bit with dithering.
func (c *ColorMetadata) Dither10to8(v uint16, seed uint32) uint8 {
	return 0
}

// EncoderTag returns the colour tag written for an encoder and primaries set.
func (c *ColorMetadata) EncoderTag(encoder, primaries string) string {
	return ""
}

// CommitOutput publishes the output file only on success.
func (c *ColorMetadata) CommitOutput(ok bool, path string) (string, error) {
	return "", errNotImplemented
}

// ThumbnailPrimaries returns the primaries tag used for generated thumbnails.
func (c *ColorMetadata) ThumbnailPrimaries(src string) string {
	return ""
}
