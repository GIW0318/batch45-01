package task

import "errors"

var (
	ErrNotImplemented   = errors.New("not implemented")
	ErrStaleVersion     = errors.New("stale save version")
	ErrDeviceConflict   = errors.New("same version uploaded from another device")
	ErrChecksumMismatch = errors.New("save checksum mismatch")
	ErrCorrupt          = errors.New("save blob is corrupt")
	ErrNotFound         = errors.New("save slot not found")
)

// Save is one cloud save payload.
type Save struct {
	Version   int64
	UpdatedAt int64
	DeviceID  string
	Items     map[string]int
	Quests    map[string]int
	Checksum  string
}

// MergeInput is one incoming save in a merge.
type MergeInput struct {
	Save   Save
	Origin string
}

// MergeResult is the outcome of a save merge.
type MergeResult struct {
	Version    int64
	Winner     string
	Conflict   bool
	Merged     bool
	ItemsTotal int
}

// SaveCloudMerger merges cloud saves of one account.
type SaveCloudMerger struct{}

// NewSaveCloudMerger returns a merger bound to a key id.
func NewSaveCloudMerger(keyID string) *SaveCloudMerger { return &SaveCloudMerger{} }

// Upload stores a save after validating its version.
func (m *SaveCloudMerger) Upload(slot string, s Save) (Save, error) { return Save{}, ErrNotImplemented }

// Download returns a save after verifying its checksum.
func (m *SaveCloudMerger) Download(slot string) (Save, error) { return Save{}, ErrNotImplemented }

// Merge merges an incoming save with the stored one.
func (m *SaveCloudMerger) Merge(slot string, in MergeInput) (MergeResult, error) {
	return MergeResult{}, ErrNotImplemented
}

// Stored returns the stored save of a slot.
func (m *SaveCloudMerger) Stored(slot string) (Save, bool) { return Save{}, false }

// Exists reports whether a slot exists in the cloud.
func (m *SaveCloudMerger) Exists(slot string) bool { return false }

// Delete removes the save from the slot and from the cloud.
func (m *SaveCloudMerger) Delete(slot string) error { return ErrNotImplemented }

// StoreRaw stores a save verbatim, bypassing validation (test/injection hook).
func (m *SaveCloudMerger) StoreRaw(slot string, s Save) error { return ErrNotImplemented }

// RotateKey rotates the encryption key while keeping old saves readable.
func (m *SaveCloudMerger) RotateKey(keyID string) error { return ErrNotImplemented }

// Checksum computes the checksum of a save payload.
func (m *SaveCloudMerger) Checksum(s Save) string { return "" }

// History reports how many versions of the slot were stored.
func (m *SaveCloudMerger) History(slot string) int { return 0 }

// Audit returns the merge audit log.
func (m *SaveCloudMerger) Audit() []string { return nil }
