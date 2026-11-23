package task

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrNotImplemented      = errors.New("not implemented")
	ErrInsufficientTickets = errors.New("not enough makeup tickets")
	ErrMakeUpLimitExceeded = errors.New("makeup check-in limit exceeded")
	ErrUnknownAccount      = errors.New("unknown account")

	errInvalidSnapshot = errors.New("invalid streak snapshot")
	errRoleConflict    = errors.New("role already bound to another account")
)

const (
	secondsPerDay    int64 = 86400
	seasonLengthDays int64 = 90
	retainedSeasons  int64 = 2
	maxMakeUps             = 3
	snapshotFormat   int   = 1
	maxSnapshotBytes       = 64 << 20
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

type ledgerEntry struct {
	Day        int64  `json:"day"`
	Season     int64  `json:"season"`
	Kind       string `json:"kind"`
	Sealed     bool   `json:"sealed,omitempty"`
	ServerTime int64  `json:"server_time,omitempty"`
	ClientTime int64  `json:"client_time,omitempty"`
}

type accountState struct {
	AccountID       string        `json:"account_id"`
	Roles           []string      `json:"roles,omitempty"`
	LastClaimDay    int64         `json:"last_claim_day"`
	LastMailID      string        `json:"last_mail_id,omitempty"`
	LastClientTime  int64         `json:"last_client_time,omitempty"`
	Streak          int           `json:"streak"`
	MailCount       int           `json:"mail_count"`
	MakeUpCount     int           `json:"makeup_count"`
	EventPoints     int           `json:"event_points"`
	CalendarVersion int           `json:"calendar_version"`
	Ledger          []ledgerEntry `json:"ledger"`
}

type snapshotData struct {
	FormatVersion int               `json:"format_version"`
	Bindings      map[string]string `json:"bindings"`
	Accounts      []*accountState   `json:"accounts"`
}

// StreakGuard guards the daily check-in flow of an account.
type StreakGuard struct {
	mu       sync.Mutex
	accounts map[string]*accountState
	roles    map[string]string
}

// NewStreakGuard returns an empty guard.
func NewStreakGuard() *StreakGuard {
	return &StreakGuard{
		accounts: make(map[string]*accountState),
		roles:    make(map[string]string),
	}
}

func (g *StreakGuard) ensureLocked() {
	if g.accounts == nil {
		g.accounts = make(map[string]*accountState)
	}
	if g.roles == nil {
		g.roles = make(map[string]string)
	}
}

func newAccount(accountID string) *accountState {
	return &accountState{
		AccountID:    accountID,
		LastClaimDay: -1,
		Ledger:       []ledgerEntry{},
	}
}

func (g *StreakGuard) accountLocked(accountID string) *accountState {
	g.ensureLocked()
	account := g.accounts[accountID]
	if account == nil {
		account = newAccount(accountID)
		g.accounts[accountID] = account
	}
	return account
}

func dayOf(serverTime int64) int64 {
	day := serverTime / secondsPerDay
	if serverTime < 0 && serverTime%secondsPerDay != 0 {
		day--
	}
	return day
}

func seasonOf(day int64) int64 {
	season := day / seasonLengthDays
	if day < 0 && day%seasonLengthDays != 0 {
		season--
	}
	return season
}

func mailIDFor(accountID string, day int64) string {
	sum := sha256.Sum256([]byte(accountID))
	return fmt.Sprintf("streak-%x-%d", sum[:8], day)
}

func normalizeLedger(entries []ledgerEntry) []ledgerEntry {
	if len(entries) == 0 {
		return []ledgerEntry{}
	}

	byDay := make(map[int64]ledgerEntry, len(entries))
	for _, entry := range entries {
		if entry.Kind == "" {
			entry.Kind = "claim"
		}
		current, ok := byDay[entry.Day]
		if !ok {
			byDay[entry.Day] = entry
			continue
		}
		if current.Kind != "claim" && entry.Kind == "claim" {
			byDay[entry.Day] = entry
		}
	}

	days := make([]int64, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	latestSeason := seasonOf(days[len(days)-1])
	oldestSeason := latestSeason - (retainedSeasons - 1)
	out := make([]ledgerEntry, 0, len(days))
	for _, day := range days {
		season := seasonOf(day)
		if season < oldestSeason {
			continue
		}
		entry := byDay[day]
		entry.Season = season
		entry.Sealed = season < latestSeason
		out = append(out, entry)
	}
	return out
}

func streakFromLedger(entries []ledgerEntry) int {
	if len(entries) == 0 {
		return 0
	}
	days := make(map[int64]struct{}, len(entries))
	for _, entry := range entries {
		days[entry.Day] = struct{}{}
	}
	day := entries[len(entries)-1].Day
	streak := 0
	for {
		if _, ok := days[day]; !ok {
			break
		}
		streak++
		day--
	}
	return streak
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func bumpStreak(lastDay int64, streak int, day int64, computed int) int {
	next := 1
	if lastDay >= 0 && streak > 0 {
		firstDay := lastDay - int64(streak) + 1
		switch {
		case day == lastDay+1 || day == firstDay-1:
			next = streak + 1
		case day > lastDay:
			next = 1
		default:
			next = streak
		}
	}
	return maxInt(next, computed)
}

func mergeStreaks(lastA int64, streakA int, lastB int64, streakB int, computed int) int {
	if lastA < 0 || streakA <= 0 {
		return maxInt(streakB, computed)
	}
	if lastB < 0 || streakB <= 0 {
		return maxInt(streakA, computed)
	}

	firstA := lastA - int64(streakA) + 1
	firstB := lastB - int64(streakB) + 1
	if firstA <= lastB+1 && firstB <= lastA+1 {
		first := firstA
		if firstB < first {
			first = firstB
		}
		last := lastA
		if lastB > last {
			last = lastB
		}
		return maxInt(int(last-first+1), computed)
	}
	if lastA > lastB {
		return maxInt(streakA, computed)
	}
	return maxInt(streakB, computed)
}

func ledgerHasDay(entries []ledgerEntry, day int64) bool {
	index := sort.Search(len(entries), func(i int) bool {
		return entries[i].Day >= day
	})
	return index < len(entries) && entries[index].Day == day
}

func checkedInDay(account *accountState, day int64) bool {
	return account.LastClaimDay == day || ledgerHasDay(account.Ledger, day)
}

func addRole(roles []string, roleID string) []string {
	index := sort.SearchStrings(roles, roleID)
	if index < len(roles) && roles[index] == roleID {
		return roles
	}
	roles = append(roles, "")
	copy(roles[index+1:], roles[index:])
	roles[index] = roleID
	return roles
}

func cloneAccount(account *accountState) *accountState {
	clone := *account
	clone.Roles = append([]string(nil), account.Roles...)
	clone.Ledger = append([]ledgerEntry(nil), account.Ledger...)
	return &clone
}

// BindRole binds a game role to an account so that merged roles share one claim.
func (g *StreakGuard) BindRole(accountID, roleID string) error {
	if accountID == "" || roleID == "" {
		return ErrUnknownAccount
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.ensureLocked()
	if owner, ok := g.roles[roleID]; ok && owner != accountID {
		return fmt.Errorf("%w: %s", errRoleConflict, roleID)
	}

	account := g.accountLocked(accountID)
	account.Roles = addRole(account.Roles, roleID)
	g.roles[roleID] = accountID
	return nil
}

// Claim performs the daily check-in using the server clock as the only day source.
func (g *StreakGuard) Claim(accountID string, serverTime, clientTime int64) (CheckInResult, error) {
	if accountID == "" {
		return CheckInResult{}, ErrUnknownAccount
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	return g.claimLocked(accountID, serverTime, clientTime)
}

func (g *StreakGuard) claimLocked(accountID string, serverTime, clientTime int64) (CheckInResult, error) {
	account := g.accountLocked(accountID)
	day := dayOf(serverTime)
	account.LastClientTime = clientTime

	if checkedInDay(account, day) {
		mailID := account.LastMailID
		if account.LastClaimDay != day || mailID == "" {
			mailID = mailIDFor(accountID, day)
		}
		return CheckInResult{
			AccountID: accountID,
			ServerDay: day,
			Streak:    account.Streak,
			Duplicate: true,
			MailID:    mailID,
		}, nil
	}

	lastDay := account.LastClaimDay
	previousStreak := account.Streak
	account.Ledger = normalizeLedger(append(account.Ledger, ledgerEntry{
		Day:        day,
		Kind:       "claim",
		ServerTime: serverTime,
		ClientTime: clientTime,
	}))
	account.Streak = bumpStreak(lastDay, previousStreak, day, streakFromLedger(account.Ledger))
	account.MailCount++
	account.CalendarVersion++

	if day > account.LastClaimDay {
		account.LastClaimDay = day
		account.LastMailID = mailIDFor(accountID, day)
	} else if account.LastMailID == "" {
		account.LastMailID = mailIDFor(accountID, account.LastClaimDay)
	}

	return CheckInResult{
		AccountID: accountID,
		ServerDay: day,
		Streak:    account.Streak,
		MailID:    mailIDFor(accountID, day),
	}, nil
}

// ClaimAsRole performs the daily check-in for a bound role.
func (g *StreakGuard) ClaimAsRole(accountID, roleID string, serverTime int64) (CheckInResult, error) {
	if accountID == "" || roleID == "" {
		return CheckInResult{}, ErrUnknownAccount
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	owner, ok := g.roles[roleID]
	if !ok || owner != accountID {
		return CheckInResult{}, ErrUnknownAccount
	}
	return g.claimLocked(accountID, serverTime, serverTime)
}

// MakeUp performs a retroactive check-in and deducts makeup tickets.
func (g *StreakGuard) MakeUp(accountID string, serverDay int64, tickets int) (MakeUpResult, error) {
	if accountID == "" {
		return MakeUpResult{}, ErrUnknownAccount
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	account := g.accountLocked(accountID)
	day := dayOf(serverDay)
	result := MakeUpResult{
		AccountID:   accountID,
		ServerDay:   day,
		TicketsLeft: tickets,
		Streak:      account.Streak,
	}

	if account.MakeUpCount >= maxMakeUps {
		return result, ErrMakeUpLimitExceeded
	}
	if tickets <= 0 {
		return result, ErrInsufficientTickets
	}
	if checkedInDay(account, day) {
		return result, nil
	}

	lastDay := account.LastClaimDay
	previousStreak := account.Streak
	result.TicketsLeft = tickets - 1
	account.MakeUpCount++
	account.Ledger = normalizeLedger(append(account.Ledger, ledgerEntry{
		Day:  day,
		Kind: "makeup",
	}))
	account.Streak = bumpStreak(lastDay, previousStreak, day, streakFromLedger(account.Ledger))
	account.CalendarVersion++
	if day > account.LastClaimDay {
		account.LastClaimDay = day
		if account.LastMailID == "" {
			account.LastMailID = mailIDFor(accountID, day)
		}
	}
	result.Streak = account.Streak
	return result, nil
}

// Streak reports the current consecutive check-in count.
func (g *StreakGuard) Streak(accountID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if account := g.accounts[accountID]; account != nil {
		return account.Streak
	}
	return 0
}

// MailCount reports how many reward mails were sent to the account.
func (g *StreakGuard) MailCount(accountID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if account := g.accounts[accountID]; account != nil {
		return account.MailCount
	}
	return 0
}

// Calendar returns the calendar view of the account for the given server day.
func (g *StreakGuard) Calendar(accountID string, serverDay int64) (CalendarView, error) {
	if accountID == "" {
		return CalendarView{}, ErrUnknownAccount
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	account := g.accountLocked(accountID)
	day := dayOf(serverDay)
	season := seasonOf(day)
	days := make([]int64, 0, seasonLengthDays)
	for _, entry := range account.Ledger {
		if seasonOf(entry.Day) == season {
			days = append(days, entry.Day)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	return CalendarView{
		AccountID: accountID,
		Version:   account.CalendarVersion,
		Days:      days,
	}, nil
}

// InvalidateCalendarCache drops the cached calendar of the account.
func (g *StreakGuard) InvalidateCalendarCache(accountID string) error {
	if accountID == "" {
		return ErrUnknownAccount
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	account := g.accounts[accountID]
	if account == nil {
		return ErrUnknownAccount
	}
	account.CalendarVersion++
	return nil
}

// AddEventPoints adds activity points that must not share the check-in counter.
func (g *StreakGuard) AddEventPoints(accountID string, points int) error {
	if accountID == "" {
		return ErrUnknownAccount
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	account := g.accountLocked(accountID)
	account.EventPoints += points
	return nil
}

// EventPoints reports the activity points of the account.
func (g *StreakGuard) EventPoints(accountID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if account := g.accounts[accountID]; account != nil {
		return account.EventPoints
	}
	return 0
}

// SyncFrom merges the check-in state of the account from another shard.
func (g *StreakGuard) SyncFrom(remote *StreakGuard, accountID string) error {
	if remote == nil || accountID == "" {
		return ErrUnknownAccount
	}

	remote.mu.Lock()
	source := remote.accounts[accountID]
	if source == nil {
		remote.mu.Unlock()
		return ErrUnknownAccount
	}
	sourceCopy := cloneAccount(source)
	sourceRoles := make([]string, 0, len(source.Roles))
	sourceRoles = append(sourceRoles, source.Roles...)
	for roleID, owner := range remote.roles {
		if owner == accountID {
			sourceRoles = append(sourceRoles, roleID)
		}
	}
	remote.mu.Unlock()

	sourceRoles = uniqueSorted(sourceRoles)

	g.mu.Lock()
	defer g.mu.Unlock()

	g.ensureLocked()
	for _, roleID := range sourceRoles {
		if owner, ok := g.roles[roleID]; ok && owner != accountID {
			return fmt.Errorf("%w: %s", errRoleConflict, roleID)
		}
	}

	account := g.accountLocked(accountID)
	lastDay := account.LastClaimDay
	previousStreak := account.Streak
	for _, roleID := range sourceRoles {
		account.Roles = addRole(account.Roles, roleID)
		g.roles[roleID] = accountID
	}

	merged := append([]ledgerEntry(nil), account.Ledger...)
	merged = append(merged, sourceCopy.Ledger...)
	account.Ledger = normalizeLedger(merged)
	account.Streak = mergeStreaks(
		lastDay,
		previousStreak,
		sourceCopy.LastClaimDay,
		sourceCopy.Streak,
		streakFromLedger(account.Ledger),
	)
	account.CalendarVersion++

	if sourceCopy.LastClaimDay > account.LastClaimDay {
		account.LastClaimDay = sourceCopy.LastClaimDay
		if sourceCopy.LastMailID != "" {
			account.LastMailID = sourceCopy.LastMailID
		} else {
			account.LastMailID = mailIDFor(accountID, account.LastClaimDay)
		}
		account.LastClientTime = sourceCopy.LastClientTime
	}
	if sourceCopy.MakeUpCount > account.MakeUpCount {
		account.MakeUpCount = sourceCopy.MakeUpCount
	}
	if sourceCopy.EventPoints > account.EventPoints {
		account.EventPoints = sourceCopy.EventPoints
	}
	return nil
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

// Snapshot serializes the guard state so that it survives a restart.
func (g *StreakGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.ensureLocked()
	bindings := make(map[string]string, len(g.roles))
	for roleID, accountID := range g.roles {
		bindings[roleID] = accountID
	}

	accounts := make([]*accountState, 0, len(g.accounts))
	for _, account := range g.accounts {
		copy := cloneAccount(account)
		copy.Roles = uniqueSorted(append([]string(nil), copy.Roles...))
		copy.Ledger = normalizeLedger(copy.Ledger)
		accounts = append(accounts, copy)
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].AccountID < accounts[j].AccountID
	})

	data, err := json.Marshal(snapshotData{
		FormatVersion: snapshotFormat,
		Bindings:      bindings,
		Accounts:      accounts,
	})
	if err != nil {
		return nil, err
	}
	if len(data) > maxSnapshotBytes {
		return nil, fmt.Errorf("%w: snapshot exceeds %d bytes", errInvalidSnapshot, maxSnapshotBytes)
	}
	return data, nil
}

// Restore rebuilds the guard state from a snapshot.
func (g *StreakGuard) Restore(data []byte) error {
	if len(data) == 0 || len(data) > maxSnapshotBytes {
		return errInvalidSnapshot
	}

	var snapshot snapshotData
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("%w: %v", errInvalidSnapshot, err)
	}
	if snapshot.FormatVersion != 0 && snapshot.FormatVersion != snapshotFormat {
		return fmt.Errorf("%w: unsupported format %d", errInvalidSnapshot, snapshot.FormatVersion)
	}

	accounts := make(map[string]*accountState, len(snapshot.Accounts))
	for _, saved := range snapshot.Accounts {
		if saved == nil || saved.AccountID == "" {
			return fmt.Errorf("%w: empty account id", errInvalidSnapshot)
		}
		if _, exists := accounts[saved.AccountID]; exists {
			return fmt.Errorf("%w: duplicate account %s", errInvalidSnapshot, saved.AccountID)
		}
		account := cloneAccount(saved)
		account.Roles = uniqueSorted(append([]string(nil), account.Roles...))
		account.Ledger = normalizeLedger(account.Ledger)
		if len(account.Ledger) > 0 && account.LastClaimDay < account.Ledger[len(account.Ledger)-1].Day {
			account.LastClaimDay = account.Ledger[len(account.Ledger)-1].Day
		}
		computed := streakFromLedger(account.Ledger)
		if account.LastClaimDay < 0 {
			account.Streak = computed
		} else {
			account.Streak = maxInt(account.Streak, computed)
		}
		if account.LastClaimDay < -1 {
			account.LastClaimDay = -1
		}
		accounts[account.AccountID] = account
	}

	roles := make(map[string]string, len(snapshot.Bindings))
	for roleID, accountID := range snapshot.Bindings {
		if roleID == "" || accountID == "" {
			return fmt.Errorf("%w: empty role binding", errInvalidSnapshot)
		}
		if accounts[accountID] == nil {
			accounts[accountID] = newAccount(accountID)
		}
		if owner, ok := roles[roleID]; ok && owner != accountID {
			return fmt.Errorf("%w: role %s bound twice", errInvalidSnapshot, roleID)
		}
		roles[roleID] = accountID
		accounts[accountID].Roles = addRole(accounts[accountID].Roles, roleID)
	}
	for _, account := range accounts {
		for _, roleID := range account.Roles {
			if roleID == "" {
				return fmt.Errorf("%w: empty role", errInvalidSnapshot)
			}
			if owner, ok := roles[roleID]; ok && owner != account.AccountID {
				return fmt.Errorf("%w: role %s belongs to %s", errInvalidSnapshot, roleID, owner)
			}
			roles[roleID] = account.AccountID
		}
	}

	g.mu.Lock()
	g.accounts = accounts
	g.roles = roles
	g.mu.Unlock()
	return nil
}
