package task

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
	"time"
)

var (
	errEmptySeason = errors.New("season id must not be empty")
	errEmptyPlayer = errors.New("player id must not be empty")
	errSameSeason  = errors.New("old and new season must differ")
)

// Entry is one persisted leaderboard record.
type Entry struct {
	SeasonID  string
	PlayerID  string
	Score     int64
	UpdatedAt time.Time
	ShardID   string
}

// RankRow is one row of a rendered leaderboard.
type RankRow struct {
	PlayerID string
	Score    int64
	Rank     int
}

// entryKey scopes a player id with its season so the same player can hold
// one record per season without cross-season collisions.
func entryKey(seasonID, playerID string) string {
	return seasonID + "\x1f" + playerID
}

// ranksBefore reports whether a ranks strictly ahead of b: higher score
// first, then earlier update, then lexicographically smaller player id so
// the order is total and stable across servers.
func ranksBefore(a, b Entry) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.Before(b.UpdatedAt)
	}
	return a.PlayerID < b.PlayerID
}

type shard struct {
	mu   sync.RWMutex
	data map[string]Entry
}

// seasonView caches a sorted snapshot of one season. Writes only flip the
// dirty flag; the snapshot is rebuilt lazily on the first read after a
// write, so rank queries stay O(log n) without sorting on every call.
type seasonView struct {
	mu     sync.RWMutex
	sorted []Entry
	dirty  bool
}

// LeaderboardStore is the persistent storage shared by every shard.
type LeaderboardStore struct {
	shards []*shard

	viewsMu sync.Mutex
	views   map[string]*seasonView
}

// NewLeaderboardStore builds a store split over the given number of shards.
func NewLeaderboardStore(shards int) *LeaderboardStore {
	if shards <= 0 {
		shards = 1
	}
	s := &LeaderboardStore{shards: make([]*shard, shards), views: make(map[string]*seasonView)}
	for i := range s.shards {
		s.shards[i] = &shard{data: make(map[string]Entry)}
	}
	return s
}

// shardOf routes a player to exactly one shard so a player can never hold
// two records for the same season on different shards.
func (s *LeaderboardStore) shardOf(playerID string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(playerID))
	return s.shards[int(h.Sum32())%len(s.shards)]
}

func (s *LeaderboardStore) shardIndex(playerID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(playerID))
	return int(h.Sum32()) % len(s.shards)
}

// Put writes or replaces the record for entry.SeasonID and entry.PlayerID.
func (s *LeaderboardStore) Put(e Entry) error {
	if e.SeasonID == "" {
		return errEmptySeason
	}
	if e.PlayerID == "" {
		return errEmptyPlayer
	}
	sh := s.shardOf(e.PlayerID)
	sh.mu.Lock()
	sh.data[entryKey(e.SeasonID, e.PlayerID)] = e
	sh.mu.Unlock()
	s.markDirty(e.SeasonID)
	return nil
}

func (s *LeaderboardStore) viewOf(seasonID string) *seasonView {
	s.viewsMu.Lock()
	v, ok := s.views[seasonID]
	if !ok {
		v = &seasonView{dirty: true}
		s.views[seasonID] = v
	}
	s.viewsMu.Unlock()
	return v
}

func (s *LeaderboardStore) markDirty(seasonID string) {
	v := s.viewOf(seasonID)
	v.mu.Lock()
	v.dirty = true
	v.mu.Unlock()
}

