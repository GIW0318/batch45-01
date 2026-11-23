package task

import "errors"

var (
	ErrNotImplemented      = errors.New("not implemented")
	ErrInsufficientTickets = errors.New("not enough makeup tickets")
	ErrMakeUpLimitExceeded = errors.New("makeup check-in limit exceeded")
	ErrUnknownAccount      = errors.New("unknown account")
)

// CheckInResult is the outcome of one daily check-in attempt.
type CheckInResult struct {
	AccountID string
	ServerDay int64
	Streak    int
	Duplicate bool
	MailID    string
}

// MakeUpResult is the outcome of a retroactive (makeup) check-in.
type MakeUpResult struct {
	AccountID   string
	ServerDay   int64
	TicketsLeft int
	Streak      int
}

// CalendarView is the cached check-in calendar of one account.
type CalendarView struct {
	AccountID string
	Version   int
	Days      []int64
}

// StreakGuard guards the daily check-in flow of an account.
type StreakGuard struct{}

// NewStreakGuard returns an empty guard.
func NewStreakGuard() *StreakGuard { return &StreakGuard{} }

// BindRole binds a game role to an account so that merged roles share one claim.
func (g *StreakGuard) BindRole(accountID, roleID string) error { return ErrNotImplemented }

// Claim performs the daily check-in using the server clock as the only day source.
func (g *StreakGuard) Claim(accountID string, serverTime, clientTime int64) (CheckInResult, error) {
	return CheckInResult{}, ErrNotImplemented
}

// ClaimAsRole performs the daily check-in for a bound role.
func (g *StreakGuard) ClaimAsRole(accountID, roleID string, serverTime int64) (CheckInResult, error) {
	return CheckInResult{}, ErrNotImplemented
}

// MakeUp performs a retroactive check-in and deducts makeup tickets.
func (g *StreakGuard) MakeUp(accountID string, serverDay int64, tickets int) (MakeUpResult, error) {
	return MakeUpResult{}, ErrNotImplemented
}

// Streak reports the current consecutive check-in count.
func (g *StreakGuard) Streak(accountID string) int { return 0 }

// MailCount reports how many reward mails were sent to the account.
func (g *StreakGuard) MailCount(accountID string) int { return 0 }

// Calendar returns the calendar view of the account for the given server day.
func (g *StreakGuard) Calendar(accountID string, serverDay int64) (CalendarView, error) {
	return CalendarView{}, ErrNotImplemented
}

// InvalidateCalendarCache drops the cached calendar of the account.
func (g *StreakGuard) InvalidateCalendarCache(accountID string) error { return ErrNotImplemented }

// AddEventPoints adds activity points that must not share the check-in counter.
func (g *StreakGuard) AddEventPoints(accountID string, points int) error { return ErrNotImplemented }

// EventPoints reports the activity points of the account.
func (g *StreakGuard) EventPoints(accountID string) int { return 0 }

// SyncFrom merges the check-in state of the account from another shard.
func (g *StreakGuard) SyncFrom(remote *StreakGuard, accountID string) error { return ErrNotImplemented }

// Snapshot serializes the guard state so that it survives a restart.
func (g *StreakGuard) Snapshot() ([]byte, error) { return nil, ErrNotImplemented }

// Restore rebuilds the guard state from a snapshot.
func (g *StreakGuard) Restore(data []byte) error { return ErrNotImplemented }
