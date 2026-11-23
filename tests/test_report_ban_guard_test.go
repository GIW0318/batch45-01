package task

import (
	"errors"
	"fmt"
	"testing"

	impl "gsb/batch45-game-a-23"
)

const reporterSecret = "reporter-7f3a-secret"

func mustReport(t *testing.T, g *impl.ReportBanGuard, id, target, reporter string, evidence []string) {
	t.Helper()
	if _, err := g.SubmitReport(id, target, reporter, evidence, 1000); err != nil {
		t.Fatalf("SubmitReport(%s) error: %v", id, err)
	}
}

func TestReviewCreatesBanAtomically(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", reporterSecret, []string{"shot-a.png"})
	ban, err := g.Review("rep-1", "mod-1", 600, 1000)
	if err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if ban.ReportID != "rep-1" {
		t.Fatalf("ban report id = %q, want rep-1", ban.ReportID)
	}
	stored, ok := g.BanOf("p1")
	if !ok {
		t.Fatalf("ban was not stored together with the report")
	}
	if !stored.Active {
		t.Fatalf("stored ban is not active")
	}
	if !g.IsBanned("p1") {
		t.Fatalf("player p1 is not banned after review")
	}
}

func TestReviewUnknownReportLeavesNoBan(t *testing.T) {
	g := impl.NewReportBanGuard()
	if _, err := g.Review("rep-x", "mod-1", 600, 1000); !errors.Is(err, impl.ErrUnknownReport) {
		t.Fatalf("review unknown report error = %v, want ErrUnknownReport", err)
	}
	if g.IsBanned("p1") {
		t.Fatalf("unknown report produced a ban")
	}
}

func TestDuplicateReviewRejected(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", reporterSecret, []string{"shot-a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("first review error: %v", err)
	}
	if _, err := g.Review("rep-1", "mod-2", 600, 1100); !errors.Is(err, impl.ErrAlreadyReviewed) {
		t.Fatalf("second review error = %v, want ErrAlreadyReviewed", err)
	}
	if got := g.BanCount("p1"); got != 1 {
		t.Fatalf("ban count after duplicate review = %d, want 1", got)
	}
}

func TestBanDurationNotAccumulated(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	mustReport(t, g, "rep-2", "p1", "reporter-b", []string{"b.png"})
	mustReport(t, g, "rep-3", "p1", "reporter-c", []string{"c.png"})
	for i, dur := range []int64{600, 300, 1200} {
		id := fmt.Sprintf("rep-%d", i+1)
		if _, err := g.Review(id, "mod-1", dur, 1000+int64(i)); err != nil {
			t.Fatalf("review %s error: %v", id, err)
		}
	}
	ban, ok := g.BanOf("p1")
	if !ok {
		t.Fatalf("ban record missing")
	}
	if ban.DurationSec != 1200 {
		t.Fatalf("accumulated ban duration = %d, want max 1200", ban.DurationSec)
	}
}

func TestConcurrentReportsKeptDistinct(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	mustReport(t, g, "rep-2", "p1", "reporter-b", []string{"b.png"})
	if got := g.ReportsAgainst("p1"); got != 2 {
		t.Fatalf("distinct reports = %d, want 2 (must not be merged)", got)
	}
	if _, err := g.SubmitReport("rep-1", "p1", "reporter-c", nil, 1000); !errors.Is(err, impl.ErrReportExists) {
		t.Fatalf("duplicate report id error = %v, want ErrReportExists", err)
	}
	if got := g.ReportsAgainst("p1"); got != 2 {
		t.Fatalf("reports after duplicate id = %d, want 2", got)
	}
}

func TestEvidenceAttachedToReport(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", nil)
	mustReport(t, g, "rep-2", "p2", "reporter-b", nil)
	if err := g.AddEvidence("rep-1", "shot-1.png"); err != nil {
		t.Fatalf("AddEvidence error: %v", err)
	}
	ev, err := g.Evidence("rep-1")
	if err != nil {
		t.Fatalf("Evidence(rep-1) error: %v", err)
	}
	if len(ev) != 1 || ev[0] != "shot-1.png" {
		t.Fatalf("evidence of rep-1 = %v, want [shot-1.png]", ev)
	}
	other, err := g.Evidence("rep-2")
	if err != nil {
		t.Fatalf("Evidence(rep-2) error: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("evidence leaked across reports: %v", other)
	}
}

func TestReviewRequiresEvidence(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", nil)
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); !errors.Is(err, impl.ErrNoEvidence) {
		t.Fatalf("review without evidence error = %v, want ErrNoEvidence", err)
	}
}

