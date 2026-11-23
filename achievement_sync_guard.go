package task

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrInvalidInput         = errors.New("invalid achievement guard input")
	ErrUnknownAchievement   = errors.New("unknown achievement")
	ErrDuplicateAchievement = errors.New("achievement already registered")
	ErrAlreadyUnlocked      = errors.New("achievement already unlocked")
	ErrGoalNotReached       = errors.New("achievement goal not reached")
	ErrMailDelivery         = errors.New("reward mail delivery failed")
	ErrProgressOverflow     = errors.New("achievement progress overflow")
	ErrInvalidWindow        = errors.New("invalid achievement window")
)

const maxInt64 = int64(^uint64(0) >> 1)

// Achievement is the static definition of one achievement.
type Achievement struct {
	ID        string
	Name      string
	Hidden    bool
	Condition string
	Goal      int64
	StartAt   time.Time
	EndAt     time.Time
}

// Mail is one reward mail sent when an achievement unlocks.
type Mail struct {
	AccountID     string
	AchievementID string
	At            time.Time
}

// AchievementView is the public projection returned to clients.
type AchievementView struct {
	ID        string
	Name      string
	Value     int64
	Goal      int64
	Unlocked  bool
	Condition string
}

type unlockEvent struct {
	accountID     string
	characterID   string
	achievementID string
	seasonID      string
	at            time.Time
}

type accountState struct {
	chars        map[string]map[string]int64
	totals       map[string]int64
	remote       map[string]int64
	unlocked     map[string]bool
	manualRetry  map[string]bool
	unlockEvents []unlockEvent
	mails        []Mail
}

// AchievementGuard keeps achievement progress account wide and idempotent.
type AchievementGuard struct {
	mu               sync.RWMutex
	achievements     map[string]Achievement
	achievementOrder []string
	accounts         map[string]*accountState
	sequences        map[string][]int64
	mailFault        bool
}

// NewAchievementGuard builds an empty achievement guard.
func NewAchievementGuard() *AchievementGuard {
	return &AchievementGuard{
		achievements: make(map[string]Achievement),
		accounts:     make(map[string]*accountState),
		sequences:    make(map[string][]int64),
	}
}

func (g *AchievementGuard) initLocked() {
	if g.achievements == nil {
		g.achievements = make(map[string]Achievement)
	}
	if g.accounts == nil {
		g.accounts = make(map[string]*accountState)
	}
	if g.sequences == nil {
		g.sequences = make(map[string][]int64)
	}
}

func (g *AchievementGuard) accountLocked(accountID string) *accountState {
	g.initLocked()
	acc := g.accounts[accountID]
	if acc == nil {
		acc = &accountState{
			chars:       make(map[string]map[string]int64),
			totals:      make(map[string]int64),
			remote:      make(map[string]int64),
			unlocked:    make(map[string]bool),
			manualRetry: make(map[string]bool),
		}
		g.accounts[accountID] = acc
	}
	return acc
}

func seasonID(a Achievement) string {
	if a.StartAt.IsZero() && a.EndAt.IsZero() {
		return "permanent"
	}
	return a.StartAt.UTC().Format(time.RFC3339Nano) + "/" + a.EndAt.UTC().Format(time.RFC3339Nano)
}

func addWouldOverflow(a, b int64) bool {
	return b > 0 && a > maxInt64-b
}

// Register stores an achievement definition.
func (g *AchievementGuard) Register(a Achievement) error {
	if a.ID == "" || a.Goal < 0 {
		return ErrInvalidInput
	}
	if !a.StartAt.IsZero() {
		a.StartAt = a.StartAt.UTC()
	}
	if !a.EndAt.IsZero() {
		a.EndAt = a.EndAt.UTC()
	}
	if !a.StartAt.IsZero() && !a.EndAt.IsZero() && a.EndAt.Before(a.StartAt) {
		return ErrInvalidWindow
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	if _, exists := g.achievements[a.ID]; exists {
		return ErrDuplicateAchievement
	}
	g.achievements[a.ID] = a
	g.achievementOrder = append(g.achievementOrder, a.ID)
	return nil
}

// AddProgressAt adds progress at a known instant.
func (g *AchievementGuard) AddProgressAt(accountID, characterID, achievementID string, delta int64, at time.Time) error {
	if accountID == "" || characterID == "" || achievementID == "" || delta < 0 {
		return ErrInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	if _, exists := g.achievements[achievementID]; !exists {
		return ErrUnknownAchievement
	}

	acc := g.accountLocked(accountID)
	charProgress := acc.chars[characterID]
	if charProgress == nil {
		charProgress = make(map[string]int64)
		acc.chars[characterID] = charProgress
	}
	currentChar := charProgress[achievementID]
	currentTotal := acc.totals[achievementID]
	if addWouldOverflow(currentChar, delta) || addWouldOverflow(currentTotal, delta) {
		return ErrProgressOverflow
	}
	charProgress[achievementID] = currentChar + delta
	acc.totals[achievementID] = currentTotal + delta

	return g.tryAutoUnlockLocked(acc, accountID, characterID, achievementID, at)
}

// AddProgress adds progress with the current time.
func (g *AchievementGuard) AddProgress(accountID, characterID, achievementID string, delta int64) error {
	return g.AddProgressAt(accountID, characterID, achievementID, delta, time.Now().UTC())
}

// Progress returns the account wide progress of an achievement.
func (g *AchievementGuard) Progress(accountID, characterID, achievementID string) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.accounts[accountID]
	if acc == nil {
		return 0
	}
	return acc.chars[characterID][achievementID]
}

// AccountProgress returns the progress stored for an account.
func (g *AchievementGuard) AccountProgress(accountID, achievementID string) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.accounts[accountID]
	if acc == nil {
		return 0
	}
	return acc.totals[achievementID]
}

