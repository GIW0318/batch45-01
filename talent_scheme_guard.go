package task

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	// ErrInvalidPoints covers negative, overflowing or over-budget point values.
	ErrInvalidPoints = errors.New("invalid talent points")
	// ErrSchemeNotFound is returned when a scheme id is unknown.
	ErrSchemeNotFound = errors.New("talent scheme not found")
	// ErrSchemeExists is returned when creating a duplicate scheme id.
	ErrSchemeExists = errors.New("talent scheme already exists")
	// ErrVersionConflict is returned when a save is based on a stale version.
	ErrVersionConflict = errors.New("talent scheme version conflict")
	// ErrSwitchCooldown is returned when a scheme switch is still cooling down.
	ErrSwitchCooldown = errors.New("talent scheme switch cooldown")
	// ErrInvalidArgument covers other invalid inputs such as empty ids.
	ErrInvalidArgument = errors.New("invalid argument")
)

const maxSchemeDiffs = 200

// Scheme is one saved talent build.
type Scheme struct {
	ID        string
	Name      string
	Version   int
	Points    map[string]int
	Skills    map[string]int
	UpdatedAt time.Time
}

// TalentDiff is one old/new value pair in a save.
type TalentDiff struct {
	TalentID string
	OldValue int
	NewValue int
}

// SaveDiff is the auditable difference produced by one successful save.
type SaveDiff struct {
	Version int
	At      time.Time
	Changes []TalentDiff
}

// TalentSchemeGuard keeps talent schemes exclusive, conserved and versioned.
type TalentSchemeGuard struct {
	mu sync.RWMutex

	totalPoints     int
	equipmentPoints int

	schemes   map[string]*Scheme
	committed map[string]map[string]int
	history   map[string][]SaveDiff

	activeScheme    string
	hasActivated    bool
	lastActivatedAt time.Time
	switchCooldown  time.Duration

	level    int
	maxLevel int

	books        map[string]int
	refundedBase map[string]int
}

// NewTalentSchemeGuard builds a guard with the default point budget.
func NewTalentSchemeGuard() *TalentSchemeGuard {
	return &TalentSchemeGuard{
		schemes:      make(map[string]*Scheme),
		committed:    make(map[string]map[string]int),
		history:      make(map[string][]SaveDiff),
		books:        make(map[string]int),
		refundedBase: make(map[string]int),
	}
}

// SetTotalPoints configures the base talent point budget.
func (g *TalentSchemeGuard) SetTotalPoints(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: total points must not be negative", ErrInvalidPoints)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	newCap := capFor(n, g.equipmentPoints)
	for _, scheme := range g.schemes {
		if spent := spentLocked(scheme); spent > newCap {
			return fmt.Errorf("%w: scheme %s has %d points allocated, cap would be %d", ErrInvalidPoints, scheme.ID, spent, newCap)
		}
	}
	g.totalPoints = n
	return nil
}

// TotalPoints returns the current talent point budget.
func (g *TalentSchemeGuard) TotalPoints() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.totalPoints
}

// CreateScheme creates an empty scheme.
func (g *TalentSchemeGuard) CreateScheme(id, name string) error {
	if id == "" {
		return fmt.Errorf("%w: scheme id is empty", ErrInvalidArgument)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.schemes[id]; ok {
		return fmt.Errorf("%w: %s", ErrSchemeExists, id)
	}
	g.schemes[id] = &Scheme{
		ID:     id,
		Name:   name,
		Points: make(map[string]int),
		Skills: make(map[string]int),
	}
	g.committed[id] = make(map[string]int)
	return nil
}

// Allocate adds or removes points of one talent and returns the new value.
func (g *TalentSchemeGuard) Allocate(schemeID, talentID string, delta int) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrSchemeNotFound, schemeID)
	}
	value, err := g.previewAllocationLocked(scheme, talentID, delta)
	if err != nil {
		return 0, err
	}

	if value == 0 {
		delete(scheme.Points, talentID)
	} else {
		scheme.Points[talentID] = value
	}
	return value, nil
}

// Points returns the points allocated to one talent of a scheme.
func (g *TalentSchemeGuard) Points(schemeID, talentID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return 0
	}
	return scheme.Points[talentID]
}

// SpentPoints returns the points spent by a scheme.
func (g *TalentSchemeGuard) SpentPoints(schemeID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return 0
	}
	return int(spentLocked(scheme))
}

