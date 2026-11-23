package task

import (
	"errors"
	"strings"
	"testing"

	task "gsb/batch45-safety-b-23"
)

func newGuard() *task.FsverityGuard {
	return task.NewFsverityGuard(task.GuardOptions{
		TrustedKeys:          map[string]bool{"key-new": true},
		MaxDescriptorVersion: 2,
	})
}

func validFile() task.FileState {
	return task.FileState{
		Path:          "/usr/local/bin/app",
		Dev:           2049,
		Ino:           77,
		Size:          4096,
		ContentSHA256: strings.Repeat("a", 64),
		VerityEnabled: true,
		Descriptor: task.VerityDescriptor{
			Version:    1,
			Algorithm:  task.AlgorithmVerity,
			RootDigest: strings.Repeat("a", 64),
			FileSize:   4096,
		},
	}
}

func TestVerifyFileRejectsWhenVerityDisabled(t *testing.T) {
	g := newGuard()
	st := validFile()
	st.VerityEnabled = false
	_, err := g.VerifyFile(st)
	if !errors.Is(err, task.ErrVerityDisabled) {
		t.Fatalf("verity disabled must be an error, not a silent pass, got %v", err)
	}
}

func TestVerifyFileRejectsDigestMismatch(t *testing.T) {
	g := newGuard()
	st := validFile()
	st.Descriptor.RootDigest = strings.Repeat("b", 64)
	_, err := g.VerifyFile(st)
	if !errors.Is(err, task.ErrVerityMismatch) {
		t.Fatalf("descriptor must be bound to file content, got %v", err)
	}
}

func TestVerifySignatureRejectsUncoveredDescriptorVersion(t *testing.T) {
	g := newGuard()
	sig := task.VeritySignature{KeyID: "key-new", DescriptorVersion: 1, Value: []byte("sig")}
	desc := task.VerityDescriptor{Version: 2, Algorithm: task.AlgorithmVerity, RootDigest: "root"}
	err := g.VerifySignature(sig, desc)
	if !errors.Is(err, task.ErrDescriptorVersionMismatch) {
		t.Fatalf("a signature over version 1 must not cover version 2, got %v", err)
	}
}

func TestVerifySignatureRejectsRotatedOutKey(t *testing.T) {
	g := newGuard()
	sig := task.VeritySignature{KeyID: "key-old", DescriptorVersion: 1, Value: []byte("sig")}
	desc := task.VerityDescriptor{Version: 1, Algorithm: task.AlgorithmVerity, RootDigest: "root"}
	err := g.VerifySignature(sig, desc)
	if !errors.Is(err, task.ErrUntrustedKey) {
		t.Fatalf("a key removed by rotation must be rejected, got %v", err)
	}
}

func TestVerifySignatureAcceptsCurrentKey(t *testing.T) {
	g := newGuard()
	sig := task.VeritySignature{KeyID: "key-new", DescriptorVersion: 1, Value: []byte("sig")}
	desc := task.VerityDescriptor{Version: 1, Algorithm: task.AlgorithmVerity, RootDigest: "root"}
	if err := g.VerifySignature(sig, desc); err != nil {
		t.Fatalf("signature from a trusted key must verify, got %v", err)
	}
}

func TestCacheHitByInodeAcrossRename(t *testing.T) {
	g := newGuard()
	stored := validFile()
	if err := g.CacheStore(stored, task.Verdict{Trusted: true}); err != nil {
		t.Fatalf("CacheStore returned unexpected error: %v", err)
	}
	moved := stored
	moved.Path = "/usr/local/bin/app.renamed"
	v, ok := g.CacheLookup(moved)
	if !ok || !v.Trusted {
		t.Fatalf("cache is keyed by inode, so a rename must still hit, ok=%v verdict=%+v", ok, v)
	}
}

func TestCacheMissWhenContentChangedSameInode(t *testing.T) {
	g := newGuard()
	if err := g.CacheStore(validFile(), task.Verdict{Trusted: true}); err != nil {
		t.Fatalf("CacheStore returned unexpected error: %v", err)
	}
	replaced := validFile()
	replaced.ContentSHA256 = strings.Repeat("c", 64)
	if v, ok := g.CacheLookup(replaced); ok {
		t.Fatalf("a replaced file must not reuse the cache entry, got %+v", v)
	}
}

func TestCacheMissForDifferentInodeSamePath(t *testing.T) {
	g := newGuard()
	if err := g.CacheStore(validFile(), task.Verdict{Trusted: true}); err != nil {
		t.Fatalf("CacheStore returned unexpected error: %v", err)
	}
	other := validFile()
	other.Ino = 78
	if v, ok := g.CacheLookup(other); ok {
		t.Fatalf("a different inode at the same path must miss the cache, got %+v", v)
	}
}