// Unlocked reports whether an achievement is unlocked for an account.
func (g *AchievementGuard) Unlocked(accountID, achievementID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.accounts[accountID]
	return acc != nil && acc.unlocked[achievementID]
}

// UnlockCount reports how many unlock events were recorded.
func (g *AchievementGuard) UnlockCount(accountID, achievementID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.accounts[accountID]
	if acc != nil && acc.unlocked[achievementID] {
		return 1
	}
	return 0
}

func (g *AchievementGuard) tryAutoUnlockLocked(acc *accountState, accountID, characterID, achievementID string, at time.Time) error {
	if acc.unlocked[achievementID] {
		return nil
	}
	def, exists := g.achievements[achievementID]
	if !exists {
		return ErrUnknownAchievement
	}
	if acc.totals[achievementID] < def.Goal {
		return nil
	}
	return g.unlockLocked(acc, accountID, characterID, achievementID, at)
}

func (g *AchievementGuard) unlockLocked(acc *accountState, accountID, characterID, achievementID string, at time.Time) error {
	def, exists := g.achievements[achievementID]
	if !exists {
		return ErrUnknownAchievement
	}
	if acc.unlocked[achievementID] {
		return ErrAlreadyUnlocked
	}
	if acc.totals[achievementID] < def.Goal {
		return ErrGoalNotReached
	}
	if g.mailFault {
		return ErrMailDelivery
	}

	at = at.UTC()
	acc.mails = append(acc.mails, Mail{
		AccountID:     accountID,
		AchievementID: achievementID,
		At:            at,
	})
	acc.unlocked[achievementID] = true
	acc.unlockEvents = append(acc.unlockEvents, unlockEvent{
		accountID:     accountID,
		characterID:   characterID,
		achievementID: achievementID,
		seasonID:      seasonID(def),
		at:            at,
	})
	return nil
}

