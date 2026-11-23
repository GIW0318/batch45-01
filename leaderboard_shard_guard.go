package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

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

// LeaderboardStore is the persistent storage shared by every shard.
type LeaderboardStore struct {
}

// NewLeaderboardStore builds a store split over the given number of shards.
func NewLeaderboardStore(shards int) *LeaderboardStore {
	return &LeaderboardStore{}
}

// Put writes or replaces the record for entry.SeasonID and entry.PlayerID.
func (s *LeaderboardStore) Put(e Entry) error {
	return errNotImplemented
}

// Get reads the record for one player in one season.
func (s *LeaderboardStore) Get(seasonID, playerID string) (Entry, bool) {
	return Entry{}, false
}

// Entries lists every record of a season.
func (s *LeaderboardStore) Entries(seasonID string) []Entry {
	return nil
}

// Count reports how many records a season holds.
func (s *LeaderboardStore) Count(seasonID string) int {
	return 0
}

// Clear removes every record of a season.
func (s *LeaderboardStore) Clear(seasonID string) error {
	return errNotImplemented
}

// LeaderboardGuard keeps season resets, shards and rankings consistent.
type LeaderboardGuard struct {
}

// NewLeaderboardGuard builds a guard over the given store.
func NewLeaderboardGuard(store *LeaderboardStore) *LeaderboardGuard {
	return &LeaderboardGuard{}
}

// Store returns the store the guard writes through.
func (g *LeaderboardGuard) Store() *LeaderboardStore {
	return nil
}

// SubmitAt records a score in the season that owns the instant at.
func (g *LeaderboardGuard) SubmitAt(requestedSeason, playerID string, score int64, at time.Time) error {
	return errNotImplemented
}

// Score reads one player score in one season.
func (g *LeaderboardGuard) Score(seasonID, playerID string) (int64, bool) {
	return 0, false
}

// TopN returns the first n ranked rows of a season.
func (g *LeaderboardGuard) TopN(seasonID string, n int) []RankRow {
	return nil
}

// RankOf returns the rank of a player, including players outside the top 100.
func (g *LeaderboardGuard) RankOf(seasonID, playerID string) (int, bool) {
	return 0, false
}

// ResetSeasonAt moves the board from oldSeason to newSeason at the given instant.
func (g *LeaderboardGuard) ResetSeasonAt(oldSeason, newSeason string, at time.Time) error {
	return errNotImplemented
}

// ShardResetStamps reports the reset stamp each shard used for a season.
func (g *LeaderboardGuard) ShardResetStamps(seasonID string) []time.Time {
	return nil
}

// Archive returns the frozen historical rows of a season.
func (g *LeaderboardGuard) Archive(seasonID string) []Entry {
	return nil
}

// MergeRemote merges rows replicated from another server without double counting.
func (g *LeaderboardGuard) MergeRemote(seasonID string, entries []Entry) error {
	return errNotImplemented
}

// Records reports how many persisted rows exist for one player in one season.
func (g *LeaderboardGuard) Records(seasonID, playerID string) int {
	return 0
}

// SetResetHook installs a hook that runs inside the reset transaction.
func (g *LeaderboardGuard) SetResetHook(h func() error) error {
	return errNotImplemented
}

// CurrentSeason returns the season that receives new writes.
func (g *LeaderboardGuard) CurrentSeason() string {
	return ""
}
