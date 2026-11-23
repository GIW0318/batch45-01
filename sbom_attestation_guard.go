package task

import (
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxComponents   = 10000
	maxDependencies = 50000
)

var (
	errNotFound       = errors.New("attestation not found")
	errInvalidSubject = errors.New("invalid subject digest")
)

// Component is one software component of an SBOM.
type Component struct {
	Name    string
	Version string
}

// Dependency is a directed edge: From depends on To.
type Dependency struct {
	From string
	To   string
}

// SBOM is a bill of materials for one subject digest.
type SBOM struct {
	Format        string
	SubjectDigest string
	Components    []Component
	Deps          []Dependency
	CreatedAt     time.Time
}

// Attestation is a signed statement about a build artifact.
type Attestation struct {
	ID            string
	Tenant        string
	SubjectDigest string
	PayloadDigest string
	SBOM          SBOM
	Materials     []string
	BuildParams   map[string]string
	SignedAt      time.Time
	ExpiresAt     time.Time
	Signature     []byte
	Revoked       bool
}

// AuditQuery selects attestations for audit.
type AuditQuery struct {
	Tenant         string
	SubjectDigest  string
	IncludeRevoked bool
}

// SbomGuard stores and validates SBOM attestations.
type SbomGuard struct {
	mu      sync.RWMutex
	entries map[string]Attestation
}

// NewSbomGuard returns an empty guard.
func NewSbomGuard() *SbomGuard {
	return &SbomGuard{entries: make(map[string]Attestation)}
}

// Submit validates and stores an attestation.
func (g *SbomGuard) Submit(att Attestation, now time.Time) error {
	if att.SubjectDigest == "" || att.SBOM.SubjectDigest == "" {
		return errInvalidSubject
	}
	if att.SubjectDigest != att.SBOM.SubjectDigest {
		return fmt.Errorf("attestation subject %q does not match SBOM subject %q", att.SubjectDigest, att.SBOM.SubjectDigest)
	}
	if err := g.ValidateSBOM(att.SBOM); err != nil {
		return err
	}
	if att.PayloadDigest != g.SBOMDigest(att.SBOM) {
		return errors.New("payload digest does not cover SBOM content")
	}
	if !att.ExpiresAt.IsZero() && !now.Before(att.ExpiresAt) {
		return errors.New("attestation has expired")
	}
	if !hasBaseImageMaterial(att.Materials) {
		return errors.New("attestation is missing a base image material")
	}
	if containsSecretParam(att.BuildParams) {
		return errors.New("build parameters contain secrets")
	}
	if len(att.Materials) > maxDependencies {
		return errors.New("too many materials")
	}

	att = canonicalAttestation(att)
	key := storeKey(att.Tenant, att.SubjectDigest)

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.entries == nil {
		g.entries = make(map[string]Attestation)
	}

	existing, ok := g.entries[key]
	if !ok {
		g.entries[key] = att
		return nil
	}
	if existing.SBOM.Format != att.SBOM.Format {
		return fmt.Errorf("mixed SBOM formats for one subject: %q and %q", existing.SBOM.Format, att.SBOM.Format)
	}

	// Re-submitting the same attestation is a no-op. The content digest is
	// validated above, so this cannot be used to smuggle different content in.
	if existing.ID == att.ID {
		return nil
	}

	cmp := att.SBOM.CreatedAt.Compare(existing.SBOM.CreatedAt)
	switch {
	case cmp > 0:
		g.entries[key] = att
	case cmp < 0:
		return fmt.Errorf("stale SBOM: %s was created before stored %s", att.ID, existing.ID)
	default:
		// Equal timestamps are ordered by ID so concurrent submissions are
		// deterministic while still retaining only one record.
		if att.ID < existing.ID {
			g.entries[key] = att
		}
	}
	return nil
}

// Get returns the stored attestation of a tenant for a digest.
func (g *SbomGuard) Get(tenant, digest string) (*Attestation, error) {
	if tenant == "" || digest == "" {
		return nil, errNotFound
	}
	g.mu.RLock()
	att, ok := g.entries[storeKey(tenant, digest)]
	g.mu.RUnlock()
	if !ok {
		return nil, errNotFound
	}
	out := cloneAttestation(att)
	return &out, nil
}