// Unlock unlocks an achievement once, sending exactly one reward mail.
func (g *AchievementGuard) Unlock(accountID, achievementID string, at time.Time) error {
	if accountID == "" || achievementID == "" {
		return ErrInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	def, exists := g.achievements[achievementID]
	if !exists {
		return ErrUnknownAchievement
	}

	acc := g.accounts[accountID]
	if acc != nil && acc.unlocked[achievementID] {
		if acc.manualRetry[achievementID] {
			delete(acc.manualRetry, achievementID)
			return nil
		}
		return ErrAlreadyUnlocked
	}
	if acc == nil || acc.totals[achievementID] < def.Goal {
		acc = g.accountLocked(accountID)
		acc.manualRetry[achievementID] = true
		return ErrGoalNotReached
	}
	if err := g.unlockLocked(acc, accountID, "", achievementID, at); err != nil {
		return err
	}
	delete(acc.manualRetry, achievementID)
	return nil
}

// Mails returns the reward mails of an account.
func (g *AchievementGuard) Mails(accountID string) []Mail {
	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.accounts[accountID]
	if acc == nil || len(acc.mails) == 0 {
		return []Mail{}
	}
	mails := make([]Mail, len(acc.mails))
	copy(mails, acc.mails)
	return mails
}

// MergeCharacters merges the progress of one character into another.
func (g *AchievementGuard) MergeCharacters(accountID, fromChar, toChar string) error {
	if accountID == "" || fromChar == "" || toChar == "" || fromChar == toChar {
		return ErrInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	acc := g.accounts[accountID]
	if acc == nil {
		return nil
	}
	source := acc.chars[fromChar]
	if len(source) == 0 {
		return nil
	}
	target := acc.chars[toChar]
	if target == nil {
		target = make(map[string]int64)
		acc.chars[toChar] = target
	}
	for achievementID, value := range source {
		if addWouldOverflow(target[achievementID], value) {
			return ErrProgressOverflow
		}
	}
	for achievementID, value := range source {
		target[achievementID] += value
	}
	delete(acc.chars, fromChar)
	return nil
}

// Views returns the client projection of every achievement of an account.
func (g *AchievementGuard) Views(accountID string) []AchievementView {
	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.accounts[accountID]
	views := make([]AchievementView, 0, len(g.achievementOrder))
	for _, achievementID := range g.achievementOrder {
		def := g.achievements[achievementID]
		view := AchievementView{
			ID:       def.ID,
			Name:     def.Name,
			Goal:     def.Goal,
			Unlocked: false,
		}
		if !def.Hidden {
			view.Condition = def.Condition
		}
		if acc != nil {
			view.Value = acc.totals[achievementID]
			view.Unlocked = acc.unlocked[achievementID]
		}
		views = append(views, view)
	}
	return views
}

// SyncRemote merges a progress value replicated from another server.
func (g *AchievementGuard) SyncRemote(accountID, achievementID string, value int64) error {
	if accountID == "" || achievementID == "" || value < 0 {
		return ErrInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	if _, exists := g.achievements[achievementID]; !exists {
		return ErrUnknownAchievement
	}
	acc := g.accountLocked(accountID)
	if value > acc.totals[achievementID] {
		delta := value - acc.totals[achievementID]
		acc.totals[achievementID] = value
		acc.remote[achievementID] += delta
	}
	return g.tryAutoUnlockLocked(acc, accountID, "", achievementID, time.Now().UTC())
}

// InWindow reports whether an instant falls inside the achievement window.
func (g *AchievementGuard) InWindow(achievementID string, at time.Time) (bool, error) {
	if achievementID == "" {
		return false, ErrInvalidInput
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	def, exists := g.achievements[achievementID]
	if !exists {
		return false, ErrUnknownAchievement
	}
	at = at.UTC()
	if !def.StartAt.IsZero() && at.Before(def.StartAt) {
		return false, nil
	}
	if !def.EndAt.IsZero() && !at.Before(def.EndAt) {
		return false, nil
	}
	return true, nil
}

// RegisterSequence stores the numeric steps of a sequence achievement.
func (g *AchievementGuard) RegisterSequence(id string, steps []int64) error {
	if id == "" {
		return ErrInvalidInput
	}
	sorted := append([]int64(nil), steps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.sequences[id] = sorted
	return nil
}

// SequenceSteps returns the steps in numeric order.
func (g *AchievementGuard) SequenceSteps(id string) []int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	steps := g.sequences[id]
	if len(steps) == 0 {
		return []int64{}
	}
	out := make([]int64, len(steps))
	copy(out, steps)
	return out
}

// TeamContribution credits a team kill to the member who made it.
func (g *AchievementGuard) TeamContribution(leaderAccount, memberAccount, achievementID string, delta int64) error {
	if leaderAccount == "" || memberAccount == "" || achievementID == "" || delta < 0 {
		return ErrInvalidInput
	}
	return g.AddProgressAt(memberAccount, memberAccount, achievementID, delta, time.Now().UTC())
}

// Reissue rebuilds every reward mail of an account ordered by unlock time.
func (g *AchievementGuard) Reissue(accountID string) ([]Mail, error) {
	if accountID == "" {
		return nil, ErrInvalidInput
	}

	g.mu.RLock()
	acc := g.accounts[accountID]
	if acc == nil || len(acc.unlockEvents) == 0 {
		g.mu.RUnlock()
		return []Mail{}, nil
	}
	events := make([]unlockEvent, len(acc.unlockEvents))
	copy(events, acc.unlockEvents)
	g.mu.RUnlock()

	mails := make([]Mail, 0, len(events))
	for _, event := range events {
		mails = append(mails, Mail{
			AccountID:     event.accountID,
			AchievementID: event.achievementID,
			At:            event.at,
		})
	}
	sort.SliceStable(mails, func(i, j int) bool {
		if !mails[i].At.Equal(mails[j].At) {
			return mails[i].At.Before(mails[j].At)
		}
		return mails[i].AchievementID < mails[j].AchievementID
	})
	return mails, nil
}

// SetMailFault makes mail delivery fail so unlock transactions can be tested.
func (g *AchievementGuard) SetMailFault(fail bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mailFault = fail
	return nil
}
