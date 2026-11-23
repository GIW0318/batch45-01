package task

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Key purposes and version states.
const (
	PurposeEncrypt = "encrypt"
	PurposeSign    = "sign"

	StateActive    = "active"
	StateRetired   = "retired"
	StateDestroyed = "destroyed"
)

var (
	ErrNoKey          = errors.New("kms: unknown key")
	ErrPurpose        = errors.New("kms: key purpose mismatch")
	ErrImportLength   = errors.New("kms: imported material has invalid length")
	ErrConfirmNeeded  = errors.New("kms: destroy requires confirmation")
	ErrActiveDestroy  = errors.New("kms: active version cannot be destroyed")
	ErrHSMDown        = errors.New("kms: HSM session is down")
	ErrRotationFailed = errors.New("kms: rotation failed")
	ErrUnknownVersion = errors.New("kms: unknown key version")

	ErrKeyExists = errors.New("kms: key already exists")

	// Reference-related errors are aliases so callers that only care about
	// the destroy gate can use any of the common names.
	ErrVersionReferenced = errors.New("kms: key version is still referenced")
	ErrVersionInUse      = ErrVersionReferenced
	ErrReferenced        = ErrVersionReferenced
	ErrRetention         = ErrVersionReferenced

	ErrCiphertext = errors.New("kms: ciphertext authentication failed")
	ErrBackup     = errors.New("kms: invalid backup state")

	ErrVersionDestroyed = fmt.Errorf("%w: version already destroyed", ErrUnknownVersion)
)

// DefaultReferenceRetention is how long a version remains protected after the
// last ciphertext that references it. It mirrors the ten minute failure window
// from the incident that motivated this guard.
const DefaultReferenceRetention = 10 * time.Minute

const (
	confirmDestroy = "CONFIRM"
	backupPrefix   = "GSBKMS1:"
	zeroTimeNanos  = int64(-1 << 63)
)

// KeyVersion is one version of a key.
type KeyVersion struct {
	Tenant    string
	KeyID     string
	Version   int
	Purpose   string
	State     string
	CreatedAt time.Time
	HSMHandle string
}

// Ciphertext binds the key version that produced it.
type Ciphertext struct {
	Tenant  string
	KeyID   string
	Version int
	Blob    []byte
}

// ReferenceCountError reports a refused destroy and the number of retained
// ciphertext references for the version.
type ReferenceCountError struct {
	Tenant  string
	KeyID   string
	Version int
	Count   int
}

func (e *ReferenceCountError) Error() string {
	return fmt.Sprintf(
		"kms: key version %s/%s v%d is still referenced by %d ciphertext(s); retention window not elapsed",
		e.Tenant, e.KeyID, e.Version, e.Count,
	)
}

func (e *ReferenceCountError) Unwrap() error { return ErrVersionReferenced }

// Aliases for callers using either spelling.
type VersionReferenceError = ReferenceCountError
type ReferenceError = ReferenceCountError

type keyName struct {
	tenant string
	keyID  string
}

type versionState struct {
	meta     KeyVersion
	material []byte
	refs     int
	lastRef  time.Time
}

type keyRing struct {
	tenant        string
	keyID         string
	purpose       string
	versions      map[int]*versionState
	activeVersion int
	nextVersion   int
	lastRotatedAt time.Time
}

type cacheEntry struct {
	version  KeyVersion
	cachedAt time.Time
}

// RotationGuard manages key versions and rotations.
type RotationGuard struct {
	mu sync.Mutex

	now            time.Time
	keys           map[keyName]*keyRing
	hsmUp          bool
	hsmGeneration  uint64
	handleSequence uint64

	cache    map[keyName]cacheEntry
	cacheTTL time.Duration

	retention time.Duration

	failNextRotate map[keyName]bool
}

// NewRotationGuard creates an empty guard with a fixed clock origin.
func NewRotationGuard(now time.Time) (*RotationGuard, error) {
	return &RotationGuard{
		now:            now,
		keys:           make(map[keyName]*keyRing),
		hsmUp:          true,
		hsmGeneration:  1,
		cache:          make(map[keyName]cacheEntry),
		retention:      DefaultReferenceRetention,
		failNextRotate: make(map[keyName]bool),
	}, nil
}