// RemainingPoints returns the points still available to a scheme.
func (g *TalentSchemeGuard) RemainingPoints(schemeID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return 0
	}
	remaining := g.capLocked() - spentLocked(scheme)
	if remaining < 0 {
		return 0
	}
	return int(remaining)
}

// Activate makes a scheme the only active one and clears its skill cooldowns.
func (g *TalentSchemeGuard) Activate(schemeID string, at time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrSchemeNotFound, schemeID)
	}
	if err := g.validateSchemeLocked(scheme); err != nil {
		return err
	}

	if g.hasActivated && g.switchCooldown > 0 {
		elapsed := at.Sub(g.lastActivatedAt)
		if elapsed < g.switchCooldown {
			return fmt.Errorf("%w: %v remaining", ErrSwitchCooldown, g.switchCooldown-elapsed)
		}
	}

	g.activeScheme = schemeID
	g.hasActivated = true
	g.lastActivatedAt = at
	scheme.Skills = make(map[string]int)
	return nil
}

// ActiveSchemes lists the schemes currently in effect.
func (g *TalentSchemeGuard) ActiveSchemes() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.activeScheme == "" {
		return nil
	}
	return []string{g.activeScheme}
}

// SetSwitchCooldown configures the cooldown between two activations.
func (g *TalentSchemeGuard) SetSwitchCooldown(d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("%w: switch cooldown must not be negative", ErrInvalidArgument)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.switchCooldown = d
	return nil
}

// SwitchCooldownRemaining returns the remaining switch cooldown.
func (g *TalentSchemeGuard) SwitchCooldownRemaining(at time.Time) time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.hasActivated || g.switchCooldown <= 0 {
		return 0
	}
	remaining := g.switchCooldown - at.Sub(g.lastActivatedAt)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// SetSkillCooldown marks a skill of a scheme as cooling down.
func (g *TalentSchemeGuard) SetSkillCooldown(schemeID, skillID string, until time.Time) error {
	if skillID == "" {
		return fmt.Errorf("%w: skill id is empty", ErrInvalidArgument)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrSchemeNotFound, schemeID)
	}
	scheme.Skills[skillID] = int(until.UnixNano())
	return nil
}

// SkillCooldownRemaining returns the remaining cooldown of a scheme skill.
func (g *TalentSchemeGuard) SkillCooldownRemaining(schemeID, skillID string, at time.Time) time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return 0
	}
	until, ok := scheme.Skills[skillID]
	if !ok {
		return 0
	}
	remaining := time.Duration(int64(until) - at.UnixNano())
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// SetLevel records the current character level.
func (g *TalentSchemeGuard) SetLevel(level int) error {
	if level < 0 {
		return fmt.Errorf("%w: level must not be negative", ErrInvalidArgument)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.level = level
	if level > g.maxLevel {
		g.maxLevel = level
	}
	return nil
}

// RespecCost returns the respec price based on the historical max level.
func (g *TalentSchemeGuard) RespecCost() (int64, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.maxLevel <= 0 {
		return 0, nil
	}
	return int64(g.maxLevel) * 100, nil
}

// EquipTalentPoints adds talent points granted by equipped items.
func (g *TalentSchemeGuard) EquipTalentPoints(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: equipment points must not be negative", ErrInvalidPoints)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if n > maxInt()-g.equipmentPoints {
		return fmt.Errorf("%w: equipment point overflow", ErrInvalidPoints)
	}
	g.equipmentPoints += n
	return nil
}

// UnequipTalentPoints removes talent points granted by unequipped items.
func (g *TalentSchemeGuard) UnequipTalentPoints(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: equipment points must not be negative", ErrInvalidPoints)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if n > g.equipmentPoints {
		return fmt.Errorf("%w: cannot remove %d equipment points from %d", ErrInvalidPoints, n, g.equipmentPoints)
	}

	newEquipment := g.equipmentPoints - n
	newCap := capFor(g.totalPoints, newEquipment)
	for _, scheme := range g.schemes {
		if spent := spentLocked(scheme); spent > newCap {
			return fmt.Errorf("%w: scheme %s would exceed cap %d after unequip", ErrInvalidPoints, scheme.ID, newCap)
		}
	}
	g.equipmentPoints = newEquipment
	return nil
}

// EquipmentPoints returns the talent points currently granted by equipment.
func (g *TalentSchemeGuard) EquipmentPoints() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.equipmentPoints
}