func TestBanSuspendedWhileAppealPending(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	appeal, err := g.OpenAppeal("ap-1", "p1", 1000, 5000)
	if err != nil {
		t.Fatalf("OpenAppeal error: %v", err)
	}
	if appeal.Status != "pending" {
		t.Fatalf("appeal status = %q, want pending", appeal.Status)
	}
	if g.IsBanned("p1") {
		t.Fatalf("ban still enforced during appeal")
	}
}

func TestAppealTimeoutEscalatesNotRejects(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if _, err := g.OpenAppeal("ap-1", "p1", 1000, 2000); err != nil {
		t.Fatalf("OpenAppeal error: %v", err)
	}
	escalated, err := g.TickAppeals(2500)
	if err != nil {
		t.Fatalf("TickAppeals error: %v", err)
	}
	if len(escalated) != 1 {
		t.Fatalf("escalated appeals = %d, want 1", len(escalated))
	}
	if escalated[0].Status != "escalated" {
		t.Fatalf("overdue appeal status = %q, want escalated", escalated[0].Status)
	}
}

func TestReporterIdentityNotLeaked(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", reporterSecret, []string{"a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	view, ok := g.View("p1")
	if !ok {
		t.Fatalf("ban view missing")
	}
	if s := fmt.Sprintf("%+v", view); contains(s, reporterSecret) {
		t.Fatalf("ban view leaked the reporter identity: %s", s)
	}
	if s := fmt.Sprintf("%+v", g.Audit()); contains(s, reporterSecret) {
		t.Fatalf("audit trail leaked the reporter identity: %s", s)
	}
}

func TestReportBlockedDuringPendingAppeal(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if _, err := g.OpenAppeal("ap-1", "p1", 1000, 5000); err != nil {
		t.Fatalf("OpenAppeal error: %v", err)
	}
	if _, err := g.SubmitReport("rep-9", "p1", "reporter-b", []string{"b.png"}, 1200); !errors.Is(err, impl.ErrAppealPending) {
		t.Fatalf("report during appeal error = %v, want ErrAppealPending", err)
	}
}

func TestUnbanRestoresItemsAndResetsCount(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	res, err := g.LiftBan("p1", 3)
	if err != nil {
		t.Fatalf("LiftBan error: %v", err)
	}
	if res.RestoredItems != 3 {
		t.Fatalf("restored items = %d, want 3", res.RestoredItems)
	}
	if g.IsBanned("p1") {
		t.Fatalf("player still banned after lift")
	}
	if got := g.BanCount("p1"); got != 0 {
		t.Fatalf("ban count after lift = %d, want 0", got)
	}
}

func TestUpheldAppealKeepsBan(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if _, err := g.OpenAppeal("ap-1", "p1", 1000, 5000); err != nil {
		t.Fatalf("OpenAppeal error: %v", err)
	}
	if _, err := g.DecideAppeal("ap-1", true, 1500); err != nil {
		t.Fatalf("DecideAppeal error: %v", err)
	}
	if !g.IsBanned("p1") {
		t.Fatalf("upheld appeal lifted the ban")
	}
	appeal, ok := g.AppealOf("ap-1")
	if !ok {
		t.Fatalf("appeal record missing")
	}
	if appeal.Status != "upheld" {
		t.Fatalf("appeal status = %q, want upheld", appeal.Status)
	}
}

func TestAuditTrailCoversBanAndLift(t *testing.T) {
	g := impl.NewReportBanGuard()
	mustReport(t, g, "rep-1", "p1", "reporter-a", []string{"a.png"})
	if _, err := g.Review("rep-1", "mod-1", 600, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if _, err := g.LiftBan("p1", 0); err != nil {
		t.Fatalf("LiftBan error: %v", err)
	}
	kinds := map[string]bool{}
	for _, e := range g.Audit() {
		kinds[e.Kind] = true
	}
	if !kinds["ban"] {
		t.Fatalf("audit trail missing ban entry: %+v", g.Audit())
	}
	if !kinds["unban"] {
		t.Fatalf("audit trail missing unban entry: %+v", g.Audit())
	}
}

func TestCrossServerBanSync(t *testing.T) {
	src := impl.NewReportBanGuard()
	mustReport(t, src, "rep-1", "p1", "reporter-a", []string{"a.png"})
	if _, err := src.Review("rep-1", "mod-1", 900, 1000); err != nil {
		t.Fatalf("Review error: %v", err)
	}
	dst := impl.NewReportBanGuard()
	if err := dst.SyncBan(src, "p1"); err != nil {
		t.Fatalf("SyncBan error: %v", err)
	}
	if !dst.IsBanned("p1") {
		t.Fatalf("cross-server ban not applied")
	}
	ban, ok := dst.BanOf("p1")
	if !ok {
		t.Fatalf("synced ban record missing")
	}
	if ban.DurationSec != 900 {
		t.Fatalf("synced ban duration = %d, want 900", ban.DurationSec)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
