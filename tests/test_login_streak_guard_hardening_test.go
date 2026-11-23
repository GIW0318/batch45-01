package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch45-game-a-21"
)

func TestConcurrentSameDayClaimsSingleEntryAndMail(t *testing.T) {
	g := impl.NewStreakGuard()
	now := dayIndex(100)

	const workers = 32
	results := make(chan impl.CheckInResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := g.Claim("acc-1", now, now)
			if err != nil {
				t.Errorf("Claim error: %v", err)
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)

	firstMailID := ""
	successes := 0
	for res := range results {
		if !res.Duplicate {
			successes++
		}
		if firstMailID == "" {
			firstMailID = res.MailID
		}
		if res.MailID != firstMailID {
			t.Fatalf("concurrent claims returned different mail ids: %q vs %q", res.MailID, firstMailID)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent claims = %d, want 1", successes)
	}
	if got := g.MailCount("acc-1"); got != 1 {
		t.Fatalf("mail count = %d, want 1", got)
	}
	if got := g.Streak("acc-1"); got != 1 {
		t.Fatalf("streak = %d, want 1", got)
	}
}

func TestRestoreKeepsBindingCalendarAndMakeupLimit(t *testing.T) {
	g := impl.NewStreakGuard()
	if err := g.BindRole("acc-1", "role-a"); err != nil {
		t.Fatalf("BindRole error: %v", err)
	}
	now := dayIndex(100)
	if _, err := g.ClaimAsRole("acc-1", "role-a", now); err != nil {
		t.Fatalf("ClaimAsRole error: %v", err)
	}
	for i := int64(0); i < 3; i++ {
		if _, err := g.MakeUp("acc-1", dayIndex(90+i), 1); err != nil {
			t.Fatalf("MakeUp %d error: %v", i, err)
		}
	}

	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot error: %v", err)
	}
	restored := impl.NewStreakGuard()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore error: %v", err)
	}

	res, err := restored.ClaimAsRole("acc-1", "role-a", now+10)
	if err != nil {
		t.Fatalf("ClaimAsRole after restore error: %v", err)
	}
	if !res.Duplicate {
		t.Fatalf("same-day claim after restore was not duplicate: %+v", res)
	}
	if _, err := restored.MakeUp("acc-1", dayIndex(95), 1); !errors.Is(err, impl.ErrMakeUpLimitExceeded) {
		t.Fatalf("makeup after restore error = %v, want ErrMakeUpLimitExceeded", err)
	}

	calendar, err := restored.Calendar("acc-1", now)
	if err != nil {
		t.Fatalf("Calendar after restore error: %v", err)
	}
	found := false
	for _, day := range calendar.Days {
		if day == 100 {
			found = true
		}
	}
	if !found {
		t.Fatalf("calendar after restore %v missing day 100", calendar.Days)
	}
}

func TestMakeUpFillsGapAndAppearsOnCalendar(t *testing.T) {
	g := impl.NewStreakGuard()
	for _, day := range []int64{100, 101} {
		if _, err := g.Claim("acc-1", dayIndex(day), dayIndex(day)); err != nil {
			t.Fatalf("claim day %d error: %v", day, err)
		}
	}
	res, err := g.MakeUp("acc-1", dayIndex(99), 1)
	if err != nil {
		t.Fatalf("makeup error: %v", err)
	}
	if res.Streak != 3 {
		t.Fatalf("streak after filling day 99 = %d, want 3", res.Streak)
	}
	calendar, err := g.Calendar("acc-1", dayIndex(101))
	if err != nil {
		t.Fatalf("Calendar error: %v", err)
	}
	want := []int64{99, 100, 101}
	if len(calendar.Days) != len(want) {
		t.Fatalf("calendar days = %v, want %v", calendar.Days, want)
	}
	for i := range want {
		if calendar.Days[i] != want[i] {
			t.Fatalf("calendar days = %v, want %v", calendar.Days, want)
		}
	}
}

func TestLedgerRetentionBoundsSnapshot(t *testing.T) {
	g := impl.NewStreakGuard()
	for day := int64(0); day < 720; day++ {
		if _, err := g.Claim("acc-1", dayIndex(day), dayIndex(day)); err != nil {
			t.Fatalf("claim day %d error: %v", day, err)
		}
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot error: %v", err)
	}
	if len(data) > 32*1024 {
		t.Fatalf("snapshot size = %d bytes, want bounded ledger", len(data))
	}
	if got := g.Streak("acc-1"); got != 720 {
		t.Fatalf("streak after 720 consecutive days = %d, want 720", got)
	}
	restored := impl.NewStreakGuard()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore error: %v", err)
	}
	if got := restored.Streak("acc-1"); got != 720 {
		t.Fatalf("restored streak = %d, want 720", got)
	}
	old, err := g.Calendar("acc-1", dayIndex(100))
	if err != nil {
		t.Fatalf("old Calendar error: %v", err)
	}
	if len(old.Days) != 0 {
		t.Fatalf("expired season still has %d calendar days", len(old.Days))
	}
	current, err := g.Calendar("acc-1", dayIndex(719))
	if err != nil {
		t.Fatalf("current Calendar error: %v", err)
	}
	if len(current.Days) == 0 {
		t.Fatalf("current calendar is empty")
	}
}

func TestUnknownAccountAndRoleReportSentinel(t *testing.T) {
	g := impl.NewStreakGuard()
	now := dayIndex(100)
	if _, err := g.ClaimAsRole("acc-1", "missing-role", now); !errors.Is(err, impl.ErrUnknownAccount) {
		t.Fatalf("unbound role error = %v, want ErrUnknownAccount", err)
	}
	source := impl.NewStreakGuard()
	if err := g.SyncFrom(source, "missing-account"); !errors.Is(err, impl.ErrUnknownAccount) {
		t.Fatalf("sync missing account error = %v, want ErrUnknownAccount", err)
	}
}
