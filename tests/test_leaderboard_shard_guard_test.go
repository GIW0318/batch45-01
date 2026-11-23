package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-12"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func stamp(i int) time.Time {
	return time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
}

func TestSubmitStoresSeasonAndScore(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 900, stamp(1)))
	score, ok := g.Score("s1", "p1")
	if !ok || score != 900 {
		t.Fatalf("score = %d, ok = %v, want 900/true", score, ok)
	}
}

func TestResetClearsPersistentScores(t *testing.T) {
	store := impl.NewLeaderboardStore(4)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 900, stamp(1)))
	must(t, g.ResetSeasonAt("s1", "s2", stamp(2)))
	if _, ok := store.Get("s1", "p1"); ok {
		t.Fatalf("old season record still persisted after reset")
	}
	if got := store.Count("s1"); got != 0 {
		t.Fatalf("old season still holds %d records after reset", got)
	}
}

func TestResetKeepsOtherSeasons(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 100, stamp(1)))
	must(t, g.SubmitAt("s2", "p1", 200, stamp(1)))
	must(t, g.ResetSeasonAt("s1", "s3", stamp(2)))
	if score, ok := g.Score("s2", "p1"); !ok || score != 200 {
		t.Fatalf("season s2 was touched by the s1 reset: score=%d ok=%v", score, ok)
	}
}

func TestResetStampsAllShardsAlike(t *testing.T) {
	store := impl.NewLeaderboardStore(4)
	g := impl.NewLeaderboardGuard(store)
	at := stamp(10)
	must(t, g.ResetSeasonAt("s1", "s2", at))
	stamps := g.ShardResetStamps("s2")
	if len(stamps) != 4 {
		t.Fatalf("shard reset stamps = %d, want 4", len(stamps))
	}
	for i, ts := range stamps {
		if !ts.Equal(at) {
			t.Fatalf("shard %d reset at %v, want %v", i, ts, at)
		}
	}
}

func TestPlayerHasSingleRecordPerSeason(t *testing.T) {
	store := impl.NewLeaderboardStore(4)
	g := impl.NewLeaderboardGuard(store)
	for i := 0; i < 5; i++ {
		must(t, g.SubmitAt("s1", "p1", int64(i+1)*10, stamp(i)))
	}
	if got := g.Records("s1", "p1"); got != 1 {
		t.Fatalf("player has %d records, want 1", got)
	}
	if got := store.Count("s1"); got != 1 {
		t.Fatalf("season holds %d records, want 1", got)
	}
	if score, _ := g.Score("s1", "p1"); score != 50 {
		t.Fatalf("score = %d, want the latest 50", score)
	}
}

func TestSeasonIDIsPartOfTheKey(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 100, stamp(1)))
	must(t, g.SubmitAt("s2", "p1", 200, stamp(1)))
	if score, ok := g.Score("s1", "p1"); !ok || score != 100 {
		t.Fatalf("s1 score = %d ok=%v, want 100", score, ok)
	}
	if score, ok := g.Score("s2", "p1"); !ok || score != 200 {
		t.Fatalf("s2 score = %d ok=%v, want 200", score, ok)
	}
}

func TestWriteAfterResetGoesToNewSeason(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	at := stamp(5)
	must(t, g.ResetSeasonAt("s1", "s2", at))
	must(t, g.SubmitAt("s1", "p1", 50, at.Add(time.Second)))
	if score, ok := g.Score("s2", "p1"); !ok || score != 50 {
		t.Fatalf("write after reset landed in s2=%d ok=%v, want 50/true", score, ok)
	}
	if _, ok := g.Score("s1", "p1"); ok {
		t.Fatalf("write after reset leaked into the old season")
	}
}

func TestWriteBeforeResetStaysInOldSeason(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	at := stamp(5)
	must(t, g.SubmitAt("s1", "p1", 70, at.Add(-time.Second)))
	if score, ok := g.Score("s1", "p1"); !ok || score != 70 {
		t.Fatalf("pre-reset write in s1=%d ok=%v, want 70/true", score, ok)
	}
	if _, ok := g.Score("s2", "p1"); ok {
		t.Fatalf("pre-reset write leaked into the new season")
	}
	must(t, g.ResetSeasonAt("s1", "s2", at))
	if _, ok := g.Score("s1", "p1"); ok {
		t.Fatalf("reset did not freeze the old season record")
	}
}

func TestTieBreakByUpdatedAt(t *testing.T) {
	store := impl.NewLeaderboardStore(1)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 100, stamp(2)))
	must(t, g.SubmitAt("s1", "p2", 100, stamp(1)))
	rows := g.TopN("s1", 2)
	if len(rows) != 2 {
		t.Fatalf("top rows = %d, want 2", len(rows))
	}
	if rows[0].PlayerID != "p2" || rows[1].PlayerID != "p1" {
		t.Fatalf("tie order = %v, want the earlier score p2 first", rows)
	}
}

