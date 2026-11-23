package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors of the verity guard.
var (
	ErrVerityDisabled            = errors.New("fs-verity not enabled")
	ErrVerityMismatch            = errors.New("verity digest mismatch")
	ErrDescriptorVersionMismatch = errors.New("descriptor version not covered by signature")
	ErrUntrustedKey              = errors.New("signing key not trusted")
	ErrFallbackForbidden         = errors.New("plain hash fallback is forbidden")
	ErrLayerUnverified           = errors.New("image layer not verified")
	ErrReadInconsistent          = errors.New("file changed during verification")
)

// AlgorithmVerity is the only digest algorithm accepted for verity descriptors.
const AlgorithmVerity = "fsverity-sha256"

// VerityDescriptor is the fs-verity descriptor of one file.
type VerityDescriptor struct {
	Version    int
	Algorithm  string
	RootDigest string
	SaltHex    string
	FileSize   int64
}

// VeritySignature signs a verity descriptor.
type VeritySignature struct {
	KeyID             string
	DescriptorVersion int
	RootDigest        string
	Value             []byte
}

// FileState is everything the guard knows about one file.
type FileState struct {
	Path          string
	Dev           uint64
	Ino           uint64
	Size          int64
	ContentSHA256 string
	Source        string
	VerityEnabled bool
	Descriptor    VerityDescriptor
}

// Verdict is the verification outcome for one file.
type Verdict struct {
	Trusted bool
	Reason  string
	Cached  bool
}

// LayerSpec is one layer of a multi architecture image.
type LayerSpec struct {
	Digest        string
	Arch          string
	ContentSHA256 string
	VerityEnabled bool
	Verified      bool
	Descriptor    VerityDescriptor
}

// ImageSpec is the image that must be fully verified before use.
type ImageSpec struct {
	Ref            string
	Layers         []LayerSpec
	RequiredArches []string
}

// ImageVerdict is the verification outcome for one image.
type ImageVerdict struct {
	Trusted bool
	Reasons []string
}

// GuardOptions configures the guard.
type GuardOptions struct {
	TrustedKeys          map[string]bool
	MaxDescriptorVersion int
}

// cacheKey binds a cached verdict to both the file identity and the exact
// content that was verified. Path is deliberately absent so renames hit while
// replaced content misses.
type cacheKey struct {
	Dev    uint64
	Ino    uint64
	Digest string
}

type cacheExportEntry struct {
	Dev           uint64  `json:"dev"`
	Ino           uint64  `json:"ino"`
	ContentSHA256 string  `json:"content_sha256"`
	Verdict       Verdict `json:"verdict"`
}

type cacheExport struct {
	Format  int                `json:"format"`
	Entries []cacheExportEntry `json:"entries"`
}

const cacheExportFormat = 1

// FsverityGuard verifies fs-verity protected files and images.
type FsverityGuard struct {
	mu                   sync.RWMutex
	trustedKeys          map[string]bool
	maxDescriptorVersion int
	cache                map[cacheKey]Verdict
}

// NewFsverityGuard creates a guard with the given trusted keys.
func NewFsverityGuard(opts GuardOptions) *FsverityGuard {
	keys := make(map[string]bool, len(opts.TrustedKeys))
	for key, trusted := range opts.TrustedKeys {
		if trusted {
			keys[key] = true
		}
	}
	return &FsverityGuard{
		trustedKeys:          keys,
		maxDescriptorVersion: opts.MaxDescriptorVersion,
		cache:                make(map[cacheKey]Verdict),
	}
}

