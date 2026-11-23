package task

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-20"
)

func newRotationGuard(t *testing.T) *RotationGuard {
	t.Helper()
	g, err := NewRotationGuard(time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("NewRotationGuard: %v", err)
	}
	return g
}

func keyMaterial() []byte {
	return bytes.Repeat([]byte{0x11}, 32)
}

func mustCreateKey(t *testing.T, g *RotationGuard, tenant, keyID, purpose string) KeyVersion {
	t.Helper()
	kv, err := g.CreateKey(tenant, keyID, purpose, keyMaterial())
	if err != nil {
		t.Fatalf("CreateKey(%s/%s): %v", tenant, keyID, err)
	}
	return kv
}

func TestCiphertextBindsVersion(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	ct, err := g.Encrypt("t1", "k1", []byte("hello"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if ct.Version != 1 {
		t.Fatalf("ciphertext version = %d, want 1", ct.Version)
	}
	g.Advance(time.Second)
	if _, err := g.Rotate("t1", "k1"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	plain, err := g.Decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt old ciphertext after rotation: %v", err)
	}
	if string(plain) != "hello" {
		t.Fatalf("decrypted = %q, want hello", plain)
	}
}

func TestRotationKeepsOldVersion(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	g.Advance(time.Second)
	if _, err := g.Rotate("t1", "k1"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	versions := g.Versions("t1", "k1")
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2 (old version must stay in the window)", len(versions))
	}
	if versions[0].Version != 1 || versions[0].State != StateRetired {
		t.Fatalf("old version = %+v, want retired v1", versions[0])
	}
	if versions[1].Version != 2 || versions[1].State != StateActive {
		t.Fatalf("new version = %+v, want active v2", versions[1])
	}
}

func TestConcurrentRotateSingleVersion(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	var wg sync.WaitGroup
	versions := make([]int, 10)
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kv, err := g.Rotate("t1", "k1")
			versions[i] = kv.Version
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("rotate %d: %v", i, errs[i])
		}
		if versions[i] != 2 {
			t.Fatalf("rotate %d returned version %d, want 2", i, versions[i])
		}
	}
	if got := len(g.Versions("t1", "k1")); got != 2 {
		t.Fatalf("versions = %d after concurrent rotation, want 2", got)
	}
}

func TestPurposeSeparation(t *testing.T) {
	g := newRotationGuard(t)
	enc := mustCreateKey(t, g, "t1", "enc-key", PurposeEncrypt)
	sign := mustCreateKey(t, g, "t1", "sign-key", PurposeSign)
	if _, err := g.Sign("t1", enc.KeyID, []byte("msg")); !errors.Is(err, ErrPurpose) {
		t.Fatalf("signing with an encryption key error = %v, want ErrPurpose", err)
	}
	sig, err := g.Sign("t1", sign.KeyID, []byte("msg"))
	if err != nil {
		t.Fatalf("signing with a signing key: %v", err)
	}
	if len(sig) == 0 {
		t.Fatalf("empty signature")
	}
	if _, err := g.Encrypt("t1", sign.KeyID, []byte("msg")); !errors.Is(err, ErrPurpose) {
		t.Fatalf("encrypting with a signing key error = %v, want ErrPurpose", err)
	}
}

