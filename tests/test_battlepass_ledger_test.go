package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch45-game-a-22"
)

func newLedger(t *testing.T) *impl.BattlePassLedger {
	t.Helper()
	return impl.NewBattlePassLedger("s1", 10)
}

func TestProgressAccumulatesPerAccount(t *testing.T) {
	b := newLedger(t)
	if err := b.BindRole("acc-1", "role-a"); err != nil {
		t.Fatalf("BindRole(role-a) error: %v", err)
	}
	if err := b.BindRole("acc-1", "role-b"); err != nil {
		t.Fatalf("BindRole(role-b) error: %v", err)
	}
	if _, err := b.AddExp("acc-1", "role-a", 60); err != nil {
		t.Fatalf("AddExp(role-a) error: %v", err)
	}
	total, err := b.AddExp("acc-1", "role-b", 70)
	if err != nil {
		t.Fatalf("AddExp(role-b) error: %v", err)
	}
	if total != 130 {
		t.Fatalf("account exp = %d, want 130", total)
	}
	if got := b.Exp("acc-1"); got != 130 {
		t.Fatalf("Exp = %d, want 130", got)
	}
}

func TestLevelDerivedFromExp(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 300); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if got := b.Level("acc-1"); got != 3 {
		t.Fatalf("Level = %d, want 3", got)
	}
}

func TestExpCappedAtMaxLevel(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 100000); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if got := b.Exp("acc-1"); got != 1000 {
		t.Fatalf("Exp = %d, want 1000 (capped at max level 10)", got)
	}
	if got := b.Level("acc-1"); got != 10 {
		t.Fatalf("Level = %d, want 10", got)
	}
}

func TestExpRollbackRejected(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 100); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if _, err := b.AddExp("acc-1", "role-a", -50); !errors.Is(err, impl.ErrExpRollback) {
		t.Fatalf("negative AddExp error = %v, want ErrExpRollback", err)
	}
	if got := b.Exp("acc-1"); got != 100 {
		t.Fatalf("Exp after rejected rollback = %d, want 100", got)
	}
}

func TestSeasonSwitchArchivesProgress(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 250); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	rec, err := b.SwitchSeason("s2", "acc-1")
	if err != nil {
		t.Fatalf("SwitchSeason error: %v", err)
	}
	if rec.Season != "s1" {
		t.Fatalf("archived season = %q, want s1", rec.Season)
	}
	if rec.Exp != 250 {
		t.Fatalf("archived exp = %d, want 250", rec.Exp)
	}
	if got := b.Exp("acc-1"); got != 0 {
		t.Fatalf("exp after season switch = %d, want 0", got)
	}
	if got := b.Season(); got != "s2" {
		t.Fatalf("active season = %q, want s2", got)
	}
}

func TestClaimRequiresUnlockedLevel(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 100); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if _, err := b.Claim("acc-1", 5, impl.TrackFree); !errors.Is(err, impl.ErrLevelLocked) {
		t.Fatalf("claim locked level error = %v, want ErrLevelLocked", err)
	}
}

func TestClaimGrantsOncePerLevelAndTrack(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 500); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	first, err := b.Claim("acc-1", 2, impl.TrackFree)
	if err != nil {
		t.Fatalf("first claim error: %v", err)
	}
	if first.AlreadyClaimed {
		t.Fatalf("first claim marked already claimed")
	}
	second, err := b.Claim("acc-1", 2, impl.TrackFree)
	if err != nil {
		t.Fatalf("duplicate claim error: %v", err)
	}
	if !second.AlreadyClaimed {
		t.Fatalf("duplicate claim was not flagged: %+v", second)
	}
	if got := b.GrantCount("acc-1", 2, impl.TrackFree); got != 1 {
		t.Fatalf("grant count = %d, want 1", got)
	}
	if got := b.MailCount("acc-1"); got != 1 {
		t.Fatalf("mail count = %d, want 1", got)
	}
}

