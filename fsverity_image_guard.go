package task

import "errors"

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

// FsverityGuard verifies fs-verity protected files and images.
type FsverityGuard struct {
}

// NewFsverityGuard creates a guard with the given trusted keys.
func NewFsverityGuard(opts GuardOptions) *FsverityGuard {
	return &FsverityGuard{}
}

// VerifyFile verifies one file, refusing to skip when verity is missing.
func (g *FsverityGuard) VerifyFile(st FileState) (Verdict, error) {
	return Verdict{}, errors.New("not implemented")
}

// VerifySignature checks the signature and the descriptor version it covers.
func (g *FsverityGuard) VerifySignature(sig VeritySignature, desc VerityDescriptor) error {
	return errors.New("not implemented")
}

// CacheLookup looks a verdict up by inode and content hash.
func (g *FsverityGuard) CacheLookup(st FileState) (Verdict, bool) {
	return Verdict{}, false
}

// CacheStore remembers a verdict for a file.
func (g *FsverityGuard) CacheStore(st FileState, v Verdict) error {
	return errors.New("not implemented")
}

// VerifyImage verifies every architecture and every layer of an image.
func (g *FsverityGuard) VerifyImage(img ImageSpec) (ImageVerdict, error) {
	return ImageVerdict{}, errors.New("not implemented")
}

// VerifyRead re-hashes the bytes actually read from a file.
func (g *FsverityGuard) VerifyRead(st FileState, chunks []string) (string, error) {
	return "", errors.New("not implemented")
}

// ExportCache serialises the verification cache for a restart.
func (g *FsverityGuard) ExportCache() ([]byte, error) {
	return nil, errors.New("not implemented")
}

// ImportCache restores a verification cache after a restart.
func (g *FsverityGuard) ImportCache(data []byte) error {
	return errors.New("not implemented")
}
