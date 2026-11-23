package task

import (
	"errors"
	"testing"

	impl "gsb/batch45-game-a-21"
)

const secPerDay int64 = 86400

func dayIndex(n int64) int64 { return secPerDay * n }

func TestMergedRolesShareSingleClaim(t *testing.T) {
	g := impl.NewStreakGuard()
	if err := g.BindRole("acc-1", "role-a"); err != nil {
		t.Fatalf("BindRole(role-a) error: %v", err)
	}
	if err := g.BindRole("acc-1", "role-b"); err != nil {
		t.Fatalf("BindRole(role-b) error: %v", err)
	}
	now := dayIndex(100)
	first, err := g.ClaimAsRole("acc-1", "role-a", now)
	if err != nil {
		t.Fatalf("first claim error: %v", err)
	}
	if first.Duplicate {
		t.Fatalf("first claim unexpectedly marked duplicate")
	}
	second, err := g.ClaimAsRole("acc-1", "role-b", now)
	if err != nil {
		t.Fatalf("second claim error: %v", err)
	}
	if !second.Duplicate {
		t.Fatalf("claim from role-b must be a duplicate: %+v", second)
	}
	if got := g.MailCount("acc-1"); got != 1 {
		t.Fatalf("reward mail count = %d, want 1", got)
	}
}

func TestDifferentAccountsClaimIndependently(t *testing.T) {
	g := impl.NewStreakGuard()
	now := dayIndex(100)
	r1, err := g.Claim("acc-1", now, now)
	if err != nil {
		t.Fatalf("claim acc-1 error: %v", err)
	}
	if r1.Duplicate {
		t.Fatalf("acc-1 first claim marked duplicate")
	}
	r2, err := g.Claim("acc-2", now, now)
	if err != nil {
		t.Fatalf("claim acc-2 error: %v", err)
	}
	if r2.Duplicate {
		t.Fatalf("acc-2 claim must not be blocked by acc-1: %+v", r2)
	}
	if got := g.MailCount("acc-2"); got != 1 {
		t.Fatalf("acc-2 reward mail count = %d, want 1", got)
	}
}

func TestMakeUpDeductsOneTicket(t *testing.T) {
	g := impl.NewStreakGuard()
	res, err := g.MakeUp("acc-1", dayIndex(99), 2)
	if err != nil {
		t.Fatalf("makeup error: %v", err)
	}
	if res.TicketsLeft != 1 {
		t.Fatalf("tickets left = %d, want 1", res.TicketsLeft)
	}
	if res.Streak != 1 {
		t.Fatalf("streak after makeup = %d, want 1", res.Streak)
	}
}

func TestMakeUpRejectedWithoutTicket(t *testing.T) {
	g := impl.NewStreakGuard()
	if _, err := g.MakeUp("acc-1", dayIndex(99), 0); !errors.Is(err, impl.ErrInsufficientTickets) {
		t.Fatalf("makeup without tickets error = %v, want ErrInsufficientTickets", err)
	}
}

func TestDayBoundaryUsesServerClock(t *testing.T) {
	g := impl.NewStreakGuard()
	server := dayIndex(100)
	client := dayIndex(103)
	if _, err := g.Claim("acc-1", server, client); err != nil {
		t.Fatalf("first claim error: %v", err)
	}
	res, err := g.Claim("acc-1", server+60, client)
	if err != nil {
		t.Fatalf("second claim error: %v", err)
	}
	if !res.Duplicate {
		t.Fatalf("client clock advanced 3 days but server day is still 100: %+v", res)
	}
}

func TestRewardMailSentOncePerDay(t *testing.T) {
	g := impl.NewStreakGuard()
	now := dayIndex(100)
	first, err := g.Claim("acc-1", now, now)
	if err != nil {
		t.Fatalf("first claim error: %v", err)
	}
	second, err := g.Claim("acc-1", now+5, now+5)
	if err != nil {
		t.Fatalf("duplicate claim error: %v", err)
	}
	if first.MailID == "" {
		t.Fatalf("first claim returned empty mail id")
	}
	if second.MailID != first.MailID {
		t.Fatalf("duplicate claim produced a second mail: %q vs %q", second.MailID, first.MailID)
	}
	if got := g.MailCount("acc-1"); got != 1 {
		t.Fatalf("reward mail count = %d, want 1", got)
	}
}

func TestReconnectResubmitIsIdempotent(t *testing.T) {
	g := impl.NewStreakGuard()
	now := dayIndex(100)
	if _, err := g.Claim("acc-1", now, now); err != nil {
		t.Fatalf("first claim error: %v", err)
	}
	streakAfterFirst := g.Streak("acc-1")
	res, err := g.Claim("acc-1", now, now)
	if err != nil {
		t.Fatalf("reconnect resubmit must not error: %v", err)
	}
	if !res.Duplicate {
		t.Fatalf("reconnect resubmit must be flagged duplicate: %+v", res)
	}
	if got := g.Streak("acc-1"); got != streakAfterFirst {
		t.Fatalf("reconnect resubmit changed streak from %d to %d", streakAfterFirst, got)
	}
}

