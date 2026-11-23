package task

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
)

const (
	// JournalPerAccountLimit bounds the audit trail kept for one account.
	JournalPerAccountLimit = 256
	// JournalTotalLimit bounds the audit trail kept by one ledger.
	JournalTotalLimit = 4096
)

// JournalEntry is one committed pity-ledger mutation. Before and After make
// the entry self-contained so an operator can reconcile a single account
// without replaying the whole history.
type JournalEntry struct {
	AccountID string `json:"account_id"`
	PoolID    string `json:"pool_id"`
	PoolKey   string `json:"pool_key"`
	Kind      string `json:"kind"`
	Sequence  uint64 `json:"sequence"`
	Order     uint64 `json:"order"`
	Draws     int    `json:"draws"`
	Before    int    `json:"before"`
	After     int    `json:"after"`
}

// PityLedger tracks gacha pity counters per account and pool.
//
// All reads and writes of counters, journals, the threshold and the device
// guard share one mutex. That is intentional: a draw, its journal entry and
// the counter update form one in-memory transaction, and two devices cannot
// interleave on the same account/pool key.
type PityLedger struct {
	mu sync.Mutex

	threshold int
	counters  map[string]int

	journals     map[string][]JournalEntry
	journalSeq   map[string]uint64
	journalOrder uint64
	journalTotal int

	journalPerAccountLimit int
	journalTotalLimit      int

	firstDevice string
	hasDevice   bool
}

// NewPityLedger builds a ledger with the pity threshold of the current version.
func NewPityLedger(threshold int) *PityLedger {
	return &PityLedger{
		threshold:              threshold,
		counters:               make(map[string]int),
		journals:               make(map[string][]JournalEntry),
		journalSeq:             make(map[string]uint64),
		journalPerAccountLimit: JournalPerAccountLimit,
		journalTotalLimit:      JournalTotalLimit,
	}
}

// PoolKey is the ledger key of one account in one card pool.
func (p *PityLedger) PoolKey(accountID, poolID string) string {
	return accountID + ":" + poolID
}

// OnDraw counts draws and returns the new pity counter.
//
// The counter update and its journal entry are committed under the same lock.
// If the entry cannot be committed, the draw is not counted and the previous
// pity value is returned.
func (p *PityLedger) OnDraw(accountID, poolID string, count int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureLocked()

	key := p.PoolKey(accountID, poolID)
	before := p.counters[key]
	if count <= 0 || before > maxInt()-count {
		return before
	}

	after := before + count
	entry := JournalEntry{
		AccountID: accountID,
		PoolID:    poolID,
		PoolKey:   key,
		Kind:      "draw",
		Draws:     count,
		Before:    before,
		After:     after,
	}
	if !p.commitLocked(key, entry) {
		return before
	}
	return after
}

// Pity is the current pity counter of an account in a pool.
func (p *PityLedger) Pity(accountID, poolID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counters[p.PoolKey(accountID, poolID)]
}

// Threshold is the configured pity threshold.
func (p *PityLedger) Threshold() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.threshold
}

// Triggered reports whether the pity counter reached the threshold.
func (p *PityLedger) Triggered(accountID, poolID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counters[p.PoolKey(accountID, poolID)] >= p.threshold
}

// OnHit consumes a pity hit and returns the counter that was reset.
func (p *PityLedger) OnHit(accountID, poolID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureLocked()

	key := p.PoolKey(accountID, poolID)
	before := p.counters[key]
	if before <= 0 {
		p.counters[key] = 0
		return 0
	}

	entry := JournalEntry{
		AccountID: accountID,
		PoolID:    poolID,
		PoolKey:   key,
		Kind:      "hit",
		Draws:     0,
		Before:    before,
		After:     0,
	}
	if !p.commitLocked(key, entry) {
		return 0
	}
	return before
}

// CarryOver returns the pity counter inherited by a new pool version.
//
// Version inheritance must never move the pity baseline back to zero. The
// counter is deliberately kept under the same account/pool key; the version
// is an audit label rather than a new independent pity bucket.
func (p *PityLedger) CarryOver(accountID, poolID, version string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counters[p.PoolKey(accountID, poolID)]
}

// RateUpEnd returns the banked counter after a rate up period ends.
//
// Ending rate up changes probabilities, not the pity baseline. The banked
// counter is returned unchanged.
func (p *PityLedger) RateUpEnd(accountID, poolID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counters[p.PoolKey(accountID, poolID)]
}

// ConcurrentDraw serialises several devices drawing on the same pool.
func (p *PityLedger) ConcurrentDraw(accountID, poolID string, workers int) int {
	if workers <= 0 {
		return p.Pity(accountID, poolID)
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			p.OnDraw(accountID, poolID, 1)
		}()
	}
	wg.Wait()
	return p.Pity(accountID, poolID)
}

// Seed derives a reproducible draw seed from a session id.
func (p *PityLedger) Seed(sessionID string) uint64 {
	sum := sha256.Sum256([]byte(sessionID))
	seed := binary.BigEndian.Uint64(sum[:8])
	if seed == 0 {
		// The all-zero state is reserved for callers that forgot to seed.
		seed = 0x9e3779b97f4a7c15
	}
	return seed
}

