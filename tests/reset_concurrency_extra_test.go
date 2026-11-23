package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-12"
)

func TestExtraConcurrentSubmitAndReset(t *testing.T) {
	store := impl.NewLeaderboardStore(8)
	g := impl.NewLeaderboardGuard(store)
	at := stamp(100)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = g.SubmitAt("s1", fmt.Sprintf("p%d", w*200+i), int64(i), at.Add(-time.Minute))
			}
		}(w)
	}
	wg.Wait()
	must(t, g.ResetSeasonAt("s1", "s2", at))
	if got := store.Count("s1"); got != 0 {
		t.Fatalf("old season holds %d records after concurrent reset", got)
	}
	if got := g.CurrentSeason(); got != "s2" {
		t.Fatalf("current season = %q, want s2", got)
	}
	// Writes stamped after the reset must land in the new season only.
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = g.SubmitAt("s1", fmt.Sprintf("p%d", w*50+i), int64(i), at.Add(time.Minute))
			}
		}(w)
	}
	wg.Wait()
	if got := store.Count("s1"); got != 0 {
		t.Fatalf("post-reset writes leaked into old season: %d records", got)
	}
	if got := store.Count("s2"); got != 400 {
		t.Fatalf("new season holds %d records, want 400", got)
	}
}

func TestExtraResetRollbackUnderConcurrency(t *testing.T) {
	store := impl.NewLeaderboardStore(4)
	g := impl.NewLeaderboardGuard(store)
	for i := 0; i < 50; i++ {
		must(t, g.SubmitAt("s1", fmt.Sprintf("p%d", i), int64(i*10), stamp(1)))
	}
	must(t, g.SetResetHook(func() error { return errors.New("boom") }))
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if err := g.ResetSeasonAt("s1", "s2", stamp(2)); err == nil {
					t.Error("reset must fail while the hook fails")
				}
			}
		}()
	}
	wg.Wait()
	if got := store.Count("s1"); got != 50 {
		t.Fatalf("rolled-back season holds %d records, want 50", got)
	}
	if got := store.Count("s2"); got != 0 {
		t.Fatalf("failed resets left %d records in the new season", got)
	}
	if got := g.CurrentSeason(); got != "s1" {
		t.Fatalf("current season = %q after rollback, want s1", got)
	}
	must(t, g.SetResetHook(nil))
	must(t, g.ResetSeasonAt("s1", "s2", stamp(3)))
	if got := store.Count("s1"); got != 0 {
		t.Fatalf("season holds %d records after the clean retry", got)
	}
}

func TestExtraArchiveFreezesOldSeason(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 100, stamp(2)))
	must(t, g.SubmitAt("s1", "p2", 100, stamp(1)))
	must(t, g.ResetSeasonAt("s1", "s2", stamp(3)))
	rows := g.Archive("s1")
	if len(rows) != 2 {
		t.Fatalf("archive holds %d rows, want 2", len(rows))
	}
	if rows[0].PlayerID != "p2" || rows[1].PlayerID != "p1" {
		t.Fatalf("archive order = %v, want p2 before p1", rows)
	}
	if got := g.Archive("s2"); got != nil {
		t.Fatalf("active season must not be archived, got %v", got)
	}
}

func TestExtraMergeRemoteKeepsHigherScoreAndOriginalStamp(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 100, stamp(1)))
	must(t, g.MergeRemote("s1", []impl.Entry{
		{SeasonID: "s1", PlayerID: "p1", Score: 250, UpdatedAt: stamp(9), ShardID: "remote"},
		{SeasonID: "s1", PlayerID: "p1", Score: 40, UpdatedAt: stamp(8), ShardID: "remote"},
		{SeasonID: "s1", PlayerID: "p1", Score: 250, UpdatedAt: stamp(9), ShardID: "remote"},
	}))
	if got := g.Records("s1", "p1"); got != 1 {
		t.Fatalf("p1 has %d records after merging duplicates, want 1", got)
	}
	e, ok := store.Get("s1", "p1")
	if !ok || e.Score != 250 {
		t.Fatalf("merged score = %d ok=%v, want the higher 250", e.Score, ok)
	}
	if !e.UpdatedAt.Equal(stamp(1)) {
		t.Fatalf("UpdatedAt = %v, want the original %v", e.UpdatedAt, stamp(1))
	}
	if err := g.MergeRemote("", nil); err == nil {
		t.Fatalf("empty season must be rejected")
	}
	if err := g.MergeRemote("s1", []impl.Entry{{PlayerID: ""}}); err == nil {
		t.Fatalf("empty player id must be rejected")
	}
}

func TestExtraRankOfScalesWithoutFullSort(t *testing.T) {
	store := impl.NewLeaderboardStore(16)
	g := impl.NewLeaderboardGuard(store)
	const n = 100000
	for i := 0; i < n; i++ {
		must(t, g.SubmitAt("s1", fmt.Sprintf("p%06d", i), int64(i), stamp(1)))
	}
	start := time.Now()
	const queries = 200
	for i := 0; i < queries; i++ {
		id := fmt.Sprintf("p%06d", (i*499)%n)
		if _, ok := g.RankOf("s1", id); !ok {
			t.Fatalf("rank of %s not found", id)
		}
	}
	elapsed := time.Since(start)
	if rank, ok := g.RankOf("s1", "p099999"); !ok || rank != 1 {
		t.Fatalf("top rank = %d ok=%v, want 1", rank, ok)
	}
	if rank, ok := g.RankOf("s1", "p000000"); !ok || rank != n {
		t.Fatalf("bottom rank = %d ok=%v, want %d", rank, ok, n)
	}
	if _, ok := g.RankOf("s1", "ghost"); ok {
		t.Fatalf("unknown player must have no rank")
	}
	if avg := elapsed / queries; avg > 5*time.Millisecond {
		t.Fatalf("RankOf averaged %v per call at %d rows, want millisecond-level", avg, n)
	}
}

func TestExtraInvalidInputRejected(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	if err := g.SubmitAt("", "p1", 1, stamp(1)); err == nil {
		t.Fatalf("empty season must be rejected")
	}
	if err := g.SubmitAt("s1", "", 1, stamp(1)); err == nil {
		t.Fatalf("empty player must be rejected")
	}
	if err := g.ResetSeasonAt("s1", "s1", stamp(1)); err == nil {
		t.Fatalf("resetting a season into itself must be rejected")
	}
	if err := g.ResetSeasonAt("", "s2", stamp(1)); err == nil {
		t.Fatalf("empty old season must be rejected")
	}
	if rows := g.TopN("s1", 0); rows != nil {
		t.Fatalf("non-positive n must yield no rows, got %v", rows)
	}
}