func TestVerifyImageRequiresAllArches(t *testing.T) {
	g := newGuard()
	img := task.ImageSpec{
		Ref:            "registry.local/app:1",
		RequiredArches: []string{"amd64", "arm64"},
		Layers: []task.LayerSpec{
			{
				Digest:        "sha256:1",
				Arch:          "amd64",
				ContentSHA256: strings.Repeat("a", 64),
				VerityEnabled: true,
				Verified:      true,
				Descriptor:    task.VerityDescriptor{Version: 1, Algorithm: task.AlgorithmVerity, RootDigest: strings.Repeat("a", 64)},
			},
			{
				Digest:        "sha256:2",
				Arch:          "arm64",
				ContentSHA256: strings.Repeat("b", 64),
				VerityEnabled: false,
				Verified:      false,
			},
		},
	}
	verdict, err := g.VerifyImage(img)
	if err != nil {
		t.Fatalf("VerifyImage returned unexpected error: %v", err)
	}
	if verdict.Trusted {
		t.Fatalf("every required architecture must be verified, got %+v", verdict)
	}
	if len(verdict.Reasons) == 0 {
		t.Fatalf("rejection must explain which architecture failed")
	}
}

func TestVerifyImageRejectsUnverifiedLayer(t *testing.T) {
	g := newGuard()
	img := task.ImageSpec{
		Ref:            "registry.local/app:1",
		RequiredArches: []string{"amd64"},
		Layers: []task.LayerSpec{{
			Digest:        "sha256:1",
			Arch:          "amd64",
			ContentSHA256: strings.Repeat("a", 64),
			VerityEnabled: true,
			Verified:      false,
			Descriptor:    task.VerityDescriptor{Version: 1, Algorithm: task.AlgorithmVerity, RootDigest: strings.Repeat("a", 64)},
		}},
	}
	verdict, err := g.VerifyImage(img)
	if err != nil {
		t.Fatalf("VerifyImage returned unexpected error: %v", err)
	}
	if verdict.Trusted {
		t.Fatalf("an extracted but unverified layer must not be trusted, got %+v", verdict)
	}
}

func TestNoFallbackToPlainHash(t *testing.T) {
	g := newGuard()
	st := validFile()
	st.Descriptor.Algorithm = "sha256"
	_, err := g.VerifyFile(st)
	if !errors.Is(err, task.ErrFallbackForbidden) {
		t.Fatalf("verification must not fall back to a plain hash, got %v", err)
	}
}

func TestVerifyReadDetectsConcurrentModification(t *testing.T) {
	g := newGuard()
	st := validFile()
	st.ContentSHA256 = strings.Repeat("0", 64)
	_, err := g.VerifyRead(st, []string{"hello ", "world"})
	if !errors.Is(err, task.ErrVerityMismatch) {
		t.Fatalf("bytes read must be re-hashed against the descriptor, got %v", err)
	}
}

func TestExportImportCacheSurvivesRestart(t *testing.T) {
	g := newGuard()
	if err := g.CacheStore(validFile(), task.Verdict{Trusted: true}); err != nil {
		t.Fatalf("CacheStore returned unexpected error: %v", err)
	}
	data, err := g.ExportCache()
	if err != nil {
		t.Fatalf("ExportCache returned unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("ExportCache returned an empty payload")
	}
	restarted := newGuard()
	if err := restarted.ImportCache(data); err != nil {
		t.Fatalf("ImportCache returned unexpected error: %v", err)
	}
	if v, ok := restarted.CacheLookup(validFile()); !ok || !v.Trusted {
		t.Fatalf("cache must survive a restart, ok=%v verdict=%+v", ok, v)
	}
}

func TestVerifyFileAcceptsValidState(t *testing.T) {
	g := newGuard()
	verdict, err := g.VerifyFile(validFile())
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	if !verdict.Trusted {
		t.Fatalf("a matching descriptor must be trusted, got %+v", verdict)
	}
}

func TestVerifyImageRejectsMissingVerityMetadata(t *testing.T) {
	g := newGuard()
	img := task.ImageSpec{
		Ref:            "registry.local/remote:2",
		RequiredArches: []string{"amd64"},
		Layers: []task.LayerSpec{{
			Digest:        "sha256:9",
			Arch:          "amd64",
			ContentSHA256: strings.Repeat("a", 64),
			VerityEnabled: true,
			Verified:      true,
			Descriptor:    task.VerityDescriptor{Version: 1, Algorithm: task.AlgorithmVerity},
		}},
	}
	verdict, err := g.VerifyImage(img)
	if err != nil {
		t.Fatalf("VerifyImage returned unexpected error: %v", err)
	}
	if verdict.Trusted {
		t.Fatalf("a remote layer without verity metadata must be rejected, got %+v", verdict)
	}
}
