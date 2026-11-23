package task

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
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
	cfg               GuardConfig
	allowedClients    map[string]bool
	allowedAlgorithms map[string]struct{}

	traceMu     sync.Mutex
	traceOwners map[string]string
}

// NewMetadataGuard creates a guard with the given policy.
func NewMetadataGuard(cfg GuardConfig) *MetadataGuard {
	g := &MetadataGuard{
		cfg:               cfg,
		allowedClients:    make(map[string]bool, len(cfg.AllowedClients)),
		allowedAlgorithms: make(map[string]struct{}, len(cfg.AllowedAlgorithms)),
		traceOwners:       make(map[string]string),
	}
	for identity, allowed := range cfg.AllowedClients {
		g.allowedClients[identity] = allowed
	}
	for _, algorithm := range cfg.AllowedAlgorithms {
		algorithm = strings.ToLower(strings.TrimSpace(algorithm))
		if algorithm != "" {
			g.allowedAlgorithms[algorithm] = struct{}{}
		}
	}
	return g
}

// Normalize lowercases keys and rejects case-only duplicates.
func (g *MetadataGuard) Normalize(md MD) (MD, error) {
	if md == nil {
		return nil, nil
	}

	normalized := make(MD, len(md))
	originals := make(map[string]string, len(md))
	for key, values := range md {
		canonical := strings.ToLower(key)
		if _, ok := originals[canonical]; ok {
			return nil, ErrCaseDuplicateKey
		}
		originals[canonical] = key
		normalized[canonical] = append([]string(nil), values...)
	}
	return normalized, nil
}

// Validate runs every inbound metadata check.
func (g *MetadataGuard) Validate(md MD, peer PeerInfo) (ValidatedMD, error) {
	normalized, err := g.Normalize(md)
	if err != nil {
		return ValidatedMD{}, err
	}

	for _, values := range normalized {
		if len(values) > 1 {
			return ValidatedMD{}, ErrDuplicateKey
		}
	}

	for key, values := range normalized {
		if !strings.HasSuffix(key, "-bin") || len(values) == 0 {
			continue
		}
		if g.cfg.MaxBinaryBytes > 0 && len(values[0]) > g.cfg.MaxBinaryBytes {
			return ValidatedMD{}, ErrBinaryTooLarge
		}
	}

	tenant, _, _ := metadataValue(normalized, "x-tenant")
	// The size cap is charged to this request's tenant. There is no global
	// counter, so a tenant that exceeds its cap cannot consume a neighbor's.
	if g.cfg.MaxMetadataBytes > 0 && metadataSize(normalized) > int64(g.cfg.MaxMetadataBytes) {
		return ValidatedMD{}, ErrMetadataTooLarge
	}

	if err := g.AuthorizeClient(normalized, peer); err != nil {
		return ValidatedMD{}, err
	}

	var timeout time.Duration
	if raw, ok, err := metadataValue(normalized, "grpc-timeout"); err != nil {
		return ValidatedMD{}, err
	} else if ok {
		timeout, err = g.Timeout(raw)
		if err != nil {
			return ValidatedMD{}, err
		}
	}

	traceID, err := g.TraceID(normalized, tenant)
	if err != nil {
		return ValidatedMD{}, err
	}

	return ValidatedMD{
		Values:  normalized,
		Timeout: timeout,
		TraceID: traceID,
	}, nil
}

// ApplyOutbound merges gateway injected metadata, which always wins.
func (g *MetadataGuard) ApplyOutbound(inbound, injected MD) (MD, error) {
	inbound, err := g.Normalize(inbound)
	if err != nil {
		return nil, err
	}
	injected, err = g.Normalize(injected)
	if err != nil {
		return nil, err
	}

	outbound := make(MD, len(inbound)+len(injected))
	for key, values := range inbound {
		outbound[key] = values
	}
	for key, values := range injected {
		outbound[key] = values
	}
	return outbound, nil
}

// Timeout parses and caps the grpc-timeout header.
func (g *MetadataGuard) Timeout(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("grpc-timeout is empty")
	}
	if len(raw) < 2 {
		return 0, fmt.Errorf("invalid grpc-timeout %q", raw)
	}

	valuePart := raw[:len(raw)-1]
	for i := 0; i < len(valuePart); i++ {
		if valuePart[i] < '0' || valuePart[i] > '9' {
			return g.goStyleTimeout(raw)
		}
	}

	value, err := strconv.ParseInt(valuePart, 10, 64)
	if err != nil {
		return 0, ErrTimeoutTooLarge
	}

	var unit time.Duration
	switch raw[len(raw)-1] {
	case 'H':
		unit = time.Hour
	case 'h':
		unit = time.Hour
	case 'M':
		unit = time.Minute
	case 'S':
		unit = time.Second
	case 's':
		unit = time.Second
	case 'm':
		unit = time.Millisecond
	case 'u':
		unit = time.Microsecond
	case 'n':
		unit = time.Nanosecond
	default:
		return g.goStyleTimeout(raw)
	}

	const maxDuration = time.Duration(1<<63 - 1)
	if value > int64(maxDuration/unit) {
		return 0, ErrTimeoutTooLarge
	}
	timeout := time.Duration(value) * unit
	if g.cfg.MaxTimeout > 0 && timeout > g.cfg.MaxTimeout {
		return 0, ErrTimeoutTooLarge
	}
	return timeout, nil
}