// ImportLegacy imports a legacy talent map without zeroing the points.
func (g *TalentSchemeGuard) ImportLegacy(schemeID string, points map[string]int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrSchemeNotFound, schemeID)
	}

	merged := clonePoints(scheme.Points)
	for talentID, value := range points {
		if talentID == "" {
			return fmt.Errorf("%w: talent id is empty", ErrInvalidPoints)
		}
		if value < 0 {
			return fmt.Errorf("%w: talent %s has negative points", ErrInvalidPoints, talentID)
		}
		merged[talentID] = value
	}
	if err := g.validatePointsLocked(merged); err != nil {
		return err
	}
	scheme.Points = merged
	return nil
}

// Save persists a scheme if the caller edited the expected version.
func (g *TalentSchemeGuard) Save(schemeID string, expectedVersion int, at time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrSchemeNotFound, schemeID)
	}
	if scheme.Version != expectedVersion {
		return fmt.Errorf("%w: expected %d, current %d", ErrVersionConflict, expectedVersion, scheme.Version)
	}
	if err := g.validateSchemeLocked(scheme); err != nil {
		return err
	}

	previous := g.committed[schemeID]
	if previous == nil {
		previous = make(map[string]int)
	}
	changes := diffPoints(previous, scheme.Points)

	scheme.Version++
	scheme.UpdatedAt = at
	g.committed[schemeID] = clonePoints(scheme.Points)
	g.appendHistoryLocked(schemeID, SaveDiff{
		Version: scheme.Version,
		At:      at,
		Changes: changes,
	})
	return nil
}

// History returns a snapshot of the save differences kept for a scheme.
func (g *TalentSchemeGuard) History(schemeID string) []SaveDiff {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stored := g.history[schemeID]
	out := make([]SaveDiff, len(stored))
	for i, record := range stored {
		out[i] = SaveDiff{
			Version: record.Version,
			At:      record.At,
			Changes: append([]TalentDiff(nil), record.Changes...),
		}
	}
	return out
}

// HistoryAt returns save differences recorded at or before the requested time.
func (g *TalentSchemeGuard) HistoryAt(schemeID string, at time.Time) []SaveDiff {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]SaveDiff, 0)
	for _, record := range g.history[schemeID] {
		if record.At.After(at) {
			continue
		}
		out = append(out, SaveDiff{
			Version: record.Version,
			At:      record.At,
			Changes: append([]TalentDiff(nil), record.Changes...),
		})
	}
	return out
}

// Preview computes the value a talent would have without saving it.
func (g *TalentSchemeGuard) Preview(schemeID, talentID string, delta int) (int, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrSchemeNotFound, schemeID)
	}
	return g.previewAllocationLocked(scheme, talentID, delta)
}

// Reset refunds the talent books of an account and returns how many were refunded.
func (g *TalentSchemeGuard) Reset(accountID string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Base points are treated as book-backed. Refund only newly granted base
	// points per account so repeated resets cannot mint unlimited books.
	refund := g.totalPoints - g.refundedBase[accountID]
	if refund < 0 {
		refund = 0
	}
	g.refundedBase[accountID] += refund
	g.books[accountID] += refund

	now := time.Now()
	for id, scheme := range g.schemes {
		changes := diffPoints(scheme.Points, nil)
		scheme.Points = make(map[string]int)
		scheme.Skills = make(map[string]int)
		scheme.Version++
		scheme.UpdatedAt = now
		g.committed[id] = make(map[string]int)
		g.appendHistoryLocked(id, SaveDiff{
			Version: scheme.Version,
			At:      now,
			Changes: changes,
		})
	}

	g.activeScheme = ""
	g.hasActivated = false
	g.lastActivatedAt = time.Time{}
	return refund, nil
}

// TalentBooks returns the talent books held by an account.
func (g *TalentSchemeGuard) TalentBooks(accountID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.books[accountID]
}

// SeasonCompensate compensates every player, online or not.
func (g *TalentSchemeGuard) SeasonCompensate(online, all []string) ([]string, error) {
	seen := make(map[string]bool, len(all)+len(online))
	out := make([]string, 0, len(all)+len(online))

	for _, player := range all {
		if seen[player] {
			continue
		}
		seen[player] = true
		out = append(out, player)
	}
	for _, player := range online {
		if seen[player] {
			continue
		}
		seen[player] = true
		out = append(out, player)
	}
	return out, nil
}