// CreateKey creates version 1 of a key for one tenant. Key ids are scoped per
// tenant.
func (g *RotationGuard) CreateKey(tenant, keyID, purpose string, material []byte) (KeyVersion, error) {
	if purpose != PurposeEncrypt && purpose != PurposeSign {
		return KeyVersion{}, ErrPurpose
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	if _, ok := g.keys[name]; ok {
		return KeyVersion{}, ErrKeyExists
	}
	if !g.hsmUp {
		return KeyVersion{}, ErrHSMDown
	}

	ring := &keyRing{
		tenant:        tenant,
		keyID:         keyID,
		purpose:       purpose,
		versions:      make(map[int]*versionState),
		activeVersion: 1,
		nextVersion:   2,
	}
	vs := &versionState{
		meta: KeyVersion{
			Tenant:    tenant,
			KeyID:     keyID,
			Version:   1,
			Purpose:   purpose,
			State:     StateActive,
			CreatedAt: g.now,
			HSMHandle: g.newHandleLocked(),
		},
		material: cloneBytes(material),
	}
	ring.versions[1] = vs
	g.keys[name] = ring
	g.cacheSetLocked(name, vs.meta)
	return vs.meta, nil
}

// ImportKey imports material into a new key, validating its length.
func (g *RotationGuard) ImportKey(tenant, keyID string, material []byte) (KeyVersion, error) {
	if !validAESKeyLength(len(material)) {
		return KeyVersion{}, ErrImportLength
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	if _, ok := g.keys[name]; ok {
		return KeyVersion{}, ErrKeyExists
	}
	if !g.hsmUp {
		return KeyVersion{}, ErrHSMDown
	}

	ring := &keyRing{
		tenant:        tenant,
		keyID:         keyID,
		purpose:       PurposeEncrypt,
		versions:      make(map[int]*versionState),
		activeVersion: 1,
		nextVersion:   2,
	}
	vs := &versionState{
		meta: KeyVersion{
			Tenant:    tenant,
			KeyID:     keyID,
			Version:   1,
			Purpose:   PurposeEncrypt,
			State:     StateActive,
			CreatedAt: g.now,
			HSMHandle: g.newHandleLocked(),
		},
		material: cloneBytes(material),
	}
	ring.versions[1] = vs
	g.keys[name] = ring
	g.cacheSetLocked(name, vs.meta)
	return vs.meta, nil
}

// Encrypt seals plaintext with the active encryption key version and records
// that version in the ciphertext.
func (g *RotationGuard) Encrypt(tenant, keyID string, plaintext []byte) (Ciphertext, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	ring, ok := g.keys[name]
	if !ok {
		return Ciphertext{}, ErrNoKey
	}
	if ring.purpose != PurposeEncrypt {
		return Ciphertext{}, ErrPurpose
	}
	if !g.hsmUp {
		return Ciphertext{}, ErrHSMDown
	}

	vs := ring.versions[ring.activeVersion]
	if vs == nil || vs.meta.State != StateActive {
		return Ciphertext{}, ErrUnknownVersion
	}

	blob, err := seal(vs.material, tenant, keyID, vs.meta.Version, plaintext)
	if err != nil {
		return Ciphertext{}, err
	}

	// Every ciphertext is a retained reference to exactly this version.
	vs.refs++
	vs.lastRef = g.now
	g.cacheSetLocked(name, vs.meta)

	return Ciphertext{
		Tenant:  tenant,
		KeyID:   keyID,
		Version: vs.meta.Version,
		Blob:    blob,
	}, nil
}

// Decrypt opens a ciphertext with exactly the version it was written with,
// which stays valid while the version exists.
func (g *RotationGuard) Decrypt(ct Ciphertext) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: ct.Tenant, keyID: ct.KeyID}
	ring, ok := g.keys[name]
	if !ok {
		return nil, ErrNoKey
	}
	if ring.purpose != PurposeEncrypt {
		return nil, ErrPurpose
	}
	if !g.hsmUp {
		return nil, ErrHSMDown
	}

	vs, ok := ring.versions[ct.Version]
	if !ok {
		return nil, ErrUnknownVersion
	}
	if vs.meta.State == StateDestroyed {
		return nil, ErrVersionDestroyed
	}

	plaintext, err := open(vs.material, ct.Tenant, ct.KeyID, ct.Version, ct.Blob)
	if err != nil {
		return nil, err
	}

	// A successful decrypt proves that this historical version is still in
	// use, so renew its retention clock.
	if vs.refs == 0 {
		vs.refs = 1
	}
	vs.lastRef = g.now
	return plaintext, nil
}

