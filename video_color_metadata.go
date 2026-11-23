package task

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var errTranscodeFailed = errors.New("transcode failed")

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
//
// Metadata is guarded by a single lock so a setter can never expose a
// half-written mastering display or content-light value. CacheKey takes the
// same lock for both values, which gives it one complete metadata snapshot.
type ColorMetadata struct {
	mu              sync.RWMutex
	mastering       MasteringDisplay
	hasMastering    bool
	contentLight    ContentLight
	hasContentLight bool
}

// NewColorMetadata creates an empty colour metadata holder.
func NewColorMetadata() *ColorMetadata {
	return &ColorMetadata{}
}

// AttachMastering stores the mastering display metadata.
func (c *ColorMetadata) AttachMastering(md MasteringDisplay) error {
	if strings.TrimSpace(md.Primaries) == "" {
		return errors.New("mastering display primaries are empty")
	}
	if !finite(md.MaxLuminance) || md.MaxLuminance <= 0 {
		return errors.New("mastering display max luminance must be positive and finite")
	}
	if !finite(md.MinLuminance) || md.MinLuminance < 0 || md.MinLuminance > md.MaxLuminance {
		return errors.New("mastering display min luminance is out of range")
	}

	c.mu.Lock()
	c.mastering = md
	c.hasMastering = true
	c.mu.Unlock()
	return nil
}

// Mastering returns the stored mastering display metadata.
func (c *ColorMetadata) Mastering() (MasteringDisplay, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mastering, c.hasMastering
}

// AttachContentLight stores the content light level metadata.
func (c *ColorMetadata) AttachContentLight(cl ContentLight) error {
	if cl.MaxCLL < 0 || cl.MaxFALL < 0 {
		return errors.New("content light levels must not be negative")
	}

	c.mu.Lock()
	c.contentLight = cl
	c.hasContentLight = true
	c.mu.Unlock()
	return nil
}

// ContentLightLevels returns the stored content light metadata.
func (c *ColorMetadata) ContentLightLevels() (ContentLight, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contentLight, c.hasContentLight
}

// PrimariesConversion names the conversion applied between two primaries sets.
func (c *ColorMetadata) PrimariesConversion(from, to string) (string, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return "", errors.New("both source and target primaries are required")
	}
	return from + "-to-" + to, nil
}

// TonemapCurve maps an HDR luminance to SDR using the content aware curve.
//
// The curve is normalised against the actual content peak and uses a
// square-root shoulder. It maps 0 nits to 0, the content peak to 100, and is
// monotonically increasing in between.
func (c *ColorMetadata) TonemapCurve(input, peak float64) float64 {
	if math.IsNaN(input) || math.IsNaN(peak) || peak <= 0 || input <= 0 {
		return 0
	}
	if math.IsInf(input, 0) || input >= peak {
		return 100
	}
	if math.IsInf(peak, 0) {
		return 0
	}

	mapped := math.Sqrt(input/peak) * 100
	if math.IsNaN(mapped) || mapped < 0 {
		return 0
	}
	if mapped > 100 {
		return 100
	}
	return mapped
}

// ChromaLocation returns the chroma sample location tag to write.
func (c *ColorMetadata) ChromaLocation(loc string) string {
	return loc
}

// ColorRangeTag returns the colour range tag to write.
func (c *ColorMetadata) ColorRangeTag(full bool) string {
	if full {
		return "full"
	}
	return "limited"
}

// ProcessedTracks returns the indices of every video track that was processed.
func (c *ColorMetadata) ProcessedTracks(n int) []int {
	if n <= 0 {
		return []int{}
	}
	tracks := make([]int, n)
	for i := range tracks {
		tracks[i] = i
	}
	return tracks
}

// MuxMetadata copies all colour metadata keys into the container.
func (c *ColorMetadata) MuxMetadata(md map[string]string) map[string]string {
	out := make(map[string]string, len(md))
	for key, value := range md {
		out[key] = value
	}
	return out
}

// FilterOrder returns the order in which the filters run.
func (c *ColorMetadata) FilterOrder() []string {
	return []string{"colorspace", "tonemap", "dither"}
}

// Dither10to8 converts a 10 bit sample to 8 bit with dithering.
//
// Scaling is rounded in the 10-bit domain and a deterministic hash supplies
// one LSB of noise. The maximum 10-bit code is pinned to 255 so highlights
// never wrap. The routine uses only integer arithmetic and allocates nothing.
func (c *ColorMetadata) Dither10to8(v uint16, seed uint32) uint8 {
	const max10 = uint32(1023)
	if uint32(v) > max10 {
		v = uint16(max10)
	}
	if v == 0 {
		return 0
	}
	if v == uint16(max10) {
		return 255
	}

	x := uint32(v)
	scaled := (x*255 + max10/2) / max10
	noise := ditherNoise(v, seed)
	value := int(scaled) + noise
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value)
}