// SortedEntries returns a stable, fully ranked snapshot of a season. The
// returned slice is never mutated in place, so concurrent readers keep a
// consistent view even while writes continue.
func (s *LeaderboardStore) SortedEntries(seasonID string) []Entry {
	if seasonID == "" {
		return nil
	}
	v := s.viewOf(seasonID)
	v.mu.RLock()
	if !v.dirty {
		snapshot := v.sorted
		v.mu.RUnlock()
		return snapshot
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dirty {
		entries := s.Entries(seasonID)
		sort.Slice(entries, func(i, j int) bool {
			return ranksBefore(entries[i], entries[j])
		})
		v.sorted = entries
		v.dirty = false
	}
	return v.sorted
}

// Get reads the record for one player in one season.
func (s *LeaderboardStore) Get(seasonID, playerID string) (Entry, bool) {
	if seasonID == "" || playerID == "" {
		return Entry{}, false
	}
	sh := s.shardOf(playerID)
	sh.mu.RLock()
	e, ok := sh.data[entryKey(seasonID, playerID)]
	sh.mu.RUnlock()
	return e, ok
}

// Entries lists every record of a season.
func (s *LeaderboardStore) Entries(seasonID string) []Entry {
	if seasonID == "" {
		return nil
	}
	var out []Entry
	for _, sh := range s.shards {
		sh.mu.RLock()
		for _, e := range sh.data {
			if e.SeasonID == seasonID {
				out = append(out, e)
			}
		}
		sh.mu.RUnlock()
	}
	return out
}

// Count reports how many records a season holds.
func (s *LeaderboardStore) Count(seasonID string) int {
	if seasonID == "" {
		return 0
	}
	n := 0
	for _, sh := range s.shards {
		sh.mu.RLock()
		for _, e := range sh.data {
			if e.SeasonID == seasonID {
				n++
			}
		}
		sh.mu.RUnlock()
	}
	return n
}

// Clear removes every record of a season.
func (s *LeaderboardStore) Clear(seasonID string) error {
	if seasonID == "" {
		return errEmptySeason
	}
	for _, sh := range s.shards {
		sh.mu.Lock()
		for k, e := range sh.data {
			if e.SeasonID == seasonID {
				delete(sh.data, k)
			}
		}
		sh.mu.Unlock()
	}
	s.viewsMu.Lock()
	delete(s.views, seasonID)
	s.viewsMu.Unlock()
	return nil
}

// LeaderboardGuard keeps season resets, shards and rankings consistent.
type LeaderboardGuard struct {
	store *LeaderboardStore

	mu          sync.Mutex
	current     string
	resetAt     map[string]time.Time   // retired season -> instant it was retired
	windowStart map[string]time.Time   // season -> instant its window opened
	shardStamps map[string][]time.Time // season -> per-shard reset stamps
	archive     map[string][]Entry     // season -> frozen historical rows
	resetHook   func() error
}

// NewLeaderboardGuard builds a guard over the given store.
func NewLeaderboardGuard(store *LeaderboardStore) *LeaderboardGuard {
	if store == nil {
		store = NewLeaderboardStore(1)
	}
	return &LeaderboardGuard{
		store:       store,
		resetAt:     make(map[string]time.Time),
		windowStart: make(map[string]time.Time),
		shardStamps: make(map[string][]time.Time),
		archive:     make(map[string][]Entry),
	}
}

// Store returns the store the guard writes through.
func (g *LeaderboardGuard) Store() *LeaderboardStore {
	return g.store
}

// SubmitAt records a score in the season that owns the instant at.
func (g *LeaderboardGuard) SubmitAt(requestedSeason, playerID string, score int64, at time.Time) error {
	if requestedSeason == "" {
		return errEmptySeason
	}
	if playerID == "" {
		return errEmptyPlayer
	}
	g.mu.Lock()
	target := requestedSeason
	switch {
	case g.current == "":
		// First write ever opens the window of the requested season.
		g.current = requestedSeason
		g.windowStart[requestedSeason] = at
	case requestedSeason == g.current:
		// In-window write.
	default:
		// A write addressed at a retired season belongs to the current
		// season once its timestamp falls inside the current window;
		// earlier timestamps are late arrivals for the old season.
		if retiredAt, retired := g.resetAt[requestedSeason]; retired && !at.Before(retiredAt) {
			target = g.current
		}
	}
	g.mu.Unlock()
	return g.store.Put(Entry{
		SeasonID:  target,
		PlayerID:  playerID,
		Score:     score,
		UpdatedAt: at,
		ShardID:   strconv.Itoa(g.store.shardIndex(playerID)),
	})
}

// Score reads one player score in one season.
func (g *LeaderboardGuard) Score(seasonID, playerID string) (int64, bool) {
	e, ok := g.store.Get(seasonID, playerID)
	if !ok {
		return 0, false
	}
	return e.Score, true
}

// TopN returns the first n ranked rows of a season.
func (g *LeaderboardGuard) TopN(seasonID string, n int) []RankRow {
	if seasonID == "" || n <= 0 {
		return nil
	}
	entries := g.store.SortedEntries(seasonID)
	if len(entries) > n {
		entries = entries[:n]
	}
	rows := make([]RankRow, len(entries))
	for i, e := range entries {
		rows[i] = RankRow{PlayerID: e.PlayerID, Score: e.Score, Rank: i + 1}
	}
	return rows
}

// RankOf returns the rank of a player, including players outside the top 100.
func (g *LeaderboardGuard) RankOf(seasonID, playerID string) (int, bool) {
	me, ok := g.store.Get(seasonID, playerID)
	if !ok {
		return 0, false
	}
	// Binary search over the cached ranked snapshot: O(log n) per call,
	// no full-table sort on the query path.
	sorted := g.store.SortedEntries(seasonID)
	idx := sort.Search(len(sorted), func(i int) bool {
		return !ranksBefore(sorted[i], me)
	})
	if idx < len(sorted) && sorted[idx].PlayerID == playerID {
		return idx + 1, true
	}
	return 0, false
}

// ResetSeasonAt moves the board from oldSeason to newSeason at the given instant.
func (g *LeaderboardGuard) ResetSeasonAt(oldSeason, newSeason string, at time.Time) error {
	if oldSeason == "" || newSeason == "" {
		return errEmptySeason
	}
	if oldSeason == newSeason {
		return errSameSeason
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Snapshot everything the transaction touches so a hook failure can
	// roll the store and the guard state back as a whole.
	oldEntries := g.store.Entries(oldSeason)
	prevCurrent := g.current
	prevOldRetired, hadOldRetired := g.resetAt[oldSeason]
	prevNewStart, hadNewStart := g.windowStart[newSeason]
	prevNewStamps, hadNewStamps := g.shardStamps[newSeason]
	prevArchive, hadArchive := g.archive[oldSeason]

	// Freeze the historical board, then clear the persisted old-season
	// rows on every shard at the same logical instant.
	frozen := make([]Entry, len(oldEntries))
	copy(frozen, oldEntries)
	sort.Slice(frozen, func(i, j int) bool {
		return ranksBefore(frozen[i], frozen[j])
	})
	g.archive[oldSeason] = frozen
	if err := g.store.Clear(oldSeason); err != nil {
		return fmt.Errorf("reset %s -> %s: %w", oldSeason, newSeason, err)
	}

	g.current = newSeason
	g.resetAt[oldSeason] = at
	g.windowStart[newSeason] = at
	stamps := make([]time.Time, len(g.store.shards))
	for i := range stamps {
		stamps[i] = at
	}
	g.shardStamps[newSeason] = stamps

	if g.resetHook != nil {
		if err := g.resetHook(); err != nil {
			// Roll the whole transaction back.
			_ = g.store.Clear(oldSeason)
			for _, e := range oldEntries {
				_ = g.store.Put(e)
			}
			g.current = prevCurrent
			if hadOldRetired {
				g.resetAt[oldSeason] = prevOldRetired
			} else {
				delete(g.resetAt, oldSeason)
			}
			if hadNewStart {
				g.windowStart[newSeason] = prevNewStart
			} else {
				delete(g.windowStart, newSeason)
			}
			if hadNewStamps {
				g.shardStamps[newSeason] = prevNewStamps
			} else {
				delete(g.shardStamps, newSeason)
			}
			if hadArchive {
				g.archive[oldSeason] = prevArchive
			} else {
				delete(g.archive, oldSeason)
			}
			return fmt.Errorf("reset %s -> %s rolled back: %w", oldSeason, newSeason, err)
		}
	}
	return nil
}

// ShardResetStamps reports the reset stamp each shard used for a season.
func (g *LeaderboardGuard) ShardResetStamps(seasonID string) []time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	stamps, ok := g.shardStamps[seasonID]
	if !ok {
		return nil
	}
	out := make([]time.Time, len(stamps))
	copy(out, stamps)
	return out
}

// Archive returns the frozen historical rows of a season.
func (g *LeaderboardGuard) Archive(seasonID string) []Entry {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, ok := g.archive[seasonID]
	if !ok {
		return nil
	}
	out := make([]Entry, len(rows))
	copy(out, rows)
	return out
}

// MergeRemote merges rows replicated from another server without double counting.
func (g *LeaderboardGuard) MergeRemote(seasonID string, entries []Entry) error {
	if seasonID == "" {
		return errEmptySeason
	}
	for _, e := range entries {
		if e.PlayerID == "" {
			return errEmptyPlayer
		}
		existing, ok := g.store.Get(seasonID, e.PlayerID)
		if ok {
			// Same primary key: keep the higher score and the original
			// UpdatedAt instead of counting the row twice.
			if e.Score > existing.Score {
				existing.Score = e.Score
				if err := g.store.Put(existing); err != nil {
					return err
				}
			}
			continue
		}
		e.SeasonID = seasonID
		if e.ShardID == "" {
			e.ShardID = strconv.Itoa(g.store.shardIndex(e.PlayerID))
		}
		if err := g.store.Put(e); err != nil {
			return err
		}
	}
	return nil
}

// Records reports how many persisted rows exist for one player in one season.
func (g *LeaderboardGuard) Records(seasonID, playerID string) int {
	if _, ok := g.store.Get(seasonID, playerID); ok {
		return 1
	}
	return 0
}

// SetResetHook installs a hook that runs inside the reset transaction.
func (g *LeaderboardGuard) SetResetHook(h func() error) error {
	g.mu.Lock()
	g.resetHook = h
	g.mu.Unlock()
	return nil
}

// CurrentSeason returns the season that receives new writes.
func (g *LeaderboardGuard) CurrentSeason() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current
}
