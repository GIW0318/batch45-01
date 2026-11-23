package task

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

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

const (
	historyLimit = 100
	auditLimit   = 100
)

type historyEntry struct {
	Version int64
}

type storedBlob struct {
	keyID      string
	nonce      []byte
	ciphertext []byte
}

type slotState struct {
	current    Save
	hasCurrent bool
	cloud      *storedBlob
	history    []historyEntry
}

// SaveCloudMerger merges cloud saves of one account.
type SaveCloudMerger struct {
	mu       sync.Mutex
	keyID    string
	keys     map[string][]byte
	slots    map[string]*slotState
	auditLog []string
}

// NewSaveCloudMerger returns a merger bound to a key id.
func NewSaveCloudMerger(keyID string) *SaveCloudMerger {
	m := &SaveCloudMerger{
		keyID: keyID,
		keys:  make(map[string][]byte),
		slots: make(map[string]*slotState),
	}
	m.keys[keyID] = newKey(keyID)
	return m
}

// Upload stores a save after validating its version.
func (m *SaveCloudMerger) Upload(slot string, s Save) (Save, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st := m.slots[slot]
	if st != nil && st.hasCurrent {
		switch {
		case s.Version < st.current.Version:
			return Save{}, ErrStaleVersion
		case s.Version == st.current.Version && s.DeviceID != st.current.DeviceID:
			return Save{}, ErrDeviceConflict
		case s.Version == st.current.Version:
			return Save{}, ErrStaleVersion
		}
	} else if st != nil && st.cloud != nil {
		// A raw blob may exist without a committed local copy. Compare against
		// its version when it can be read so a corrupt injection cannot make an
		// old upload look new.
		if prev, err := m.loadBlobLocked(st.cloud); err == nil {
			switch {
			case s.Version < prev.Version:
				return Save{}, ErrStaleVersion
			case s.Version == prev.Version && s.DeviceID != prev.DeviceID:
				return Save{}, ErrDeviceConflict
			case s.Version == prev.Version:
				return Save{}, ErrStaleVersion
			}
		}
	}

	stored := cloneSave(s)
	stored.Checksum = m.Checksum(stored)
	blob, err := m.sealSaveLocked(stored)
	if err != nil {
		return Save{}, err
	}
	if st == nil {
		st = &slotState{}
		m.slots[slot] = st
	}
	st.current = cloneSave(stored)
	st.hasCurrent = true
	st.cloud = blob
	st.history = appendBoundedHistory(st.history, historyEntry{Version: stored.Version})
	return cloneSave(stored), nil
}

// Download returns a save after verifying its checksum.
func (m *SaveCloudMerger) Download(slot string) (Save, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.slots[slot]
	if !ok {
		return Save{}, ErrNotFound
	}
	if st.cloud == nil {
		if !st.hasCurrent {
			return Save{}, ErrNotFound
		}
		if err := validateSave(st.current, m.Checksum(st.current)); err != nil {
			return Save{}, err
		}
		return cloneSave(st.current), nil
	}
	s, err := m.loadBlobLocked(st.cloud)
	if err != nil {
		return Save{}, err
	}
	return s, nil
}