// EncoderTag returns the colour tag written for an encoder and primaries set.
func (c *ColorMetadata) EncoderTag(encoder, primaries string) string {
	encoder = strings.ToLower(strings.TrimSpace(encoder))
	primaries = strings.ToLower(strings.TrimSpace(primaries))
	if encoder == "" || primaries == "" {
		return ""
	}
	return encoder + "/" + primaries
}

// CommitOutput publishes the output file only on success.
func (c *ColorMetadata) CommitOutput(ok bool, path string) (string, error) {
	if !ok {
		if path != "" {
			_ = os.Remove(path)
		}
		return "", errTranscodeFailed
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("output path is empty")
	}
	return path, nil
}

// ThumbnailPrimaries returns the primaries tag used for generated thumbnails.
func (c *ColorMetadata) ThumbnailPrimaries(src string) string {
	src = strings.ToLower(strings.TrimSpace(src))
	switch src {
	case "", "bt2020", "bt2020nc", "bt2020c", "rec2020", "display-p3", "p3", "dci-p3":
		return "bt709"
	default:
		return src
	}
}

// CacheKey returns a deterministic cache key for a transcode input.
//
// The key covers the complete current mastering/content-light snapshot and
// every supplied colour parameter (source primaries, target primaries, peak
// luminance, conversion name, and so on). Maps and structs are serialised in a
// stable order before hashing, so repeating the same transcode after a restart
// produces a byte-identical key. Changing the primaries or peak changes the
// key, which prevents a stale colour conversion from being reused.
func (c *ColorMetadata) CacheKey(parts ...any) string {
	c.mu.RLock()
	mastering, hasMastering := c.mastering, c.hasMastering
	contentLight, hasContentLight := c.contentLight, c.hasContentLight
	c.mu.RUnlock()

	var b strings.Builder
	b.WriteString("color-metadata-cache-v1")
	b.WriteString("|mastering=")
	if hasMastering {
		appendCacheValue(&b, reflect.ValueOf(mastering))
	} else {
		b.WriteString("<none>")
	}
	b.WriteString("|content-light=")
	if hasContentLight {
		appendCacheValue(&b, reflect.ValueOf(contentLight))
	} else {
		b.WriteString("<none>")
	}
	b.WriteString("|params=")
	for i, part := range parts {
		if i > 0 {
			b.WriteByte(';')
		}
		appendCacheValue(&b, reflect.ValueOf(part))
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// TranscodeCacheKey is the typed form of CacheKey used by the transcode path.
func (c *ColorMetadata) TranscodeCacheKey(input, primaries string, peak float64) string {
	return c.CacheKey(input, primaries, peak)
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func ditherNoise(v uint16, seed uint32) int {
	x := uint32(v)*0x9e3779b1 ^ seed*0x85ebca6b
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	x ^= x >> 16
	return int(x%3) - 1
}

func appendCacheValue(b *strings.Builder, v reflect.Value) {
	if !v.IsValid() {
		b.WriteString("nil")
		return
	}

	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		b.WriteByte('&')
		appendCacheValue(b, v.Elem())
	case reflect.String:
		s := v.String()
		b.WriteString("s")
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
	case reflect.Bool:
		if v.Bool() {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString("i")
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		b.WriteString("u")
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		b.WriteString("f")
		b.WriteString(strconv.FormatUint(math.Float64bits(v.Float()), 16))
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			b.WriteString("nil-slice")
			return
		}
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			appendCacheValue(b, v.Index(i))
		}
		b.WriteByte(']')
	case reflect.Map:
		if v.IsNil() {
			b.WriteString("nil-map")
			return
		}
		type entry struct {
			key string
			val reflect.Value
		}
		entries := make([]entry, 0, v.Len())
		for _, key := range v.MapKeys() {
			entries = append(entries, entry{key: cacheValueString(key), val: v.MapIndex(key)})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		b.WriteString("{")
		for i, item := range entries {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(item.key)
			b.WriteByte('=')
			appendCacheValue(b, item.val)
		}
		b.WriteByte('}')
	case reflect.Struct:
		t := v.Type()
		b.WriteString(t.String())
		b.WriteByte('{')
		first := true
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(field.Name)
			b.WriteByte('=')
			appendCacheValue(b, v.Field(i))
		}
		b.WriteByte('}')
	default:
		b.WriteString(fmt.Sprintf("%v", v.Interface()))
	}
}

func cacheValueString(v reflect.Value) string {
	var b strings.Builder
	appendCacheValue(&b, v)
	return b.String()
}