// VerifyFile verifies one file, refusing to skip when verity is missing.
func (g *FsverityGuard) VerifyFile(st FileState) (Verdict, error) {
	if !st.VerityEnabled {
		return Verdict{Reason: ErrVerityDisabled.Error()}, ErrVerityDisabled
	}
	if err := g.validateDescriptor(st.Descriptor); err != nil {
		return Verdict{Reason: err.Error()}, err
	}

	content, ok := canonicalSHA256(st.ContentSHA256)
	if !ok {
		return Verdict{Reason: "file content digest is missing or invalid"}, fmt.Errorf("%w: invalid content digest", ErrVerityMismatch)
	}
	root, ok := canonicalSHA256(st.Descriptor.RootDigest)
	if !ok {
		return Verdict{Reason: "verity root digest is missing or invalid"}, fmt.Errorf("%w: invalid descriptor root digest", ErrVerityMismatch)
	}
	if content != root {
		return Verdict{Reason: "descriptor root digest does not match file content"}, fmt.Errorf("%w: descriptor root does not match content", ErrVerityMismatch)
	}
	if st.Size < 0 || st.Descriptor.FileSize < 0 || st.Size != st.Descriptor.FileSize {
		return Verdict{Reason: "descriptor file size does not match file state"}, fmt.Errorf("%w: descriptor file size does not match content", ErrVerityMismatch)
	}

	return Verdict{Trusted: true, Reason: "descriptor and content digest match"}, nil
}

// VerifySignature checks the signature and the descriptor version it covers.
func (g *FsverityGuard) VerifySignature(sig VeritySignature, desc VerityDescriptor) error {
	if sig.KeyID == "" || !g.trustedKeys[sig.KeyID] {
		return fmt.Errorf("%w: %q", ErrUntrustedKey, sig.KeyID)
	}
	if desc.Algorithm != AlgorithmVerity {
		return fmt.Errorf("%w: algorithm %q", ErrFallbackForbidden, desc.Algorithm)
	}
	if err := g.validateDescriptorVersion(desc.Version); err != nil {
		return err
	}
	if sig.DescriptorVersion != desc.Version {
		return fmt.Errorf("%w: signature covers version %d, descriptor is version %d",
			ErrDescriptorVersionMismatch, sig.DescriptorVersion, desc.Version)
	}

	// An empty signature root is treated as a legacy signature whose signed
	// payload carries the root elsewhere. A present root must bind the
	// descriptor exactly.
	if sig.RootDigest != "" {
		sigRoot, ok := canonicalSHA256(sig.RootDigest)
		descRoot, ok := canonicalSHA256(desc.RootDigest)
		if ok {
			if sigRoot != descRoot {
				return fmt.Errorf("%w: signature root does not match descriptor", ErrVerityMismatch)
			}
			return nil
		}
		if sig.RootDigest != desc.RootDigest {
			return fmt.Errorf("%w: signature root does not match descriptor", ErrVerityMismatch)
		}
	}
	return nil
}

// CacheLookup looks a verdict up by inode and content hash.
func (g *FsverityGuard) CacheLookup(st FileState) (Verdict, bool) {
	content, ok := canonicalSHA256(st.ContentSHA256)
	if !ok {
		return Verdict{}, false
	}
	key := cacheKey{Dev: st.Dev, Ino: st.Ino, Digest: content}

	g.mu.RLock()
	verdict, ok := g.cache[key]
	g.mu.RUnlock()
	if !ok {
		return Verdict{}, false
	}
	verdict.Cached = true
	return verdict, true
}

// CacheStore remembers a verdict for a file.
func (g *FsverityGuard) CacheStore(st FileState, v Verdict) error {
	content, ok := canonicalSHA256(st.ContentSHA256)
	if !ok {
		return fmt.Errorf("%w: refusing to cache without a content digest", ErrVerityMismatch)
	}
	key := cacheKey{Dev: st.Dev, Ino: st.Ino, Digest: content}
	v.Cached = false

	g.mu.Lock()
	g.cache[key] = v
	g.mu.Unlock()
	return nil
}