// Merge merges an incoming save with the stored one.
func (m *SaveCloudMerger) Merge(slot string, in MergeInput) (MergeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	incoming := cloneSave(in.Save)
	incoming.Checksum = ""

	st := m.slots[slot]
	if st == nil {
		st = &slotState{}
		m.slots[slot] = st
	}

	var stored Save
	hasStored := false
	if st.cloud != nil {
		prev, err := m.loadBlobLocked(st.cloud)
		if err != nil {
			m.addAuditLocked("merge", slot, "error", err, MergeResult{})
			return MergeResult{}, err
		}
		stored = prev
		hasStored = true
	}
	if st.hasCurrent && (!hasStored || st.current.Version > stored.Version) {
		stored = cloneSave(st.current)
		hasStored = true
	}

	if !hasStored {
		merged := cloneSave(incoming)
		merged.Checksum = m.Checksum(merged)
		blob, err := m.sealSaveLocked(merged)
		if err != nil {
			m.addAuditLocked("merge", slot, "error", err, MergeResult{})
			return MergeResult{}, err
		}
		st.current = cloneSave(merged)
		st.hasCurrent = true
		st.cloud = blob
		st.history = appendBoundedHistory(st.history, historyEntry{Version: merged.Version})
		res := MergeResult{
			Version:    merged.Version,
			Winner:     "incoming",
			Merged:     true,
			ItemsTotal: itemsTotal(merged.Items),
		}
		m.addAuditLocked("merge", slot, "created", nil, res, "origin", in.Origin)
		return res, nil
	}

	switch {
	case incoming.Version < stored.Version:
		res := MergeResult{
			Version:    stored.Version,
			Winner:     "cloud",
			Merged:     false,
			ItemsTotal: itemsTotal(stored.Items),
		}
		m.addAuditLocked("merge", slot, "cloud-wins", nil, res, "origin", in.Origin)
		return res, nil

	case incoming.Version > stored.Version:
		merged := cloneSave(incoming)
		merged.Checksum = m.Checksum(merged)
		blob, err := m.sealSaveLocked(merged)
		if err != nil {
			m.addAuditLocked("merge", slot, "error", err, MergeResult{})
			return MergeResult{}, err
		}
		st.current = cloneSave(merged)
		st.hasCurrent = true
		st.cloud = blob
		st.history = appendBoundedHistory(st.history, historyEntry{Version: merged.Version})
		res := MergeResult{
			Version:    merged.Version,
			Winner:     "incoming",
			Merged:     true,
			ItemsTotal: itemsTotal(merged.Items),
		}
		m.addAuditLocked("merge", slot, "incoming-wins", nil, res, "origin", in.Origin)
		return res, nil
	}

	// Equal versions: replaying the exact same save is a no-op. This makes
	// retries idempotent instead of summing the same item quantities again.
	if sameSavePayload(stored, incoming) {
		res := MergeResult{
			Version:    stored.Version,
			Winner:     "cloud",
			Merged:     false,
			ItemsTotal: itemsTotal(stored.Items),
		}
		m.addAuditLocked("merge", slot, "duplicate", nil, res, "origin", in.Origin)
		return res, nil
	}

	items := cloneIntMap(stored.Items)
	if items == nil && len(incoming.Items) > 0 {
		items = make(map[string]int, len(incoming.Items))
	}
	for k, v := range incoming.Items {
		items[k] += v
	}
	quests := cloneIntMap(stored.Quests)
	if quests == nil && len(incoming.Quests) > 0 {
		quests = make(map[string]int, len(incoming.Quests))
	}
	for k, v := range incoming.Quests {
		if v > quests[k] {
			quests[k] = v
		}
	}

	deviceID := incoming.DeviceID
	if deviceID == "" {
		deviceID = stored.DeviceID
	}
	updatedAt := stored.UpdatedAt
	if incoming.UpdatedAt > updatedAt {
		updatedAt = incoming.UpdatedAt
	}
	merged := Save{
		Version:   stored.Version + 1,
		UpdatedAt: updatedAt,
		DeviceID:  deviceID,
		Items:     items,
		Quests:    quests,
	}
	merged.Checksum = m.Checksum(merged)
	blob, err := m.sealSaveLocked(merged)
	if err != nil {
		m.addAuditLocked("merge", slot, "error", err, MergeResult{})
		return MergeResult{}, err
	}

	st.current = cloneSave(merged)
	st.hasCurrent = true
	st.cloud = blob
	st.history = appendBoundedHistory(st.history, historyEntry{Version: merged.Version})
	res := MergeResult{
		Version:    merged.Version,
		Winner:     "merged",
		Conflict:   stored.DeviceID != incoming.DeviceID,
		Merged:     true,
		ItemsTotal: itemsTotal(merged.Items),
	}
	m.addAuditLocked("merge", slot, "merged", nil, res, "origin", in.Origin)
	return res, nil
}

// Stored returns the stored save of a slot.
func (m *SaveCloudMerger) Stored(slot string) (Save, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.slots[slot]
	if !ok {
		return Save{}, false
	}
	if st.hasCurrent {
		return cloneSave(st.current), true
	}
	if st.cloud == nil {
		return Save{}, false
	}
	s, err := m.loadBlobLocked(st.cloud)
	if err != nil {
		return Save{}, false
	}
	return s, true
}

// Exists reports whether a slot exists in the cloud.
func (m *SaveCloudMerger) Exists(slot string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.slots[slot]
	return ok && (st.hasCurrent || st.cloud != nil)
}

// Delete removes the save from the slot and from the cloud.
func (m *SaveCloudMerger) Delete(slot string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.slots[slot]; !ok {
		return ErrNotFound
	}
	delete(m.slots, slot)
	return nil
}

// StoreRaw stores a save verbatim, bypassing validation (test/injection hook).
func (m *SaveCloudMerger) StoreRaw(slot string, s Save) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	blob, err := m.sealSaveLocked(cloneSave(s))
	if err != nil {
		return err
	}
	st := m.slots[slot]
	if st == nil {
		st = &slotState{}
		m.slots[slot] = st
	}
	st.cloud = blob
	return nil
}