// Validate checks that a scheme respects the talent point budget.
func (g *TalentSchemeGuard) Validate(schemeID string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	scheme, ok := g.schemes[schemeID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrSchemeNotFound, schemeID)
	}
	return g.validateSchemeLocked(scheme)
}

func (g *TalentSchemeGuard) capLocked() int64 {
	return capFor(g.totalPoints, g.equipmentPoints)
}

func (g *TalentSchemeGuard) validateSchemeLocked(scheme *Scheme) error {
	if scheme.Version < 0 {
		return fmt.Errorf("%w: scheme %s has a negative version", ErrInvalidPoints, scheme.ID)
	}
	return g.validatePointsLocked(scheme.Points)
}

func (g *TalentSchemeGuard) validatePointsLocked(points map[string]int) error {
	var spent int64
	for talentID, value := range points {
		if talentID == "" {
			return fmt.Errorf("%w: talent id is empty", ErrInvalidPoints)
		}
		if value < 0 {
			return fmt.Errorf("%w: talent %s has negative points", ErrInvalidPoints, talentID)
		}
		spent += int64(value)
		if spent > g.capLocked() {
			return fmt.Errorf("%w: allocated %d exceeds cap %d", ErrInvalidPoints, spent, g.capLocked())
		}
	}
	return nil
}

func (g *TalentSchemeGuard) previewAllocationLocked(scheme *Scheme, talentID string, delta int) (int, error) {
	if talentID == "" {
		return 0, fmt.Errorf("%w: talent id is empty", ErrInvalidPoints)
	}

	current := scheme.Points[talentID]
	if current < 0 {
		return 0, fmt.Errorf("%w: talent %s has negative points", ErrInvalidPoints, talentID)
	}

	value := current + delta
	if (delta > 0 && value < current) || (delta < 0 && value > current) {
		return 0, fmt.Errorf("%w: point value overflow", ErrInvalidPoints)
	}
	if value < 0 {
		return 0, fmt.Errorf("%w: talent %s cannot go below zero", ErrInvalidPoints, talentID)
	}

	var spent int64
	for id, points := range scheme.Points {
		if id == talentID {
			continue
		}
		if points < 0 {
			return 0, fmt.Errorf("%w: talent %s has negative points", ErrInvalidPoints, id)
		}
		spent += int64(points)
	}
	spent += int64(value)
	if spent > g.capLocked() {
		return 0, fmt.Errorf("%w: allocated %d exceeds cap %d", ErrInvalidPoints, spent, g.capLocked())
	}
	return value, nil
}

func (g *TalentSchemeGuard) appendHistoryLocked(schemeID string, record SaveDiff) {
	history := append(g.history[schemeID], record)
	if len(history) > maxSchemeDiffs {
		history = append([]SaveDiff(nil), history[len(history)-maxSchemeDiffs:]...)
	}
	g.history[schemeID] = history
}

func spentLocked(scheme *Scheme) int64 {
	var spent int64
	for _, value := range scheme.Points {
		if value > 0 {
			spent += int64(value)
		}
	}
	return spent
}

func clonePoints(points map[string]int) map[string]int {
	clone := make(map[string]int, len(points))
	for talentID, value := range points {
		clone[talentID] = value
	}
	return clone
}

func diffPoints(previous, current map[string]int) []TalentDiff {
	ids := make(map[string]struct{}, len(previous)+len(current))
	for talentID := range previous {
		ids[talentID] = struct{}{}
	}
	for talentID := range current {
		ids[talentID] = struct{}{}
	}

	ordered := make([]string, 0, len(ids))
	for talentID := range ids {
		ordered = append(ordered, talentID)
	}
	sort.Strings(ordered)

	changes := make([]TalentDiff, 0, len(ordered))
	for _, talentID := range ordered {
		oldValue := previous[talentID]
		newValue := current[talentID]
		if oldValue == newValue {
			continue
		}
		changes = append(changes, TalentDiff{
			TalentID: talentID,
			OldValue: oldValue,
			NewValue: newValue,
		})
	}
	return changes
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func capFor(totalPoints, equipmentPoints int) int64 {
	if equipmentPoints > maxInt()-totalPoints {
		return int64(maxInt())
	}
	return int64(totalPoints) + int64(equipmentPoints)
}