// Sign signs msg with a signing key. Encryption keys must be refused.
func (g *RotationGuard) Sign(tenant, keyID string, msg []byte) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	ring, ok := g.keys[name]
	if !ok {
		return nil, ErrNoKey
	}
	if ring.purpose != PurposeSign {
		return nil, ErrPurpose
	}
	if !g.hsmUp {
		return nil, ErrHSMDown
	}

	vs := ring.versions[ring.activeVersion]
	if vs == nil || vs.meta.State != StateActive {
		return nil, ErrUnknownVersion
	}

	mac := hmac.New(sha256.New, deriveKey(vs.material, PurposeSign))
	if _, err := mac.Write(msg); err != nil {
		return nil, err
	}
	return mac.Sum(nil), nil
}

// Rotate creates a new active version and retires the previous one. Two calls
// made at the same guard time must produce a single new version.
func (g *RotationGuard) Rotate(tenant, keyID string) (KeyVersion, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	ring, ok := g.keys[name]
	if !ok {
		return KeyVersion{}, ErrNoKey
	}
	if !g.hsmUp {
		return KeyVersion{}, ErrHSMDown
	}

	if g.failNextRotate[name] {
		delete(g.failNextRotate, name)
		return KeyVersion{}, ErrRotationFailed
	}

	// Rotation is idempotent within one guard-clock tick. This turns
	// concurrent rotations into one version transition.
	if !ring.lastRotatedAt.IsZero() && ring.lastRotatedAt.Equal(g.now) {
		vs := ring.versions[ring.activeVersion]
		if vs == nil {
			return KeyVersion{}, ErrUnknownVersion
		}
		return vs.meta, nil
	}

	previous := ring.versions[ring.activeVersion]
	if previous == nil || previous.meta.State != StateActive {
		return KeyVersion{}, ErrUnknownVersion
	}

	next := ring.nextVersion
	if next < 2 {
		next = 2
	}
	ring.nextVersion = next + 1

	previous.meta.State = StateRetired

	vs := &versionState{
		meta: KeyVersion{
			Tenant:    tenant,
			KeyID:     keyID,
			Version:   next,
			Purpose:   ring.purpose,
			State:     StateActive,
			CreatedAt: g.now,
			HSMHandle: g.newHandleLocked(),
		},
		material: cloneBytes(previous.material),
	}
	ring.versions[next] = vs
	ring.activeVersion = next
	ring.lastRotatedAt = g.now
	g.cacheSetLocked(name, vs.meta)
	return vs.meta, nil
}

// Destroy removes a retired version. The caller must pass the confirmation
// string "CONFIRM" and the active version may never be destroyed.
func (g *RotationGuard) Destroy(tenant, keyID string, version int, confirm string) error {
	if confirm != confirmDestroy {
		return ErrConfirmNeeded
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	ring, ok := g.keys[name]
	if !ok {
		return ErrNoKey
	}
	if !g.hsmUp {
		return ErrHSMDown
	}

	vs, ok := ring.versions[version]
	if !ok {
		return ErrUnknownVersion
	}
	if vs.meta.State == StateDestroyed {
		return ErrVersionDestroyed
	}
	if version == ring.activeVersion || vs.meta.State == StateActive {
		return ErrActiveDestroy
	}

	g.pruneVersionRefsLocked(vs)
	if vs.refs > 0 {
		return &ReferenceCountError{
			Tenant:  tenant,
			KeyID:   keyID,
			Version: version,
			Count:   vs.refs,
		}
	}

	// Retain the tombstone in the version table, but reclaim its material
	// and reference index so the per-version reference state does not grow.
	vs.meta.State = StateDestroyed
	vs.meta.HSMHandle = ""
	vs.material = nil
	vs.refs = 0
	vs.lastRef = time.Time{}
	return nil
}

// Versions lists the versions of one tenant key ordered by version.
func (g *RotationGuard) Versions(tenant, keyID string) []KeyVersion {
	g.mu.Lock()
	defer g.mu.Unlock()

	ring, ok := g.keys[keyName{tenant: tenant, keyID: keyID}]
	if !ok {
		return []KeyVersion{}
	}

	numbers := make([]int, 0, len(ring.versions))
	for number := range ring.versions {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)

	out := make([]KeyVersion, 0, len(numbers))
	for _, number := range numbers {
		out = append(out, ring.versions[number].meta)
	}
	return out
}

// Version returns one version of one tenant key.
func (g *RotationGuard) Version(tenant, keyID string, v int) (KeyVersion, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ring, ok := g.keys[keyName{tenant: tenant, keyID: keyID}]
	if !ok {
		return KeyVersion{}, false
	}
	vs, ok := ring.versions[v]
	if !ok {
		return KeyVersion{}, false
	}
	return vs.meta, true
}

// DropHSM simulates losing the HSM session.
func (g *RotationGuard) DropHSM() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.hsmUp = false
	g.hsmGeneration++
	return nil
}