// RotateKey rotates the encryption key while keeping old saves readable.
func (m *SaveCloudMerger) RotateKey(keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if keyID == "" {
		return ErrCorrupt
	}
	if _, ok := m.keys[keyID]; !ok {
		m.keys[keyID] = newKey(keyID)
	}
	m.keyID = keyID
	return nil
}

// Checksum computes the checksum of a save payload.
func (m *SaveCloudMerger) Checksum(s Save) string {
	type payload struct {
		Version   int64
		UpdatedAt int64
		DeviceID  string
		Items     map[string]int
		Quests    map[string]int
	}
	raw, err := json.Marshal(payload{
		Version:   s.Version,
		UpdatedAt: s.UpdatedAt,
		DeviceID:  s.DeviceID,
		Items:     s.Items,
		Quests:    s.Quests,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// History reports how many versions of the slot were stored.
func (m *SaveCloudMerger) History(slot string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.slots[slot]
	if !ok {
		return 0
	}
	return len(st.history)
}

// Audit returns the merge audit log.
func (m *SaveCloudMerger) Audit() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, len(m.auditLog))
	copy(out, m.auditLog)
	return out
}

func newKey(keyID string) []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err == nil {
		return key
	}
	sum := sha256.Sum256([]byte(keyID))
	return sum[:]
}

func (m *SaveCloudMerger) sealSaveLocked(s Save) (*storedBlob, error) {
	key := m.keys[m.keyID]
	if len(key) == 0 {
		key = newKey(m.keyID)
		m.keys[m.keyID] = key
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, ErrCorrupt
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrCorrupt
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrCorrupt
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrCorrupt
	}
	return &storedBlob{
		keyID:      m.keyID,
		nonce:      nonce,
		ciphertext: gcm.Seal(nil, nonce, raw, nil),
	}, nil
}

func (m *SaveCloudMerger) loadBlobLocked(blob *storedBlob) (Save, error) {
	if blob == nil || len(blob.nonce) == 0 || len(blob.ciphertext) == 0 {
		return Save{}, ErrCorrupt
	}
	key := m.keys[blob.keyID]
	if len(key) == 0 {
		return Save{}, ErrCorrupt
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Save{}, ErrCorrupt
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Save{}, ErrCorrupt
	}
	raw, err := gcm.Open(nil, blob.nonce, blob.ciphertext, nil)
	if err != nil {
		return Save{}, ErrCorrupt
	}
	var s Save
	if err := json.Unmarshal(raw, &s); err != nil {
		return Save{}, ErrCorrupt
	}
	if err := validateSave(s, m.Checksum(s)); err != nil {
		return Save{}, err
	}
	return cloneSave(s), nil
}

func validateSave(s Save, want string) error {
	if s.Checksum == "" {
		return ErrCorrupt
	}
	if s.Checksum != want {
		return ErrChecksumMismatch
	}
	return nil
}

func cloneSave(s Save) Save {
	return Save{
		Version:   s.Version,
		UpdatedAt: s.UpdatedAt,
		DeviceID:  s.DeviceID,
		Items:     cloneIntMap(s.Items),
		Quests:    cloneIntMap(s.Quests),
		Checksum:  s.Checksum,
	}
}

func cloneIntMap(in map[string]int) map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sameSavePayload(a, b Save) bool {
	return a.Version == b.Version &&
		a.DeviceID == b.DeviceID &&
		sameIntMap(a.Items, b.Items) &&
		sameIntMap(a.Quests, b.Quests)
}

func sameIntMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || av != bv {
			return false
		}
	}
	return true
}

func itemsTotal(items map[string]int) int {
	total := 0
	for _, v := range items {
		total += v
	}
	return total
}

func appendBoundedHistory(history []historyEntry, entry historyEntry) []historyEntry {
	history = append(history, entry)
	if len(history) > historyLimit {
		copy(history, history[len(history)-historyLimit:])
		history = history[:historyLimit]
	}
	return history
}

func (m *SaveCloudMerger) addAuditLocked(op, slot, status string, err error, res MergeResult, extras ...interface{}) {
	entry := fmt.Sprintf(
		"%s slot=%s status=%s version=%d winner=%s conflict=%t merged=%t items=%d",
		op, slot, status, res.Version, res.Winner, res.Conflict, res.Merged, res.ItemsTotal,
	)
	if err != nil {
		entry += fmt.Sprintf(" error=%q", err.Error())
	}
	for i := 0; i+1 < len(extras); i += 2 {
		entry += fmt.Sprintf(" %v=%v", extras[i], extras[i+1])
	}
	m.auditLog = append(m.auditLog, entry)
	if len(m.auditLog) > auditLimit {
		copy(m.auditLog, m.auditLog[len(m.auditLog)-auditLimit:])
		m.auditLog = m.auditLog[:auditLimit]
	}
}
