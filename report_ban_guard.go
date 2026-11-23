package task

import "errors"

var (
	ErrNotImplemented  = errors.New("not implemented")
	ErrUnknownReport   = errors.New("unknown report")
	ErrReportExists    = errors.New("report already exists")
	ErrAlreadyReviewed = errors.New("report already reviewed")
	ErrNoEvidence      = errors.New("report has no evidence")
	ErrAppealPending   = errors.New("appeal pending for player")
	ErrUnknownAppeal   = errors.New("unknown appeal")
	ErrNotBanned       = errors.New("player is not banned")
)

// Report is one player report with bound evidence.
type Report struct {
	ID         string
	TargetID   string
	ReporterID string
	Evidence   []string
	Reviewed   bool
}

// Ban is the ban state of one player.
type Ban struct {
	PlayerID    string
	ReportID    string
	DurationSec int64
	Active      bool
}

// BanView is the reporter-anonymous view of a ban.
type BanView struct {
	PlayerID    string
	DurationSec int64
	Active      bool
}

// Appeal is one ban appeal.
type Appeal struct {
	ID       string
	PlayerID string
	Status   string
	Deadline int64
}

// LiftResult is the outcome of lifting a ban.
type LiftResult struct {
	PlayerID      string
	RestoredItems int
	BanCount      int
}

// AuditEntry is one audit trail record.
type AuditEntry struct {
	Kind   string
	Actor  string
	Detail string
}

// ReportBanGuard guards the report and ban flow.
type ReportBanGuard struct{}

// NewReportBanGuard returns an empty guard.
func NewReportBanGuard() *ReportBanGuard { return &ReportBanGuard{} }

// SubmitReport records a report and its evidence atomically.
func (g *ReportBanGuard) SubmitReport(reportID, targetID, reporterID string, evidence []string, now int64) (Report, error) {
	return Report{}, ErrNotImplemented
}

// AddEvidence binds one evidence screenshot to an existing report.
func (g *ReportBanGuard) AddEvidence(reportID, url string) error { return ErrNotImplemented }

// Evidence returns the evidence bound to a report.
func (g *ReportBanGuard) Evidence(reportID string) ([]string, error) { return nil, ErrNotImplemented }

// Review reviews a report and bans the target in the same transaction.
func (g *ReportBanGuard) Review(reportID, reviewerID string, durationSec, now int64) (Ban, error) {
	return Ban{}, ErrNotImplemented
}

// BanOf returns the ban record of a player.
func (g *ReportBanGuard) BanOf(playerID string) (Ban, bool) { return Ban{}, false }

// IsBanned reports whether the player is currently banned.
func (g *ReportBanGuard) IsBanned(playerID string) bool { return false }

// BanCount reports how many bans the player received.
func (g *ReportBanGuard) BanCount(playerID string) int { return 0 }

// ReportsAgainst reports how many distinct reports target the player.
func (g *ReportBanGuard) ReportsAgainst(targetID string) int { return 0 }

// OpenAppeal opens an appeal for a banned player.
func (g *ReportBanGuard) OpenAppeal(appealID, playerID string, now, deadline int64) (Appeal, error) {
	return Appeal{}, ErrNotImplemented
}

// AppealOf returns one appeal by id.
func (g *ReportBanGuard) AppealOf(appealID string) (Appeal, bool) { return Appeal{}, false }

// DecideAppeal resolves an appeal.
func (g *ReportBanGuard) DecideAppeal(appealID string, upheld bool, now int64) (LiftResult, error) {
	return LiftResult{}, ErrNotImplemented
}

// TickAppeals escalates overdue appeals instead of silently rejecting them.
func (g *ReportBanGuard) TickAppeals(now int64) ([]Appeal, error) { return nil, ErrNotImplemented }

// LiftBan lifts the ban and restores confiscated items.
func (g *ReportBanGuard) LiftBan(playerID string, restoreItems int) (LiftResult, error) {
	return LiftResult{}, ErrNotImplemented
}

// SyncBan imports a ban from another shard.
func (g *ReportBanGuard) SyncBan(from *ReportBanGuard, playerID string) error {
	return ErrNotImplemented
}

// View returns the anonymous ban view of a player.
func (g *ReportBanGuard) View(playerID string) (BanView, bool) { return BanView{}, false }

// Audit returns the audit trail.
func (g *ReportBanGuard) Audit() []AuditEntry { return nil }
