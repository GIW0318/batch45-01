package task

import (
	"errors"
	"time"
)

// Sentinel errors of the metadata guard.
var (
	ErrCaseDuplicateKey    = errors.New("duplicate metadata key differing only in case")
	ErrDuplicateKey        = errors.New("duplicate metadata key")
	ErrBinaryTooLarge      = errors.New("binary metadata value too large")
	ErrMetadataTooLarge    = errors.New("metadata size exceeds the limit")
	ErrTimeoutTooLarge     = errors.New("grpc-timeout exceeds the gateway cap")
	ErrCompressionMismatch = errors.New("compression flag does not match the payload")
	ErrClientNotAllowed    = errors.New("peer identity not in the client whitelist")
	ErrTraceIDReused       = errors.New("trace id already owned by another tenant")
)

// MD is a gRPC metadata map.
type MD map[string][]string

// PeerInfo is what the gateway knows about the caller.
type PeerInfo struct {
	CertIdentity string
	UserAgent    string
}

// GuardConfig configures the gateway metadata policy.
type GuardConfig struct {
	MaxMetadataBytes  int
	MaxBinaryBytes    int
	MaxTimeout        time.Duration
	AllowedClients    map[string]bool
	AllowedAlgorithms []string
}

// ValidatedMD is metadata that passed every gateway check.
type ValidatedMD struct {
	Values  MD
	Timeout time.Duration
	TraceID string
}

// Status is a gRPC trailer status.
type Status struct {
	Code    int
	Message string
}

// MetadataGuard validates inbound gRPC metadata.
type MetadataGuard struct {
}

// NewMetadataGuard creates a guard with the given policy.
func NewMetadataGuard(cfg GuardConfig) *MetadataGuard {
	return &MetadataGuard{}
}

// Normalize lowercases keys and rejects case-only duplicates.
func (g *MetadataGuard) Normalize(md MD) (MD, error) {
	return nil, errors.New("not implemented")
}

// Validate runs every inbound metadata check.
func (g *MetadataGuard) Validate(md MD, peer PeerInfo) (ValidatedMD, error) {
	return ValidatedMD{}, errors.New("not implemented")
}

// ApplyOutbound merges gateway injected metadata, which always wins.
func (g *MetadataGuard) ApplyOutbound(inbound, injected MD) (MD, error) {
	return nil, errors.New("not implemented")
}

// Timeout parses and caps the grpc-timeout header.
func (g *MetadataGuard) Timeout(raw string) (time.Duration, error) {
	return 0, errors.New("not implemented")
}

// AuthorizeClient authorizes a client by its TLS identity.
func (g *MetadataGuard) AuthorizeClient(md MD, peer PeerInfo) error {
	return errors.New("not implemented")
}

// CheckCompression verifies the compression flag and the size accounting.
func (g *MetadataGuard) CheckCompression(encoding string, compressed bool, payloadSize, uncompressedSize int) error {
	return errors.New("not implemented")
}

// MapTrailer maps trailers to a status without letting proxies rewrite it.
func (g *MetadataGuard) MapTrailer(md MD) (Status, error) {
	return Status{}, errors.New("not implemented")
}

// TraceID returns the trace id after enforcing tenant ownership.
func (g *MetadataGuard) TraceID(md MD, tenant string) (string, error) {
	return "", errors.New("not implemented")
}

// ReflectionAllowed decides whether the reflection service is exposed.
func (g *MetadataGuard) ReflectionAllowed(method string, tenant string) (bool, error) {
	return false, errors.New("not implemented")
}

// HealthAllowed decides whether a health probe may proceed.
func (g *MetadataGuard) HealthAllowed(md MD) (bool, error) {
	return false, errors.New("not implemented")
}