// Audit lists attestations matching the query.
func (g *SbomGuard) Audit(q AuditQuery) ([]Attestation, error) {
	g.mu.RLock()
	out := make([]Attestation, 0, len(g.entries))
	for _, att := range g.entries {
		if q.Tenant != "" && att.Tenant != q.Tenant {
			continue
		}
		if q.SubjectDigest != "" && att.SubjectDigest != q.SubjectDigest {
			continue
		}
		if att.Revoked && !q.IncludeRevoked {
			continue
		}
		out = append(out, cloneAttestation(att))
	}
	g.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		if out[i].SubjectDigest != out[j].SubjectDigest {
			return out[i].SubjectDigest < out[j].SubjectDigest
		}
		if !out[i].SBOM.CreatedAt.Equal(out[j].SBOM.CreatedAt) {
			return out[i].SBOM.CreatedAt.Before(out[j].SBOM.CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Merge combines two attestations of the same subject digest.
func (g *SbomGuard) Merge(a, b Attestation) (Attestation, error) {
	aSubject := attestationSubject(a)
	bSubject := attestationSubject(b)
	if aSubject == "" || bSubject == "" {
		return Attestation{}, errInvalidSubject
	}
	if aSubject != bSubject {
		return Attestation{}, fmt.Errorf("cannot merge attestations for different subjects: %q and %q", aSubject, bSubject)
	}
	if a.Tenant != "" && b.Tenant != "" && a.Tenant != b.Tenant {
		return Attestation{}, errors.New("cannot merge attestations from different tenants")
	}
	if a.SBOM.Format != "" && b.SBOM.Format != "" && a.SBOM.Format != b.SBOM.Format {
		return Attestation{}, errors.New("cannot merge attestations with different SBOM formats")
	}

	merged := cloneAttestation(a)
	if merged.ID == "" {
		merged.ID = b.ID
	}
	if merged.Tenant == "" {
		merged.Tenant = b.Tenant
	}
	merged.SubjectDigest = aSubject
	merged.SBOM.SubjectDigest = aSubject
	if merged.SBOM.Format == "" {
		merged.SBOM.Format = b.SBOM.Format
	}
	if b.SBOM.CreatedAt.After(merged.SBOM.CreatedAt) {
		merged.SBOM.CreatedAt = b.SBOM.CreatedAt
	}

	merged.SBOM.Components = mergeComponents(a.SBOM.Components, b.SBOM.Components)
	merged.SBOM.Deps = mergeDependencies(a.SBOM.Deps, b.SBOM.Deps)
	merged.Materials = mergeStrings(a.Materials, b.Materials)
	merged.BuildParams = mergeParams(a.BuildParams, b.BuildParams)
	if len(merged.SBOM.Components) > maxComponents {
		return Attestation{}, fmt.Errorf("merged SBOM has too many components: %d exceeds limit %d", len(merged.SBOM.Components), maxComponents)
	}
	if len(merged.SBOM.Deps) > maxDependencies || len(merged.Materials) > maxDependencies {
		return Attestation{}, errors.New("merged SBOM exceeds dependency or material limit")
	}
	if len(merged.Signature) == 0 {
		merged.Signature = append([]byte(nil), b.Signature...)
	}
	if b.SignedAt.After(merged.SignedAt) {
		merged.SignedAt = b.SignedAt
	}
	merged.ExpiresAt = earliestNonZero(merged.ExpiresAt, b.ExpiresAt)
	merged.Revoked = a.Revoked || b.Revoked
	merged.PayloadDigest = g.SBOMDigest(merged.SBOM)

	return merged, nil
}

// ValidateSBOM validates components, versions and formats.
func (g *SbomGuard) ValidateSBOM(s SBOM) error {
	if strings.TrimSpace(s.SubjectDigest) == "" {
		return errInvalidSubject
	}
	if !validFormat(s.Format) {
		return fmt.Errorf("unsupported SBOM format %q", s.Format)
	}
	if len(s.Components) > maxComponents {
		return fmt.Errorf("too many components: %d exceeds limit %d", len(s.Components), maxComponents)
	}
	if len(s.Deps) > maxDependencies {
		return fmt.Errorf("too many dependencies: %d exceeds limit %d", len(s.Deps), maxDependencies)
	}

	seen := make(map[string]struct{}, len(s.Components))
	for _, c := range s.Components {
		if strings.TrimSpace(c.Name) == "" {
			return errors.New("component name must not be empty")
		}
		if !isPinnedVersion(c.Version) {
			return fmt.Errorf("component %q has a non-pinned version %q", c.Name, c.Version)
		}
		seen[c.Name] = struct{}{}
	}
	for _, d := range s.Deps {
		if strings.TrimSpace(d.From) == "" || strings.TrimSpace(d.To) == "" {
			return errors.New("dependency endpoint must not be empty")
		}
		if _, ok := seen[d.From]; !ok {
			return fmt.Errorf("dependency references unknown component %q", d.From)
		}
		if _, ok := seen[d.To]; !ok {
			return fmt.Errorf("dependency references unknown component %q", d.To)
		}
	}
	if _, err := dependencyOrder(s); err != nil {
		return fmt.Errorf("invalid dependency graph: %w", err)
	}
	return nil
}

// DependencyOrder returns the components in dependency order.
func (g *SbomGuard) DependencyOrder(s SBOM) ([]string, error) {
	return dependencyOrder(s)
}

// RedactParams removes secrets from build parameters.
func (g *SbomGuard) RedactParams(p map[string]string) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, len(p))
	for k, v := range p {
		if isSecretKey(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = v
	}
	return out
}

// Revoke marks an attestation as revoked.
func (g *SbomGuard) Revoke(id string) error {
	if id == "" {
		return errNotFound
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	found := false
	for key, att := range g.entries {
		if att.ID != id {
			continue
		}
		att.Revoked = true
		g.entries[key] = att
		found = true
	}
	if !found {
		return errNotFound
	}
	return nil
}

// SBOMDigest returns the canonical digest of an SBOM payload.
func (g *SbomGuard) SBOMDigest(s SBOM) string {
	type canonicalSBOM struct {
		Format        string       `json:"format"`
		SubjectDigest string       `json:"subjectDigest"`
		Components    []Component  `json:"components"`
		Deps          []Dependency `json:"deps"`
		CreatedAt     string       `json:"createdAt"`
	}

	c := canonicalSBOM{
		Format:        s.Format,
		SubjectDigest: s.SubjectDigest,
		Components:    normalizedComponents(s.Components),
		Deps:          normalizedDependencies(s.Deps),
		CreatedAt:     s.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(c)
	if err != nil {
		// The canonical struct only contains strings and slices of strings,
		// so marshaling cannot fail in practice. Keep a deterministic fallback.
		payload = []byte(fmt.Sprintf("%s|%s|%s", c.Format, c.SubjectDigest, c.CreatedAt))
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func storeKey(tenant, digest string) string {
	return tenant + "\x00" + digest
}

func canonicalAttestation(att Attestation) Attestation {
	att.SBOM.Components = normalizedComponents(att.SBOM.Components)
	att.SBOM.Deps = normalizedDependencies(att.SBOM.Deps)
	att.Materials = mergeStrings(att.Materials, nil)
	att.BuildParams = cloneParams(att.BuildParams)
	att.Signature = append([]byte(nil), att.Signature...)
	return att
}

func cloneAttestation(att Attestation) Attestation {
	return canonicalAttestation(att)
}

func cloneParams(p map[string]string) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func normalizedComponents(in []Component) []Component {
	if len(in) == 0 {
		return []Component{}
	}
	seen := make(map[Component]struct{}, len(in))
	out := make([]Component, 0, len(in))
	for _, c := range in {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out
}

func normalizedDependencies(in []Dependency) []Dependency {
	if len(in) == 0 {
		return []Dependency{}
	}
	seen := make(map[Dependency]struct{}, len(in))
	out := make([]Dependency, 0, len(in))
	for _, d := range in {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

func mergeComponents(a, b []Component) []Component {
	all := make([]Component, 0, len(a)+len(b))
	all = append(all, a...)
	all = append(all, b...)
	return normalizedComponents(all)
}

func mergeDependencies(a, b []Dependency) []Dependency {
	all := make([]Dependency, 0, len(a)+len(b))
	all = append(all, a...)
	all = append(all, b...)
	return normalizedDependencies(all)
}

func mergeStrings(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, values := range [][]string{a, b} {
		for _, v := range values {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func mergeParams(a, b map[string]string) map[string]string {
	if a == nil && b == nil {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range b {
		out[k] = v
	}
	for k, v := range a {
		out[k] = v
	}
	return out
}

func earliestNonZero(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if b.Before(a) {
		return b
	}
	return a
}

func attestationSubject(a Attestation) string {
	if a.SubjectDigest != "" {
		return a.SubjectDigest
	}
	return a.SBOM.SubjectDigest
}

func validFormat(format string) bool {
	f := strings.ToLower(format)
	switch {
	case strings.HasPrefix(f, "spdx-") && len(f) > len("spdx-"):
		return true
	case strings.HasPrefix(f, "cyclonedx-") && len(f) > len("cyclonedx-"):
		return true
	default:
		return false
	}
}

func isPinnedVersion(version string) bool {
	v := strings.TrimSpace(version)
	if v == "" || v != version {
		return false
	}
	lower := strings.ToLower(v)
	if lower == "latest" || lower == "current" || lower == "stable" {
		return false
	}
	if strings.ContainsAny(v, "<>=~^*,|") {
		return false
	}
	if strings.ContainsAny(v, " \t\r\n") {
		return false
	}
	for _, part := range strings.Split(lower, ".") {
		if part == "x" || part == "*" {
			return false
		}
	}
	return true
}

func hasBaseImageMaterial(materials []string) bool {
	for _, material := range materials {
		m := strings.ToLower(strings.TrimSpace(material))
		if strings.Contains(m, "base-image") ||
			strings.Contains(m, "base_image") ||
			strings.Contains(m, "base image") ||
			strings.HasPrefix(m, "base:") {
			return true
		}
	}
	return false
}

func containsSecretParam(params map[string]string) bool {
	for key := range params {
		if isSecretKey(key) {
			return true
		}
	}
	return false
}

func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	for _, token := range []string{
		"password", "passwd", "passphrase", "secret", "token",
		"apikey", "api_key", "api-key", "access_key", "accesskey",
		"private_key", "privatekey", "credential", "authorization",
		"cookie", "session", "signing", "encryption", "ssh", "pwd", "key",
	} {
		if strings.Contains(k, token) {
			return true
		}
	}
	return false
}

func dependencyOrder(s SBOM) ([]string, error) {
	if len(s.Components) > maxComponents {
		return nil, fmt.Errorf("too many components: %d exceeds limit %d", len(s.Components), maxComponents)
	}
	if len(s.Deps) > maxDependencies {
		return nil, fmt.Errorf("too many dependencies: %d exceeds limit %d", len(s.Deps), maxDependencies)
	}

	names := make(map[string]struct{}, len(s.Components))
	for _, c := range s.Components {
		if strings.TrimSpace(c.Name) == "" {
			return nil, errors.New("component name must not be empty")
		}
		names[c.Name] = struct{}{}
	}

	adjacency := make(map[string][]string, len(names))
	depsLeft := make(map[string]int, len(names))
	seenEdges := make(map[Dependency]struct{}, len(s.Deps))
	for _, d := range s.Deps {
		if _, ok := names[d.From]; !ok {
			return nil, fmt.Errorf("dependency references unknown component %q", d.From)
		}
		if _, ok := names[d.To]; !ok {
			return nil, fmt.Errorf("dependency references unknown component %q", d.To)
		}
		if _, ok := seenEdges[d]; ok {
			continue
		}
		seenEdges[d] = struct{}{}
		depsLeft[d.From]++
		adjacency[d.To] = append(adjacency[d.To], d.From)
	}

	ready := make(stringHeap, 0, len(names))
	for name := range names {
		if depsLeft[name] == 0 {
			ready = append(ready, name)
		}
	}
	heap.Init(&ready)

	order := make([]string, 0, len(names))
	for ready.Len() > 0 {
		name := heap.Pop(&ready).(string)
		order = append(order, name)
		for _, dependent := range adjacency[name] {
			depsLeft[dependent]--
			if depsLeft[dependent] == 0 {
				heap.Push(&ready, dependent)
			}
		}
	}
	if len(order) != len(names) {
		return nil, errors.New("dependency cycle detected")
	}
	return order, nil
}

type stringHeap []string

func (h stringHeap) Len() int           { return len(h) }
func (h stringHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h stringHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stringHeap) Push(x any)        { *h = append(*h, x.(string)) }
func (h *stringHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