func TestFreeAndPaidTracksIndependent(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 500); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if err := b.Purchase("acc-1"); err != nil {
		t.Fatalf("Purchase error: %v", err)
	}
	if _, err := b.Claim("acc-1", 3, impl.TrackFree); err != nil {
		t.Fatalf("free claim error: %v", err)
	}
	if _, err := b.Claim("acc-1", 3, impl.TrackPaid); err != nil {
		t.Fatalf("paid claim error: %v", err)
	}
	if got := b.GrantCount("acc-1", 3, impl.TrackFree); got != 1 {
		t.Fatalf("free grant count = %d, want 1", got)
	}
	if got := b.GrantCount("acc-1", 3, impl.TrackPaid); got != 1 {
		t.Fatalf("paid grant count = %d, want 1", got)
	}
}

func TestPaidClaimWithoutPurchaseRejected(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 500); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if _, err := b.Claim("acc-1", 3, impl.TrackPaid); !errors.Is(err, impl.ErrTrackNotPurchased) {
		t.Fatalf("paid claim without purchase error = %v, want ErrTrackNotPurchased", err)
	}
	if got := b.MailCount("acc-1"); got != 0 {
		t.Fatalf("mail count = %d, want 0", got)
	}
}

func TestConcurrentClaimSameLevelSingleMail(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 500); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Claim("acc-1", 1, impl.TrackFree)
		}()
	}
	wg.Wait()
	if got := b.GrantCount("acc-1", 1, impl.TrackFree); got != 1 {
		t.Fatalf("grant count after concurrent claims = %d, want 1", got)
	}
	if got := b.MailCount("acc-1"); got != 1 {
		t.Fatalf("mail count after concurrent claims = %d, want 1", got)
	}
}

func TestCompensationTasksSorted(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 500); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	applied, err := b.ApplyCompensation("acc-1", []int{3, 1, 2})
	if err != nil {
		t.Fatalf("ApplyCompensation error: %v", err)
	}
	want := []int{1, 2, 3}
	if len(applied) != len(want) {
		t.Fatalf("applied levels = %v, want %v", applied, want)
	}
	for i := range want {
		if applied[i] != want[i] {
			t.Fatalf("applied levels = %v, want ascending %v", applied, want)
		}
	}
}

func TestClaimAfterSeasonCloseRejected(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 500); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if _, err := b.Claim("acc-1", 1, impl.TrackFree); !errors.Is(err, impl.ErrSeasonClosed) {
		t.Fatalf("claim after close error = %v, want ErrSeasonClosed", err)
	}
}

func TestRewardMailOnlyOnSuccessfulClaim(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 100); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if _, err := b.Claim("acc-1", 4, impl.TrackFree); err == nil {
		t.Fatalf("claim of locked level unexpectedly succeeded")
	}
	if got := b.MailCount("acc-1"); got != 0 {
		t.Fatalf("failed claim left %d mails, want 0", got)
	}
}

func TestRestoreKeepsClaimedLevels(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 500); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if _, err := b.Claim("acc-1", 1, impl.TrackFree); err != nil {
		t.Fatalf("claim error: %v", err)
	}
	data, err := b.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot error: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot returned no data")
	}
	restored := impl.NewBattlePassLedger("s1", 10)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore error: %v", err)
	}
	res, err := restored.Claim("acc-1", 1, impl.TrackFree)
	if err != nil {
		t.Fatalf("claim after restore error: %v", err)
	}
	if !res.AlreadyClaimed {
		t.Fatalf("restored ledger lost the claimed level: %+v", res)
	}
}

func TestCrossServerExpSyncKeepsHigher(t *testing.T) {
	b := newLedger(t)
	if _, err := b.AddExp("acc-1", "role-a", 300); err != nil {
		t.Fatalf("AddExp error: %v", err)
	}
	if err := b.SyncExp("acc-1", 500); err != nil {
		t.Fatalf("SyncExp(500) error: %v", err)
	}
	if got := b.Exp("acc-1"); got != 500 {
		t.Fatalf("Exp after sync = %d, want 500", got)
	}
	if err := b.SyncExp("acc-1", 100); err != nil {
		t.Fatalf("SyncExp(100) error: %v", err)
	}
	if got := b.Exp("acc-1"); got != 500 {
		t.Fatalf("lower sync rolled exp back to %d, want 500", got)
	}
}