// VerifyImage verifies every architecture and every layer of an image.
func (g *FsverityGuard) VerifyImage(img ImageSpec) (ImageVerdict, error) {
	var reasons []string
	present := make(map[string]bool, len(img.Layers))

	for i, layer := range img.Layers {
		arch := strings.TrimSpace(layer.Arch)
		if arch == "" {
			reasons = append(reasons, fmt.Sprintf("architecture <unspecified>: layer %s has no architecture", layerLabel(i, layer)))
			continue
		}
		present[arch] = true
		if err := g.verifyLayer(layer); err != nil {
			reasons = append(reasons, fmt.Sprintf("architecture %s: layer %s: %v", arch, layerLabel(i, layer), err))
		}
	}

	required := img.RequiredArches
	if len(required) == 0 {
		required = make([]string, 0, len(present))
		for arch := range present {
			required = append(required, arch)
		}
		sort.Strings(required)
		if len(required) == 0 {
			reasons = append(reasons, "image has no layers")
		}
	}
	for _, arch := range required {
		arch = strings.TrimSpace(arch)
		if arch == "" {
			reasons = append(reasons, "required architecture <unspecified>")
			continue
		}
		if !present[arch] {
			reasons = append(reasons, fmt.Sprintf("architecture %s: required architecture has no layer", arch))
		}
	}

	if len(reasons) > 0 {
		return ImageVerdict{Trusted: false, Reasons: reasons}, nil
	}
	return ImageVerdict{Trusted: true}, nil
}

// VerifyRead re-hashes the bytes actually read from a file.
//
// Chunks are folded into a single streaming hash. The input is never joined or
// copied as a whole, so peak memory is independent of the file size.
func (g *FsverityGuard) VerifyRead(st FileState, chunks []string) (string, error) {
	if !st.VerityEnabled {
		return "", ErrVerityDisabled
	}
	if err := g.validateDescriptor(st.Descriptor); err != nil {
		return "", err
	}

	content, ok := canonicalSHA256(st.ContentSHA256)
	if !ok {
		return "", fmt.Errorf("%w: invalid content digest", ErrVerityMismatch)
	}
	root, ok := canonicalSHA256(st.Descriptor.RootDigest)
	if !ok {
		return "", fmt.Errorf("%w: invalid descriptor root digest", ErrVerityMismatch)
	}
	if content != root {
		return "", fmt.Errorf("%w: descriptor root does not match content", ErrVerityMismatch)
	}

	h, total, err := hashChunks(chunks)
	if err != nil {
		return "", err
	}
	return finishRead(st, h, total, content)
}

func (g *FsverityGuard) verifyLayer(layer LayerSpec) error {
	if !layer.VerityEnabled {
		return ErrVerityDisabled
	}
	if layer.Descriptor.Algorithm != AlgorithmVerity {
		return fmt.Errorf("%w: algorithm %q", ErrFallbackForbidden, layer.Descriptor.Algorithm)
	}
	if !layer.Verified {
		return ErrLayerUnverified
	}
	if err := g.validateDescriptorVersion(layer.Descriptor.Version); err != nil {
		return err
	}

	content, ok := canonicalSHA256(layer.ContentSHA256)
	if !ok {
		return fmt.Errorf("%w: layer content digest is missing or invalid", ErrVerityMismatch)
	}
	root, ok := canonicalSHA256(layer.Descriptor.RootDigest)
	if !ok {
		return fmt.Errorf("%w: layer verity metadata is missing or invalid", ErrVerityMismatch)
	}
	if content != root {
		return fmt.Errorf("%w: layer descriptor root does not match content", ErrVerityMismatch)
	}
	return nil
}

// ExportCache serialises the verification cache for a restart.
func (g *FsverityGuard) ExportCache() ([]byte, error) {
	g.mu.RLock()
	entries := make([]cacheExportEntry, 0, len(g.cache))
	for key, verdict := range g.cache {
		entries = append(entries, cacheExportEntry{
			Dev:           key.Dev,
			Ino:           key.Ino,
			ContentSHA256: key.Digest,
			Verdict:       verdict,
		})
	}
	g.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dev != entries[j].Dev {
			return entries[i].Dev < entries[j].Dev
		}
		if entries[i].Ino != entries[j].Ino {
			return entries[i].Ino < entries[j].Ino
		}
		return entries[i].ContentSHA256 < entries[j].ContentSHA256
	})

	return json.Marshal(cacheExport{Format: cacheExportFormat, Entries: entries})
}

