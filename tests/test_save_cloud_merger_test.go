package task

import (
	"errors"
	"strings"
	"testing"

	impl "gsb/batch45-game-a-25"
)

func newSave(version, updatedAt int64, device string, items, quests map[string]int) impl.Save {
	return impl.Save{
		Version:   version,
		UpdatedAt: updatedAt,
		DeviceID:  device,
		Items:     items,
		Quests:    quests,
	}
}

func TestVersionBeatsTimestamp(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(5, 100, "dev-a", map[string]int{"potion": 1}, nil)); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	res, err := m.Merge("slot-1", impl.MergeInput{
		Save:   newSave(3, 999, "dev-b", map[string]int{"potion": 9}, nil),
		Origin: "online",
	})
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	if res.Winner != "cloud" {
		t.Fatalf("merge winner = %q, want cloud (higher version)", res.Winner)
	}
	if res.Version != 5 {
		t.Fatalf("merge version = %d, want 5", res.Version)
	}
}

func TestOfflineHigherVersionWins(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(2, 500, "dev-online", map[string]int{"potion": 1}, nil)); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	res, err := m.Merge("slot-1", impl.MergeInput{
		Save:   newSave(4, 100, "dev-offline", map[string]int{"potion": 2}, nil),
		Origin: "offline",
	})
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	if res.Winner != "incoming" {
		t.Fatalf("merge winner = %q, want incoming offline save with higher version", res.Winner)
	}
	stored, ok := m.Stored("slot-1")
	if !ok {
		t.Fatalf("save missing after merge")
	}
	if stored.Version != 4 {
		t.Fatalf("stored version = %d, want 4", stored.Version)
	}
}

func TestStaleUploadRejected(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(5, 100, "dev-a", nil, nil)); err != nil {
		t.Fatalf("Upload v5 error: %v", err)
	}
	if _, err := m.Upload("slot-1", newSave(4, 200, "dev-a", nil, nil)); !errors.Is(err, impl.ErrStaleVersion) {
		t.Fatalf("stale upload error = %v, want ErrStaleVersion", err)
	}
	stored, _ := m.Stored("slot-1")
	if stored.Version != 5 {
		t.Fatalf("stored version = %d, want 5", stored.Version)
	}
}

func TestSameVersionDifferentDeviceConflicts(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(3, 100, "dev-a", nil, nil)); err != nil {
		t.Fatalf("first upload error: %v", err)
	}
	if _, err := m.Upload("slot-1", newSave(3, 200, "dev-b", nil, nil)); !errors.Is(err, impl.ErrDeviceConflict) {
		t.Fatalf("same version upload error = %v, want ErrDeviceConflict", err)
	}
}

func TestMergeFailureKeepsOriginal(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(1, 100, "dev-a", map[string]int{"potion": 2}, nil)); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	corrupt := newSave(2, 200, "dev-a", map[string]int{"potion": 99}, nil)
	corrupt.Checksum = "bad-checksum"
	if err := m.StoreRaw("slot-1", corrupt); err != nil {
		t.Fatalf("StoreRaw error: %v", err)
	}
	if _, err := m.Merge("slot-1", impl.MergeInput{Save: newSave(3, 300, "dev-b", nil, nil), Origin: "online"}); err == nil {
		t.Fatalf("merge against corrupt stored save unexpectedly succeeded")
	}
	stored, ok := m.Stored("slot-1")
	if !ok {
		t.Fatalf("original save was discarded")
	}
	if stored.Items["potion"] != 2 {
		t.Fatalf("original items changed to %v after failed merge", stored.Items)
	}
}

func TestItemQuantitiesSummed(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(3, 100, "dev-a", map[string]int{"potion": 3, "gem": 1}, nil)); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	res, err := m.Merge("slot-1", impl.MergeInput{
		Save:   newSave(3, 200, "dev-b", map[string]int{"potion": 2, "gem": 4}, nil),
		Origin: "online",
	})
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	stored, _ := m.Stored("slot-1")
	if stored.Items["potion"] != 5 {
		t.Fatalf("potion count = %d, want 5 (sum, not max)", stored.Items["potion"])
	}
	if stored.Items["gem"] != 5 {
		t.Fatalf("gem count = %d, want 5", stored.Items["gem"])
	}
	if res.ItemsTotal != 10 {
		t.Fatalf("items total = %d, want 10", res.ItemsTotal)
	}
}