// Reconnect re-establishes the HSM session and revalidates existing handles.
func (g *RotationGuard) Reconnect() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.hsmGeneration++
	g.hsmUp = true

	// Handles are session scoped. Revalidation replaces every live handle;
	// no operation is allowed to fall back to a pre-disconnect handle.
	for name, ring := range g.keys {
		for _, vs := range ring.versions {
			if vs.meta.State == StateDestroyed {
				vs.meta.HSMHandle = ""
				continue
			}
			vs.meta.HSMHandle = g.newHandleLocked()
		}
		if active := ring.versions[ring.activeVersion]; active != nil {
			g.cacheSetLocked(name, active.meta)
		}
	}
	return nil
}

// HSMUp reports whether the HSM session is usable.
func (g *RotationGuard) HSMUp() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hsmUp
}

type backupState struct {
	Schema    int         `json:"schema"`
	Now       int64       `json:"now"`
	CacheTTL  int64       `json:"cache_ttl"`
	Retention int64       `json:"retention"`
	Keys      []backupKey `json:"keys"`
}

type backupKey struct {
	Tenant        string          `json:"tenant"`
	KeyID         string          `json:"key_id"`
	Purpose       string          `json:"purpose"`
	ActiveVersion int             `json:"active_version"`
	NextVersion   int             `json:"next_version"`
	LastRotatedAt int64           `json:"last_rotated_at"`
	Versions      []backupVersion `json:"versions"`
}

type backupVersion struct {
	Version    int    `json:"version"`
	State      string `json:"state"`
	CreatedAt  int64  `json:"created_at"`
	Material   string `json:"material"`
	References int    `json:"references"`
	LastRef    int64  `json:"last_ref"`
}

// Backup exports the key state outside the primary fault domain. Raw key
// material must not appear verbatim in the result.
func (g *RotationGuard) Backup() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	doc := backupState{
		Schema:    1,
		Now:       g.now.UnixNano(),
		CacheTTL:  int64(g.cacheTTL),
		Retention: int64(g.retention),
		Keys:      make([]backupKey, 0, len(g.keys)),
	}

	names := make([]keyName, 0, len(g.keys))
	for name := range g.keys {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].tenant == names[j].tenant {
			return names[i].keyID < names[j].keyID
		}
		return names[i].tenant < names[j].tenant
	})

	for _, name := range names {
		ring := g.keys[name]
		bk := backupKey{
			Tenant:        ring.tenant,
			KeyID:         ring.keyID,
			Purpose:       ring.purpose,
			ActiveVersion: ring.activeVersion,
			NextVersion:   ring.nextVersion,
			LastRotatedAt: unixNano(ring.lastRotatedAt),
			Versions:      make([]backupVersion, 0, len(ring.versions)),
		}

		numbers := make([]int, 0, len(ring.versions))
		for number := range ring.versions {
			numbers = append(numbers, number)
		}
		sort.Ints(numbers)

		for _, number := range numbers {
			vs := ring.versions[number]
			bv := backupVersion{
				Version:    number,
				State:      vs.meta.State,
				CreatedAt:  unixNano(vs.meta.CreatedAt),
				References: vs.refs,
				LastRef:    unixNano(vs.lastRef),
			}
			if vs.meta.State != StateDestroyed {
				wrapped, err := wrapMaterial(vs.material)
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrBackup, err)
				}
				bv.Material = wrapped
			}
			bk.Versions = append(bk.Versions, bv)
		}
		doc.Keys = append(doc.Keys, bk)
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBackup, err)
	}
	return []byte(backupPrefix + base64.StdEncoding.EncodeToString(raw)), nil
}