// ImportCache restores a verification cache after a restart.
func (g *FsverityGuard) ImportCache(data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return errors.New("verity cache payload is empty")
	}

	var payload cacheExport
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("decode verity cache: %w", err)
	}
	if payload.Format != cacheExportFormat {
		return fmt.Errorf("unsupported verity cache format %d", payload.Format)
	}

	restored := make(map[cacheKey]Verdict, len(payload.Entries))
	for _, entry := range payload.Entries {
		content, ok := canonicalSHA256(entry.ContentSHA256)
		if !ok {
			return fmt.Errorf("verity cache entry for inode %d has an invalid content digest", entry.Ino)
		}
		verdict := entry.Verdict
		verdict.Cached = false
		restored[cacheKey{Dev: entry.Dev, Ino: entry.Ino, Digest: content}] = verdict
	}

	g.mu.Lock()
	g.cache = restored
	g.mu.Unlock()
	return nil
}

func (g *FsverityGuard) validateDescriptor(desc VerityDescriptor) error {
	if desc.Algorithm != AlgorithmVerity {
		return fmt.Errorf("%w: algorithm %q", ErrFallbackForbidden, desc.Algorithm)
	}
	return g.validateDescriptorVersion(desc.Version)
}

func (g *FsverityGuard) validateDescriptorVersion(version int) error {
	if version <= 0 {
		return fmt.Errorf("%w: descriptor version %d is invalid", ErrDescriptorVersionMismatch, version)
	}
	if g.maxDescriptorVersion > 0 && version > g.maxDescriptorVersion {
		return fmt.Errorf("%w: descriptor version %d exceeds supported version %d",
			ErrDescriptorVersionMismatch, version, g.maxDescriptorVersion)
	}
	return nil
}

func finishRead(st FileState, h hash.Hash, total int64, expected string) (string, error) {
	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return "", fmt.Errorf("%w: re-hashed bytes do not match descriptor", ErrVerityMismatch)
	}
	if st.Size != 0 && st.Descriptor.FileSize != 0 && st.Size != st.Descriptor.FileSize {
		return "", fmt.Errorf("%w: file state and descriptor sizes disagree", ErrReadInconsistent)
	}
	if st.Size != 0 && total != st.Size {
		return "", fmt.Errorf("%w: read %d bytes, expected %d", ErrReadInconsistent, total, st.Size)
	}
	if st.Descriptor.FileSize != 0 && total != st.Descriptor.FileSize {
		return "", fmt.Errorf("%w: read %d bytes, descriptor says %d", ErrReadInconsistent, total, st.Descriptor.FileSize)
	}
	return got, nil
}

const readBlockSize = 64 * 1024

// hashChunks folds strings into a hash through one reusable block buffer. It
// never joins the chunks and performs no allocation proportional to the input.
func hashChunks(chunks []string) (hash.Hash, int64, error) {
	h := sha256.New()
	buf := make([]byte, readBlockSize)
	var total int64

	for _, chunk := range chunks {
		for len(chunk) > 0 {
			n := copy(buf, chunk)
			chunk = chunk[n:]
			written, err := h.Write(buf[:n])
			if err != nil {
				return nil, 0, fmt.Errorf("%w: %v", ErrReadInconsistent, err)
			}
			total += int64(written)
		}
	}
	return h, total, nil
}

func layerLabel(index int, layer LayerSpec) string {
	if layer.Digest != "" {
		return layer.Digest
	}
	return fmt.Sprintf("#%d", index)
}

// canonicalSHA256 accepts an optional "sha256:" prefix, normalises case, and
// requires a complete 32-byte hex digest.
func canonicalSHA256(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= len("sha256:") && strings.EqualFold(value[:len("sha256:")], "sha256:") {
		value = value[len("sha256:"):]
	}
	value = strings.ToLower(value)
	if len(value) != sha256.Size*2 {
		return "", false
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", false
	}
	return value, true
}