// Snapshot exports the pity counters for persistence.
func (p *PityLedger) Snapshot() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := make(map[string]int, len(p.counters))
	for key, value := range p.counters {
		state[key] = value
	}
	return state
}

// Restore reloads pity counters from storage.
//
// Restoring is a replacement, not a merge: the snapshot is the authoritative
// state after a process restart. Journals are cleared with the old in-memory
// state so a later entry cannot point at a stale pre-restart counter; the
// first new entry starts from the restored Before value, keeping the audit
// trail continuous.
func (p *PityLedger) Restore(state map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.counters = make(map[string]int, len(state))
	for key, value := range state {
		p.counters[key] = value
	}
	p.journals = make(map[string][]JournalEntry)
	p.journalSeq = make(map[string]uint64)
	p.journalOrder = 0
	p.journalTotal = 0
}

// DeviceGuard accepts only the first draw device of an account.
func (p *PityLedger) DeviceGuard(deviceID string) bool {
	if deviceID == "" {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.hasDevice {
		p.firstDevice = deviceID
		p.hasDevice = true
		return true
	}
	return deviceID == p.firstDevice
}

// Reload applies a hot updated pity threshold and returns the kept counter.
func (p *PityLedger) Reload(accountID, poolID string, newThreshold int) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := p.PoolKey(accountID, poolID)
	p.threshold = newThreshold
	return p.counters[key]
}

// ResolveReward returns the pity reward when pity triggered, never a normal drop.
func (p *PityLedger) ResolveReward(pityTriggered bool, normalDrop string) string {
	if pityTriggered {
		return "pity_reward"
	}
	return normalDrop
}

// Journal returns a copy of the retained audit trail for one account.
func (p *PityLedger) Journal(accountID string) []JournalEntry {
	p.mu.Lock()
	defer p.mu.Unlock()

	entries := p.journals[accountID]
	out := make([]JournalEntry, len(entries))
	copy(out, entries)
	return out
}

// JournalSize returns the number of retained journal entries in the ledger.
func (p *PityLedger) JournalSize() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.journalTotal
}

// commitLocked writes the journal entry first and only then publishes the new
// counter value. Callers must hold p.mu. The entry's Before must match the
// current value, which makes stale or duplicate settlements fail instead of
// overwriting a newer counter.
func (p *PityLedger) commitLocked(key string, entry JournalEntry) bool {
	if p.counters[key] != entry.Before {
		return false
	}
	if !p.appendJournalLocked(entry) {
		return false
	}
	p.counters[key] = entry.After
	return true
}

// ensureLocked makes a zero-value ledger usable as well as one built by
// NewPityLedger.
func (p *PityLedger) ensureLocked() {
	if p.counters == nil {
		p.counters = make(map[string]int)
	}
	if p.journals == nil {
		p.journals = make(map[string][]JournalEntry)
	}
	if p.journalSeq == nil {
		p.journalSeq = make(map[string]uint64)
	}
	if p.journalPerAccountLimit <= 0 {
		p.journalPerAccountLimit = JournalPerAccountLimit
	}
	if p.journalTotalLimit <= 0 {
		p.journalTotalLimit = JournalTotalLimit
	}
}

// appendJournalLocked appends an entry and applies the per-account and global
// retention limits by overwriting the oldest entries first.
func (p *PityLedger) appendJournalLocked(entry JournalEntry) bool {
	if p.journals == nil {
		p.journals = make(map[string][]JournalEntry)
	}
	if p.journalSeq == nil {
		p.journalSeq = make(map[string]uint64)
	}

	p.journalOrder++
	p.journalSeq[entry.AccountID]++
	entry.Order = p.journalOrder
	entry.Sequence = p.journalSeq[entry.AccountID]

	p.journals[entry.AccountID] = append(p.journals[entry.AccountID], entry)
	p.journalTotal++

	for p.journalPerAccountLimit > 0 &&
		len(p.journals[entry.AccountID]) > p.journalPerAccountLimit {
		p.dropOldestForAccountLocked(entry.AccountID)
	}
	for p.journalTotalLimit > 0 && p.journalTotal > p.journalTotalLimit {
		if !p.dropOldestGlobalLocked() {
			break
		}
	}
	return true
}

func (p *PityLedger) dropOldestForAccountLocked(accountID string) bool {
	entries := p.journals[accountID]
	if len(entries) == 0 {
		delete(p.journals, accountID)
		return false
	}
	entries = entries[1:]
	p.journalTotal--
	if len(entries) == 0 {
		delete(p.journals, accountID)
	} else {
		p.journals[accountID] = entries
	}
	return true
}

func (p *PityLedger) dropOldestGlobalLocked() bool {
	oldestAccount := ""
	var oldestOrder uint64
	found := false

	for accountID, entries := range p.journals {
		if len(entries) == 0 {
			delete(p.journals, accountID)
			continue
		}
		if !found || entries[0].Order < oldestOrder {
			oldestAccount = accountID
			oldestOrder = entries[0].Order
			found = true
		}
	}
	if !found {
		return false
	}
	return p.dropOldestForAccountLocked(oldestAccount)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