func TestQuestBranchesMerged(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(2, 100, "dev-a", nil, map[string]int{"main": 2})); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	if _, err := m.Merge("slot-1", impl.MergeInput{
		Save:   newSave(2, 200, "dev-b", nil, map[string]int{"side": 1}),
		Origin: "online",
	}); err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	stored, _ := m.Stored("slot-1")
	if stored.Quests["main"] != 2 {
		t.Fatalf("main quest progress = %d, want 2", stored.Quests["main"])
	}
	if stored.Quests["side"] != 1 {
		t.Fatalf("side quest branch lost: %v", stored.Quests)
	}
}

func TestDownloadDetectsChecksumMismatch(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	bad := newSave(1, 100, "dev-a", map[string]int{"potion": 1}, nil)
	bad.Checksum = "deadbeef"
	if err := m.StoreRaw("slot-1", bad); err != nil {
		t.Fatalf("StoreRaw error: %v", err)
	}
	if _, err := m.Download("slot-1"); !errors.Is(err, impl.ErrChecksumMismatch) {
		t.Fatalf("download tampered save error = %v, want ErrChecksumMismatch", err)
	}
}

func TestUploadSetsChecksum(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	stored, err := m.Upload("slot-1", newSave(1, 100, "dev-a", map[string]int{"potion": 1}, nil))
	if err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	if stored.Checksum == "" {
		t.Fatalf("uploaded save has no checksum")
	}
	if want := m.Checksum(stored); stored.Checksum != want {
		t.Fatalf("uploaded checksum = %q, want %q", stored.Checksum, want)
	}
	if _, err := m.Download("slot-1"); err != nil {
		t.Fatalf("download of uploaded save error: %v", err)
	}
}

func TestTruncatedBlobRejected(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	truncated := newSave(1, 100, "dev-a", map[string]int{"potion": 1}, nil)
	truncated.Checksum = ""
	if err := m.StoreRaw("slot-1", truncated); err != nil {
		t.Fatalf("StoreRaw error: %v", err)
	}
	if _, err := m.Download("slot-1"); !errors.Is(err, impl.ErrCorrupt) {
		t.Fatalf("download truncated save error = %v, want ErrCorrupt", err)
	}
}

func TestKeyRotationKeepsOldSavesReadable(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(1, 100, "dev-a", map[string]int{"potion": 1}, nil)); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	if err := m.RotateKey("k2"); err != nil {
		t.Fatalf("RotateKey error: %v", err)
	}
	if _, err := m.Download("slot-1"); err != nil {
		t.Fatalf("download after key rotation error: %v", err)
	}
}

func TestMergeAuditRecorded(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(1, 100, "dev-a", nil, nil)); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	if _, err := m.Merge("slot-1", impl.MergeInput{Save: newSave(2, 200, "dev-b", nil, nil), Origin: "online"}); err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	joined := strings.Join(m.Audit(), "\n")
	if !strings.Contains(joined, "merge") {
		t.Fatalf("audit log %q missing merge entry", joined)
	}
}

func TestDeleteRemovesCloudCopy(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(1, 100, "dev-a", nil, nil)); err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	if err := m.Delete("slot-1"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if m.Exists("slot-1") {
		t.Fatalf("slot still exists after delete")
	}
	if _, err := m.Download("slot-1"); !errors.Is(err, impl.ErrNotFound) {
		t.Fatalf("download deleted slot error = %v, want ErrNotFound", err)
	}
}

func TestMergeIntoEmptySlotCreates(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	res, err := m.Merge("slot-new", impl.MergeInput{
		Save:   newSave(1, 100, "dev-a", map[string]int{"potion": 1}, nil),
		Origin: "offline",
	})
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	if res.Version != 1 {
		t.Fatalf("merge version = %d, want 1", res.Version)
	}
	if !m.Exists("slot-new") {
		t.Fatalf("merged save was not created in the cloud")
	}
}

func TestHistoryTracksVersions(t *testing.T) {
	m := impl.NewSaveCloudMerger("k1")
	if _, err := m.Upload("slot-1", newSave(1, 100, "dev-a", nil, nil)); err != nil {
		t.Fatalf("first upload error: %v", err)
	}
	if _, err := m.Upload("slot-1", newSave(2, 200, "dev-a", nil, nil)); err != nil {
		t.Fatalf("second upload error: %v", err)
	}
	if got := m.History("slot-1"); got != 2 {
		t.Fatalf("history count = %d, want 2", got)
	}
}