func TestStreakResetsAfterGap(t *testing.T) {
	g := impl.NewStreakGuard()
	for _, d := range []int64{100, 101} {
		if _, err := g.Claim("acc-1", dayIndex(d), dayIndex(d)); err != nil {
			t.Fatalf("claim day %d error: %v", d, err)
		}
	}
	res, err := g.Claim("acc-1", dayIndex(105), dayIndex(105))
	if err != nil {
		t.Fatalf("claim day 105 error: %v", err)
	}
	if res.Streak != 1 {
		t.Fatalf("streak after 3 day gap = %d, want 1", res.Streak)
	}
}

func TestStreakIncrementsOnConsecutiveDays(t *testing.T) {
	g := impl.NewStreakGuard()
	want := []int{1, 2, 3}
	for i, d := range []int64{100, 101, 102} {
		res, err := g.Claim("acc-1", dayIndex(d), dayIndex(d))
		if err != nil {
			t.Fatalf("claim day %d error: %v", d, err)
		}
		if res.Streak != want[i] {
			t.Fatalf("streak after day %d = %d, want %d", d, res.Streak, want[i])
		}
	}
}

func TestCalendarCacheRefreshedAfterClaim(t *testing.T) {
	g := impl.NewStreakGuard()
	before, err := g.Calendar("acc-1", dayIndex(100))
	if err != nil {
		t.Fatalf("calendar before claim error: %v", err)
	}
	now := dayIndex(100)
	if _, err := g.Claim("acc-1", now, now); err != nil {
		t.Fatalf("claim error: %v", err)
	}
	after, err := g.Calendar("acc-1", dayIndex(100))
	if err != nil {
		t.Fatalf("calendar after claim error: %v", err)
	}
	if after.Version <= before.Version {
		t.Fatalf("calendar cache version %d did not grow past %d", after.Version, before.Version)
	}
	found := false
	for _, d := range after.Days {
		if d == 100 {
			found = true
		}
	}
	if !found {
		t.Fatalf("calendar days %v missing freshly claimed day 100", after.Days)
	}
}

func TestCrossServerSyncBlocksSecondClaim(t *testing.T) {
	src := impl.NewStreakGuard()
	now := dayIndex(100)
	if _, err := src.Claim("acc-1", now, now); err != nil {
		t.Fatalf("source claim error: %v", err)
	}
	dst := impl.NewStreakGuard()
	if err := dst.SyncFrom(src, "acc-1"); err != nil {
		t.Fatalf("sync error: %v", err)
	}
	res, err := dst.Claim("acc-1", now+10, now+10)
	if err != nil {
		t.Fatalf("claim after sync error: %v", err)
	}
	if !res.Duplicate {
		t.Fatalf("cross-server claim after sync must be duplicate: %+v", res)
	}
	if got := dst.MailCount("acc-1"); got != 0 {
		t.Fatalf("destination sent %d extra mails, want 0", got)
	}
}

func TestMakeUpLimitEnforced(t *testing.T) {
	g := impl.NewStreakGuard()
	for i := int64(0); i < 3; i++ {
		if _, err := g.MakeUp("acc-1", dayIndex(90+i), 1); err != nil {
			t.Fatalf("makeup %d error: %v", i, err)
		}
	}
	if _, err := g.MakeUp("acc-1", dayIndex(95), 1); !errors.Is(err, impl.ErrMakeUpLimitExceeded) {
		t.Fatalf("fourth makeup error = %v, want ErrMakeUpLimitExceeded", err)
	}
}

func TestEventCounterIndependentOfStreak(t *testing.T) {
	g := impl.NewStreakGuard()
	if err := g.AddEventPoints("acc-1", 500); err != nil {
		t.Fatalf("AddEventPoints error: %v", err)
	}
	now := dayIndex(100)
	res, err := g.Claim("acc-1", now, now)
	if err != nil {
		t.Fatalf("claim error: %v", err)
	}
	if res.Streak != 1 {
		t.Fatalf("event points leaked into streak counter: streak = %d", res.Streak)
	}
	if got := g.EventPoints("acc-1"); got != 500 {
		t.Fatalf("event points = %d, want 500", got)
	}
}

func TestSnapshotRestoreKeepsState(t *testing.T) {
	g := impl.NewStreakGuard()
	now := dayIndex(100)
	if _, err := g.Claim("acc-1", now, now); err != nil {
		t.Fatalf("claim error: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot error: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot returned no data")
	}
	restored := impl.NewStreakGuard()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if got := restored.Streak("acc-1"); got != 1 {
		t.Fatalf("restored streak = %d, want 1", got)
	}
	res, err := restored.Claim("acc-1", now+1, now+1)
	if err != nil {
		t.Fatalf("claim after restore error: %v", err)
	}
	if !res.Duplicate {
		t.Fatalf("restored state lost the same-day claim: %+v", res)
	}
}

func TestNextServerDayAllowsNewClaim(t *testing.T) {
	g := impl.NewStreakGuard()
	first, err := g.Claim("acc-1", dayIndex(100), dayIndex(100))
	if err != nil {
		t.Fatalf("day 100 claim error: %v", err)
	}
	if first.Duplicate {
		t.Fatalf("day 100 first claim marked duplicate")
	}
	second, err := g.Claim("acc-1", dayIndex(101), dayIndex(101))
	if err != nil {
		t.Fatalf("day 101 claim error: %v", err)
	}
	if second.Duplicate {
		t.Fatalf("day 101 claim marked duplicate: %+v", second)
	}
	if got := g.MailCount("acc-1"); got != 2 {
		t.Fatalf("reward mail count = %d, want 2", got)
	}
}
