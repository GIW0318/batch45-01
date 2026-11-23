package task

import (
	"errors"
	"strings"
	"testing"
	"time"

	task "gsb/batch45-safety-c-02"
)

func newGuard() *task.MetadataGuard {
	return task.NewMetadataGuard(task.GuardConfig{
		MaxMetadataBytes: 1000,
		MaxBinaryBytes:   4096,
		MaxTimeout:       60 * time.Second,
		AllowedClients:   map[string]bool{"spiffe://mesh/client-a": true},
	})
}

func TestNormalizeLowercasesKeys(t *testing.T) {
	g := newGuard()
	got, err := g.Normalize(task.MD{"X-Tenant": {"tenant-1"}, "Grpc-Timeout": {"10s"}})
	if err != nil {
		t.Fatalf("Normalize returned unexpected error: %v", err)
	}
	if len(got["x-tenant"]) != 1 || got["x-tenant"][0] != "tenant-1" {
		t.Fatalf("keys must be lowercased, got %+v", got)
	}
	if _, ok := got["X-Tenant"]; ok {
		t.Fatalf("the original mixed case key must not survive")
	}
	if len(got["grpc-timeout"]) != 1 {
		t.Fatalf("grpc-timeout must be lowercased too, got %+v", got)
	}
}

func TestNormalizeRejectsCaseDuplicate(t *testing.T) {
	g := newGuard()
	_, err := g.Normalize(task.MD{"x-tenant": {"a"}, "X-Tenant": {"b"}})
	if !errors.Is(err, task.ErrCaseDuplicateKey) {
		t.Fatalf("case only duplicate keys must be rejected, got %v", err)
	}
}

func TestValidateRejectsDuplicateKey(t *testing.T) {
	g := newGuard()
	_, err := g.Validate(task.MD{"x-tenant": {"a", "b"}}, task.PeerInfo{CertIdentity: "spiffe://mesh/client-a"})
	if !errors.Is(err, task.ErrDuplicateKey) {
		t.Fatalf("a repeated key must be rejected, not silently forwarded, got %v", err)
	}
}

func TestValidateRejectsOversizeBinary(t *testing.T) {
	g := newGuard()
	md := task.MD{"payload-bin": {strings.Repeat("a", 5000)}}
	_, err := g.Validate(md, task.PeerInfo{CertIdentity: "spiffe://mesh/client-a"})
	if !errors.Is(err, task.ErrBinaryTooLarge) {
		t.Fatalf("oversized binary metadata must be rejected instead of truncated, got %v", err)
	}
}

func TestSizeLimitUsesUncompressedSize(t *testing.T) {
	g := newGuard()
	err := g.CheckCompression("gzip", true, 50, 5000)
	if !errors.Is(err, task.ErrMetadataTooLarge) {
		t.Fatalf("the metadata limit must apply before compression, got %v", err)
	}
}

func TestCompressionFlagMismatchRejected(t *testing.T) {
	g := newGuard()
	if err := g.CheckCompression("gzip", false, 100, 100); !errors.Is(err, task.ErrCompressionMismatch) {
		t.Fatalf("an uncompressed payload marked gzip must be rejected, got %v", err)
	}
	if err := g.CheckCompression("", true, 100, 100); !errors.Is(err, task.ErrCompressionMismatch) {
		t.Fatalf("a compressed payload without encoding must be rejected, got %v", err)
	}
}

func TestInjectedAuthorizationNotOverridable(t *testing.T) {
	g := newGuard()
	inbound := task.MD{"authorization": {"Bearer attacker"}, "x-tenant": {"tenant-1"}}
	injected := task.MD{"authorization": {"Bearer gateway"}}
	got, err := g.ApplyOutbound(inbound, injected)
	if err != nil {
		t.Fatalf("ApplyOutbound returned unexpected error: %v", err)
	}
	auth := got["authorization"]
	if len(auth) != 1 || auth[0] != "Bearer gateway" {
		t.Fatalf("gateway credentials must win over client metadata, got %+v", auth)
	}
	if got["x-tenant"][0] != "tenant-1" {
		t.Fatalf("non reserved metadata must be forwarded, got %+v", got)
	}
}

func TestTimeoutCapped(t *testing.T) {
	g := newGuard()
	if _, err := g.Timeout("300s"); !errors.Is(err, task.ErrTimeoutTooLarge) {
		t.Fatalf("a client extended timeout must be capped, got %v", err)
	}
	d, err := g.Timeout("10s")
	if err != nil {
		t.Fatalf("Timeout returned unexpected error: %v", err)
	}
	if d != 10*time.Second {
		t.Fatalf("valid timeout must be parsed, got %v", d)
	}
}