func (g *MetadataGuard) goStyleTimeout(raw string) (time.Duration, error) {
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout < 0 {
		return 0, fmt.Errorf("invalid grpc-timeout %q", raw)
	}
	if g.cfg.MaxTimeout > 0 && timeout > g.cfg.MaxTimeout {
		return 0, ErrTimeoutTooLarge
	}
	return timeout, nil
}

// AuthorizeClient authorizes a client by its TLS identity.
func (g *MetadataGuard) AuthorizeClient(md MD, peer PeerInfo) error {
	if peer.CertIdentity == "" || !g.allowedClients[peer.CertIdentity] {
		return ErrClientNotAllowed
	}
	return nil
}

// CheckCompression verifies the compression flag and the size accounting.
func (g *MetadataGuard) CheckCompression(encoding string, compressed bool, payloadSize, uncompressedSize int) error {
	if payloadSize < 0 || uncompressedSize < 0 {
		return errors.New("negative payload size")
	}
	if g.cfg.MaxMetadataBytes > 0 && uncompressedSize > g.cfg.MaxMetadataBytes {
		return ErrMetadataTooLarge
	}

	encoding = strings.ToLower(strings.TrimSpace(encoding))
	if compressed {
		if encoding == "" || encoding == "identity" {
			return ErrCompressionMismatch
		}
		if len(g.allowedAlgorithms) > 0 {
			if _, ok := g.allowedAlgorithms[encoding]; !ok {
				return ErrCompressionMismatch
			}
		}
		return nil
	}

	if encoding != "" && encoding != "identity" {
		return ErrCompressionMismatch
	}
	return nil
}

// MapTrailer maps trailers to a status without letting proxies rewrite it.
func (g *MetadataGuard) MapTrailer(md MD) (Status, error) {
	normalized, err := g.Normalize(md)
	if err != nil {
		return Status{}, err
	}

	status := Status{}
	if raw, ok, err := metadataValue(normalized, "grpc-status"); err != nil {
		return Status{}, err
	} else if ok && raw != "" {
		status.Code, err = strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return Status{}, fmt.Errorf("invalid grpc-status %q: %w", raw, err)
		}
	}
	if message, _, err := metadataValue(normalized, "grpc-message"); err != nil {
		return Status{}, err
	} else {
		status.Message = message
	}
	return status, nil
}

// TraceID returns the trace id after enforcing tenant ownership.
func (g *MetadataGuard) TraceID(md MD, tenant string) (string, error) {
	normalized, err := g.Normalize(md)
	if err != nil {
		return "", err
	}
	traceID, ok, err := metadataValue(normalized, "x-trace-id")
	if err != nil {
		return "", err
	}
	if !ok || traceID == "" {
		return "", nil
	}

	g.traceMu.Lock()
	defer g.traceMu.Unlock()
	if g.traceOwners == nil {
		g.traceOwners = make(map[string]string)
	}
	if owner, ok := g.traceOwners[traceID]; ok {
		if owner != tenant {
			return "", ErrTraceIDReused
		}
		return traceID, nil
	}
	g.traceOwners[traceID] = tenant
	return traceID, nil
}

// ReflectionAllowed decides whether the reflection service is exposed.
func (g *MetadataGuard) ReflectionAllowed(method string, tenant string) (bool, error) {
	canonical := strings.ToLower(strings.TrimSpace(method))
	if canonical == "" {
		return false, nil
	}
	if strings.Contains(canonical, "grpc.reflection") ||
		strings.Contains(canonical, "serverreflection") {
		return false, nil
	}
	return true, nil
}

// HealthAllowed decides whether a health probe may proceed.
func (g *MetadataGuard) HealthAllowed(md MD) (bool, error) {
	normalized, err := g.Normalize(md)
	if err != nil {
		return false, err
	}
	authorization, ok, err := metadataValue(normalized, "authorization")
	if err != nil {
		return false, err
	}
	if !ok || strings.TrimSpace(authorization) == "" {
		return false, nil
	}
	return true, nil
}

func metadataValue(md MD, key string) (string, bool, error) {
	values, ok := md[key]
	if !ok {
		return "", false, nil
	}
	if len(values) > 1 {
		return "", false, ErrDuplicateKey
	}
	if len(values) == 0 {
		return "", true, nil
	}
	return values[0], true, nil
}

func metadataSize(md MD) int64 {
	// Quota accounting uses canonical key bytes plus raw value bytes. Binary
	// values are counted as supplied rather than after base64 expansion.
	var total int64
	for key, values := range md {
		total += int64(len(key))
		for _, value := range values {
			total += int64(len(value))
		}
	}
	return total
}