// Restore loads the state produced by Backup.
func (g *RotationGuard) Restore(state []byte) error {
	rawState := string(state)
	if !bytes.HasPrefix([]byte(rawState), []byte(backupPrefix)) {
		return fmt.Errorf("%w: missing backup header", ErrBackup)
	}
	raw, err := base64.StdEncoding.DecodeString(rawState[len(backupPrefix):])
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBackup, err)
	}

	var doc backupState
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: %v", ErrBackup, err)
	}
	if doc.Schema != 1 {
		return fmt.Errorf("%w: unsupported schema %d", ErrBackup, doc.Schema)
	}

	restored := make(map[keyName]*keyRing, len(doc.Keys))
	for _, bk := range doc.Keys {
		if bk.Purpose != PurposeEncrypt && bk.Purpose != PurposeSign {
			return fmt.Errorf("%w: invalid purpose", ErrBackup)
		}
		name := keyName{tenant: bk.Tenant, keyID: bk.KeyID}
		if _, exists := restored[name]; exists {
			return fmt.Errorf("%w: duplicate key", ErrBackup)
		}

		ring := &keyRing{
			tenant:        bk.Tenant,
			keyID:         bk.KeyID,
			purpose:       bk.Purpose,
			versions:      make(map[int]*versionState, len(bk.Versions)),
			activeVersion: bk.ActiveVersion,
			nextVersion:   bk.NextVersion,
			lastRotatedAt: fromUnixNano(bk.LastRotatedAt),
		}

		maxVersion := 0
		for _, bv := range bk.Versions {
			if bv.Version <= 0 {
				return fmt.Errorf("%w: invalid version", ErrBackup)
			}
			if _, exists := ring.versions[bv.Version]; exists {
				return fmt.Errorf("%w: duplicate version", ErrBackup)
			}
			if bv.State != StateActive && bv.State != StateRetired && bv.State != StateDestroyed {
				return fmt.Errorf("%w: invalid version state", ErrBackup)
			}
			if bv.References < 0 {
				return fmt.Errorf("%w: invalid reference count", ErrBackup)
			}

			vs := &versionState{
				meta: KeyVersion{
					Tenant:    bk.Tenant,
					KeyID:     bk.KeyID,
					Version:   bv.Version,
					Purpose:   bk.Purpose,
					State:     bv.State,
					CreatedAt: fromUnixNano(bv.CreatedAt),
				},
				refs:    bv.References,
				lastRef: fromUnixNano(bv.LastRef),
			}
			if bv.State == StateDestroyed {
				if bv.Material != "" {
					return fmt.Errorf("%w: destroyed version has material", ErrBackup)
				}
			} else {
				material, err := unwrapMaterial(bv.Material)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrBackup, err)
				}
				vs.material = material
			}
			ring.versions[bv.Version] = vs
			if bv.Version > maxVersion {
				maxVersion = bv.Version
			}
		}

		if len(ring.versions) == 0 {
			return fmt.Errorf("%w: key has no versions", ErrBackup)
		}
		active := ring.versions[ring.activeVersion]
		if active == nil || active.meta.State != StateActive {
			return fmt.Errorf("%w: invalid active version", ErrBackup)
		}
		if ring.nextVersion <= maxVersion {
			ring.nextVersion = maxVersion + 1
		}
		restored[name] = ring
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.now = fromUnixNano(doc.Now)
	g.cacheTTL = time.Duration(doc.CacheTTL)
	g.retention = time.Duration(doc.Retention)
	g.keys = restored
	g.cache = make(map[keyName]cacheEntry)
	g.failNextRotate = make(map[keyName]bool)
	g.hsmUp = true
	g.hsmGeneration++

	for name, ring := range g.keys {
		for _, vs := range ring.versions {
			if vs.meta.State == StateDestroyed {
				vs.meta.HSMHandle = ""
				continue
			}
			vs.meta.HSMHandle = g.newHandleLocked()
		}
		active := ring.versions[ring.activeVersion]
		g.cacheSetLocked(name, active.meta)
	}
	return nil
}

// CacheGet returns the cached active version for a key.
func (g *RotationGuard) CacheGet(tenant, keyID string) (KeyVersion, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	entry, ok := g.cache[name]
	if !ok {
		return KeyVersion{}, false
	}
	if g.cacheTTL > 0 && !g.now.Before(entry.cachedAt.Add(g.cacheTTL)) {
		delete(g.cache, name)
		return KeyVersion{}, false
	}
	return entry.version, true
}