func TestUserAgentSpoofIgnored(t *testing.T) {
	g := newGuard()
	md := task.MD{"user-agent": {"grpc-go/client-a"}, "authorization": {"Bearer x"}}
	peer := task.PeerInfo{CertIdentity: "spiffe://mesh/rogue", UserAgent: "grpc-go/client-a"}
	if err := g.AuthorizeClient(md, peer); !errors.Is(err, task.ErrClientNotAllowed) {
		t.Fatalf("the whitelist must be driven by the peer identity, not the user agent, got %v", err)
	}
}

func TestAuthorizeClientAllowsListedPeer(t *testing.T) {
	g := newGuard()
	md := task.MD{"user-agent": {"anything"}}
	peer := task.PeerInfo{CertIdentity: "spiffe://mesh/client-a"}
	if err := g.AuthorizeClient(md, peer); err != nil {
		t.Fatalf("a whitelisted peer must be allowed, got %v", err)
	}
}

func TestTrailerStatusNotRewritten(t *testing.T) {
	g := newGuard()
	status, err := g.MapTrailer(task.MD{
		"grpc-status":        {"7"},
		"grpc-message":       {"denied"},
		"x-rewritten-status": {"0"},
	})
	if err != nil {
		t.Fatalf("MapTrailer returned unexpected error: %v", err)
	}
	if status.Code != 7 || status.Message != "denied" {
		t.Fatalf("the canonical trailer must win over a rewrite attempt, got %+v", status)
	}
}

func TestCrossTenantTraceIDRejected(t *testing.T) {
	g := newGuard()
	md := task.MD{"x-trace-id": {"trace-abc"}}
	id, err := g.TraceID(md, "tenant-a")
	if err != nil {
		t.Fatalf("TraceID returned unexpected error: %v", err)
	}
	if id != "trace-abc" {
		t.Fatalf("trace id must be preserved, got %q", id)
	}
	if _, err := g.TraceID(md, "tenant-b"); !errors.Is(err, task.ErrTraceIDReused) {
		t.Fatalf("a trace id must not be reused across tenants, got %v", err)
	}
}

func TestReflectionServiceNotExposed(t *testing.T) {
	g := newGuard()
	allowed, err := g.ReflectionAllowed("grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo", "tenant-a")
	if err != nil {
		t.Fatalf("ReflectionAllowed returned unexpected error: %v", err)
	}
	if allowed {
		t.Fatalf("internal reflection methods must stay hidden from tenants")
	}
	allowed, err = g.ReflectionAllowed("/svc.api.Users/GetUser", "tenant-a")
	if err != nil {
		t.Fatalf("ReflectionAllowed returned unexpected error: %v", err)
	}
	if !allowed {
		t.Fatalf("ordinary methods must not be blocked")
	}
}

func TestHealthCheckRequiresAuth(t *testing.T) {
	g := newGuard()
	allowed, err := g.HealthAllowed(task.MD{})
	if err != nil {
		t.Fatalf("HealthAllowed returned unexpected error: %v", err)
	}
	if allowed {
		t.Fatalf("an unauthenticated health probe must be rejected")
	}
	allowed, err = g.HealthAllowed(task.MD{"authorization": {"Bearer gateway"}})
	if err != nil {
		t.Fatalf("HealthAllowed returned unexpected error: %v", err)
	}
	if !allowed {
		t.Fatalf("an authenticated health probe must be allowed")
	}
}

func TestValidateAcceptsCleanMetadata(t *testing.T) {
	g := newGuard()
	md := task.MD{
		"authorization": {"Bearer gateway"},
		"x-tenant":      {"tenant-1"},
		"grpc-timeout":  {"10s"},
	}
	got, err := g.Validate(md, task.PeerInfo{CertIdentity: "spiffe://mesh/client-a", UserAgent: "grpc-go/1"})
	if err != nil {
		t.Fatalf("Validate returned unexpected error: %v", err)
	}
	if got.Values["x-tenant"][0] != "tenant-1" {
		t.Fatalf("validated metadata must carry the tenant, got %+v", got.Values)
	}
	if got.Timeout != 10*time.Second {
		t.Fatalf("validated metadata must carry the parsed timeout, got %v", got.Timeout)
	}
}