func TestHSMReconnect(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	ct, err := g.Encrypt("t1", "k1", []byte("payload"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := g.DropHSM(); err != nil {
		t.Fatalf("drop HSM: %v", err)
	}
	if g.HSMUp() {
		t.Fatalf("HSM still reported up after drop")
	}
	if _, err := g.Encrypt("t1", "k1", []byte("payload")); !errors.Is(err, ErrHSMDown) {
		t.Fatalf("encrypt with a dead HSM session error = %v, want ErrHSMDown", err)
	}
	if err := g.Reconnect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if !g.HSMUp() {
		t.Fatalf("HSM not up after reconnect")
	}
	if _, err := g.Encrypt("t1", "k1", []byte("payload")); err != nil {
		t.Fatalf("encrypt after reconnect: %v", err)
	}
	plain, err := g.Decrypt(ct)
	if err != nil || string(plain) != "payload" {
		t.Fatalf("decrypt after reconnect = %q, %v", plain, err)
	}
}

func TestImportValidatesMaterialLength(t *testing.T) {
	g := newRotationGuard(t)
	if _, err := g.ImportKey("t1", "imported", make([]byte, 5)); !errors.Is(err, ErrImportLength) {
		t.Fatalf("short import error = %v, want ErrImportLength", err)
	}
	if got := len(g.Versions("t1", "imported")); got != 0 {
		t.Fatalf("versions = %d after rejected import, want 0", got)
	}
	if _, err := g.ImportKey("t1", "imported", keyMaterial()); err != nil {
		t.Fatalf("valid import: %v", err)
	}
	if got := len(g.Versions("t1", "imported")); got != 1 {
		t.Fatalf("versions = %d after import, want 1", got)
	}
}

func TestDestroyRequiresConfirmation(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	g.Advance(time.Second)
	if _, err := g.Rotate("t1", "k1"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := g.Destroy("t1", "k1", 1, ""); !errors.Is(err, ErrConfirmNeeded) {
		t.Fatalf("destroy without confirmation error = %v, want ErrConfirmNeeded", err)
	}
	if err := g.Destroy("t1", "k1", 2, "CONFIRM"); !errors.Is(err, ErrActiveDestroy) {
		t.Fatalf("destroying the active version error = %v, want ErrActiveDestroy", err)
	}
	if err := g.Destroy("t1", "k1", 1, "CONFIRM"); err != nil {
		t.Fatalf("destroy retired version: %v", err)
	}
	kv, ok := g.Version("t1", "k1", 1)
	if !ok || kv.State != StateDestroyed {
		t.Fatalf("destroyed version = %+v, want state destroyed", kv)
	}
}

func TestBackupOutsidePrimaryFaultDomain(t *testing.T) {
	g := newRotationGuard(t)
	material := keyMaterial()
	if _, err := g.CreateKey("t1", "k1", PurposeEncrypt, material); err != nil {
		t.Fatalf("create: %v", err)
	}
	ct, err := g.Encrypt("t1", "k1", []byte("secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	backup, err := g.Backup()
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if bytes.Contains(backup, material) {
		t.Fatalf("backup contains raw key material")
	}
	restored := newRotationGuard(t)
	if err := restored.Restore(backup); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(restored.Versions("t1", "k1")); got != 1 {
		t.Fatalf("restored versions = %d, want 1", got)
	}
	plain, err := restored.Decrypt(ct)
	if err != nil || string(plain) != "secret" {
		t.Fatalf("restored decrypt = %q, %v", plain, err)
	}
}

func TestCacheExpiry(t *testing.T) {
	g := newRotationGuard(t)
	g.SetCacheTTL(time.Minute)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	cached, ok := g.CacheGet("t1", "k1")
	if !ok || cached.Version != 1 {
		t.Fatalf("cache = %+v/%v, want v1", cached, ok)
	}
	g.Advance(2 * time.Minute)
	if _, ok := g.CacheGet("t1", "k1"); ok {
		t.Fatalf("cache entry did not expire")
	}
}

func TestCrossTenantKeyIDs(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "tenant-a", "shared-id", PurposeEncrypt)
	mustCreateKey(t, g, "tenant-b", "shared-id", PurposeEncrypt)
	ctA, err := g.Encrypt("tenant-a", "shared-id", []byte("a-secret"))
	if err != nil {
		t.Fatalf("encrypt tenant a: %v", err)
	}
	ctB, err := g.Encrypt("tenant-b", "shared-id", []byte("b-secret"))
	if err != nil {
		t.Fatalf("encrypt tenant b: %v", err)
	}
	g.Advance(time.Second)
	if _, err := g.Rotate("tenant-a", "shared-id"); err != nil {
		t.Fatalf("rotate tenant a: %v", err)
	}
	if got := len(g.Versions("tenant-a", "shared-id")); got != 2 {
		t.Fatalf("tenant a versions = %d, want 2", got)
	}
	if got := len(g.Versions("tenant-b", "shared-id")); got != 1 {
		t.Fatalf("tenant b versions = %d, want 1 (namespace collision)", got)
	}
	plainA, err := g.Decrypt(ctA)
	if err != nil || string(plainA) != "a-secret" {
		t.Fatalf("tenant a decrypt = %q, %v", plainA, err)
	}
	plainB, err := g.Decrypt(ctB)
	if err != nil || string(plainB) != "b-secret" {
		t.Fatalf("tenant b decrypt = %q, %v", plainB, err)
	}
}

func TestRotateFailureRollsBack(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	g.Advance(time.Second)
	if err := g.FailNextRotate("t1", "k1"); err != nil {
		t.Fatalf("arm rotation failure: %v", err)
	}
	if _, err := g.Rotate("t1", "k1"); !errors.Is(err, ErrRotationFailed) {
		t.Fatalf("failed rotation error = %v, want ErrRotationFailed", err)
	}
	versions := g.Versions("t1", "k1")
	if len(versions) != 1 {
		t.Fatalf("versions = %d after failed rotation, want 1", len(versions))
	}
	if versions[0].Version != 1 || versions[0].State != StateActive {
		t.Fatalf("active version = %+v, want v1 active", versions[0])
	}
	cached, ok := g.CacheGet("t1", "k1")
	if !ok || cached.Version != 1 {
		t.Fatalf("cache = %+v/%v after failed rotation, want v1", cached, ok)
	}
}

func TestRestoreAfterRestart(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	g.Advance(time.Second)
	if _, err := g.Rotate("t1", "k1"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	ct, err := g.Encrypt("t1", "k1", []byte("persisted"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	backup, err := g.Backup()
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	restarted := newRotationGuard(t)
	if err := restarted.Restore(backup); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(restarted.Versions("t1", "k1")); got != 2 {
		t.Fatalf("restored versions = %d, want 2", got)
	}
	if plain, err := restarted.Decrypt(ct); err != nil || string(plain) != "persisted" {
		t.Fatalf("decrypt after restore = %q, %v", plain, err)
	}
	restarted.Advance(time.Second)
	kv, err := restarted.Rotate("t1", "k1")
	if err != nil {
		t.Fatalf("rotate after restore: %v", err)
	}
	if kv.Version != 3 {
		t.Fatalf("next version after restore = %d, want 3", kv.Version)
	}
}

func TestDecryptUnknownVersionRejected(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	ct, err := g.Encrypt("t1", "k1", []byte("data"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := g.Decrypt(Ciphertext{Tenant: "t1", KeyID: "k1", Version: 99, Blob: ct.Blob}); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("unknown version error = %v, want ErrUnknownVersion", err)
	}
	if plain, err := g.Decrypt(ct); err != nil || string(plain) != "data" {
		t.Fatalf("valid decrypt = %q, %v", plain, err)
	}
}

func TestVersionMonotonic(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	g.Advance(time.Second)
	if _, err := g.Rotate("t1", "k1"); err != nil {
		t.Fatalf("rotate 1: %v", err)
	}
	g.Advance(time.Second)
	if _, err := g.Rotate("t1", "k1"); err != nil {
		t.Fatalf("rotate 2: %v", err)
	}
	versions := g.Versions("t1", "k1")
	if len(versions) != 3 {
		t.Fatalf("versions = %d, want 3", len(versions))
	}
	for i, kv := range versions {
		if kv.Version != i+1 {
			t.Fatalf("version at %d = %d, want %d", i, kv.Version, i+1)
		}
	}
	if versions[2].State != StateActive {
		t.Fatalf("latest version state = %q, want active", versions[2].State)
	}
	if versions[0].State != StateRetired || versions[1].State != StateRetired {
		t.Fatalf("older versions states = %q,%q, want retired", versions[0].State, versions[1].State)
	}
}

func TestCacheInvalidatedOnRotate(t *testing.T) {
	g := newRotationGuard(t)
	mustCreateKey(t, g, "t1", "k1", PurposeEncrypt)
	if _, err := g.Encrypt("t1", "k1", []byte("warm")); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cached, ok := g.CacheGet("t1", "k1")
	if !ok || cached.Version != 1 {
		t.Fatalf("cache = %+v/%v, want v1", cached, ok)
	}
	g.Advance(time.Second)
	if _, err := g.Rotate("t1", "k1"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	cached, ok = g.CacheGet("t1", "k1")
	if !ok {
		t.Fatalf("cache empty after rotation")
	}
	if cached.Version != 2 {
		t.Fatalf("cached version = %d after rotation, want 2", cached.Version)
	}
}