func TestTieBreakDeterministicAcrossCalls(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	at := stamp(3)
	must(t, g.SubmitAt("s1", "p2", 100, at))
	must(t, g.SubmitAt("s1", "p1", 100, at))
	first := g.TopN("s1", 2)
	second := g.TopN("s1", 2)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("top rows = %d and %d, want 2", len(first), len(second))
	}
	for i := range first {
		if first[i].PlayerID != second[i].PlayerID {
			t.Fatalf("ranking is not stable between calls: %v vs %v", first, second)
		}
	}
	if first[0].PlayerID != "p1" || first[1].PlayerID != "p2" {
		t.Fatalf("equal score and time must order by player id, got %v", first)
	}
}

func TestRankOfPlayerOutsideTop100(t *testing.T) {
	store := impl.NewLeaderboardStore(4)
	g := impl.NewLeaderboardGuard(store)
	for i := 1; i <= 150; i++ {
		id := "p" + pad3(i)
		must(t, g.SubmitAt("s1", id, int64(i), stamp(i%30)))
	}
	if got := store.Count("s1"); got != 150 {
		t.Fatalf("season holds %d records, want 150", got)
	}
	if rank, ok := g.RankOf("s1", "p150"); !ok || rank != 1 {
		t.Fatalf("rank of the top player = %d ok=%v, want 1", rank, ok)
	}
	if rank, ok := g.RankOf("s1", "p001"); !ok || rank != 150 {
		t.Fatalf("rank of the lowest player = %d ok=%v, want 150 outside the top 100", rank, ok)
	}
}

func TestTopNTruncatesAndSorts(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	for i := 1; i <= 25; i++ {
		must(t, g.SubmitAt("s1", "p"+pad3(i), int64(i*10), stamp(i)))
	}
	rows := g.TopN("s1", 10)
	if len(rows) != 10 {
		t.Fatalf("top rows = %d, want 10", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Score < rows[i].Score {
			t.Fatalf("rows not sorted descending at %d: %v", i, rows)
		}
	}
	if rows[0].PlayerID != "p025" || rows[0].Rank != 1 {
		t.Fatalf("rank one = %v, want p025", rows[0])
	}
}

func TestMergeRemoteDoesNotDoubleCount(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 100, stamp(1)))
	must(t, g.MergeRemote("s1", []impl.Entry{
		{SeasonID: "s1", PlayerID: "p1", Score: 100, UpdatedAt: stamp(1), ShardID: "remote"},
		{SeasonID: "s1", PlayerID: "p2", Score: 60, UpdatedAt: stamp(1), ShardID: "remote"},
	}))
	if got := g.Records("s1", "p1"); got != 1 {
		t.Fatalf("p1 has %d records after merge, want 1", got)
	}
	if score, _ := g.Score("s1", "p1"); score != 100 {
		t.Fatalf("p1 score = %d after merge, want 100 instead of a doubled value", score)
	}
	if score, ok := g.Score("s1", "p2"); !ok || score != 60 {
		t.Fatalf("merged p2 = %d ok=%v, want 60", score, ok)
	}
}

func TestResetRollsBackOnFailure(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	must(t, g.SubmitAt("s1", "p1", 100, stamp(1)))
	must(t, g.SetResetHook(func() error { return errors.New("boom") }))
	if err := g.ResetSeasonAt("s1", "s2", stamp(2)); err == nil {
		t.Fatalf("reset must fail when the hook fails")
	}
	if score, ok := g.Score("s1", "p1"); !ok || score != 100 {
		t.Fatalf("old season was not rolled back: score=%d ok=%v", score, ok)
	}
	if _, ok := g.Score("s2", "p1"); ok {
		t.Fatalf("failed reset left data in the new season")
	}
	must(t, g.SetResetHook(nil))
	must(t, g.ResetSeasonAt("s1", "s2", stamp(3)))
	if _, ok := g.Score("s1", "p1"); ok {
		t.Fatalf("retry after a clean hook did not clear the old season")
	}
}

func TestTopNStableDuringConcurrentUpdates(t *testing.T) {
	store := impl.NewLeaderboardStore(2)
	g := impl.NewLeaderboardGuard(store)
	for i := 0; i < 5; i++ {
		must(t, g.SubmitAt("s1", "p"+pad3(i), int64(i+1)*10, stamp(1)))
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				_ = g.SubmitAt("s1", "p"+pad3(i%5), int64((i+1)*(w+1)), stamp(i))
			}
		}(w)
	}
	for i := 0; i < 100; i++ {
		rows := g.TopN("s1", 5)
		if len(rows) != 5 {
			t.Fatalf("snapshot %d returned %d rows, want 5", i, len(rows))
		}
		seen := map[string]bool{}
		for j, row := range rows {
			if seen[row.PlayerID] {
				t.Fatalf("snapshot %d has duplicate player %s", i, row.PlayerID)
			}
			seen[row.PlayerID] = true
			if j > 0 && rows[j-1].Score < row.Score {
				t.Fatalf("snapshot %d is not sorted: %v", i, rows)
			}
		}
	}
	wg.Wait()
}

func pad3(n int) string {
	s := "000"
	d := []byte(s)
	for i := len(d) - 1; i >= 0 && n > 0; i-- {
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d)
}