// SetCacheTTL configures how long a cached version stays valid.
func (g *RotationGuard) SetCacheTTL(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cacheTTL = d
}

// SetRetention configures how long references protect a retired version.
// A non-positive duration means references never age out automatically.
func (g *RotationGuard) SetRetention(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.retention = d
}

// SetReferenceRetention is an alias for SetRetention.
func (g *RotationGuard) SetReferenceRetention(d time.Duration) {
	g.SetRetention(d)
}

// Advance moves the guard clock forward.
func (g *RotationGuard) Advance(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.now = g.now.Add(d)
}

// FailNextRotate makes the next rotation of one key fail once.
func (g *RotationGuard) FailNextRotate(tenant, keyID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := keyName{tenant: tenant, keyID: keyID}
	if _, ok := g.keys[name]; !ok {
		return ErrNoKey
	}
	g.failNextRotate[name] = true
	return nil
}

func (g *RotationGuard) cacheSetLocked(name keyName, version KeyVersion) {
	g.cache[name] = cacheEntry{version: version, cachedAt: g.now}
}

func (g *RotationGuard) newHandleLocked() string {
	g.handleSequence++
	return fmt.Sprintf("hsm:%d:%d", g.hsmGeneration, g.handleSequence)
}

func (g *RotationGuard) pruneVersionRefsLocked(vs *versionState) {
	if vs.refs == 0 || g.retention <= 0 || vs.lastRef.IsZero() {
		return
	}
	if !g.now.Before(vs.lastRef.Add(g.retention)) {
		vs.refs = 0
		vs.lastRef = time.Time{}
	}
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func validAESKeyLength(n int) bool {
	return n == 16 || n == 24 || n == 32
}

func deriveKey(material []byte, purpose string) []byte {
	h := sha256.New()
	h.Write([]byte("gsb/batch45/kms/v1"))
	h.Write([]byte{0})
	h.Write([]byte(purpose))
	h.Write([]byte{0})
	h.Write(material)
	return h.Sum(nil)
}

func cipherAAD(tenant, keyID string, version int) []byte {
	var buf bytes.Buffer
	buf.WriteString("gsb/batch45/ciphertext/v1")
	buf.WriteByte(0)
	buf.WriteString(tenant)
	buf.WriteByte(0)
	buf.WriteString(keyID)
	buf.WriteByte(0)
	var versionBytes [8]byte
	binary.BigEndian.PutUint64(versionBytes[:], uint64(version))
	buf.Write(versionBytes[:])
	return buf.Bytes()
}

func seal(material []byte, tenant, keyID string, version int, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveKey(material, PurposeEncrypt))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, cipherAAD(tenant, keyID, version)), nil
}

func open(material []byte, tenant, keyID string, version int, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveKey(material, PurposeEncrypt))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < aead.NonceSize() {
		return nil, ErrCiphertext
	}

	nonce := blob[:aead.NonceSize()]
	ciphertext := blob[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, cipherAAD(tenant, keyID, version))
	if err != nil {
		return nil, ErrCiphertext
	}
	return plaintext, nil
}

var backupWrappingKey = sha256.Sum256([]byte("gsb/batch45/kms/backup-wrapping-key/v1"))

func wrapMaterial(material []byte) (string, error) {
	block, err := aes.NewCipher(backupWrappingKey[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, material, []byte("gsb/batch45/kms/backup/v1"))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func unwrapMaterial(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, errors.New("missing wrapped material")
	}
	wrapped, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(backupWrappingKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < aead.NonceSize() {
		return nil, errors.New("truncated wrapped material")
	}
	nonce := wrapped[:aead.NonceSize()]
	material, err := aead.Open(nil, nonce, wrapped[aead.NonceSize():], []byte("gsb/batch45/kms/backup/v1"))
	if err != nil {
		return nil, err
	}
	return material, nil
}

func unixNano(t time.Time) int64 {
	if t.IsZero() {
		return zeroTimeNanos
	}
	return t.UnixNano()
}

func fromUnixNano(n int64) time.Time {
	if n == zeroTimeNanos {
		return time.Time{}
	}
	return time.Unix(0, n)
}
