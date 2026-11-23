package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Scheme is one saved talent build.
type Scheme struct {
	ID        string
	Name      string
	Version   int
	Points    map[string]int
	Skills    map[string]int
	UpdatedAt time.Time
}

// TalentSchemeGuard keeps talent schemes exclusive, conserved and versioned.
type TalentSchemeGuard struct {
}

// NewTalentSchemeGuard builds a guard with the default point budget.
func NewTalentSchemeGuard() *TalentSchemeGuard {
	return &TalentSchemeGuard{}
}

// SetTotalPoints configures the base talent point budget.
func (g *TalentSchemeGuard) SetTotalPoints(n int) error {
	return errNotImplemented
}

// TotalPoints returns the current talent point budget.
func (g *TalentSchemeGuard) TotalPoints() int {
	return 0
}

// CreateScheme creates an empty scheme.
func (g *TalentSchemeGuard) CreateScheme(id, name string) error {
	return errNotImplemented
}

// Allocate adds or removes points of one talent and returns the new value.
func (g *TalentSchemeGuard) Allocate(schemeID, talentID string, delta int) (int, error) {
	return 0, errNotImplemented
}

// Points returns the points allocated to one talent of a scheme.
func (g *TalentSchemeGuard) Points(schemeID, talentID string) int {
	return 0
}

// SpentPoints returns the points spent by a scheme.
func (g *TalentSchemeGuard) SpentPoints(schemeID string) int {
	return 0
}

// RemainingPoints returns the points still available to a scheme.
func (g *TalentSchemeGuard) RemainingPoints(schemeID string) int {
	return 0
}

// Activate makes a scheme the only active one and clears its skill cooldowns.
func (g *TalentSchemeGuard) Activate(schemeID string, at time.Time) error {
	return errNotImplemented
}

// ActiveSchemes lists the schemes currently in effect.
func (g *TalentSchemeGuard) ActiveSchemes() []string {
	return nil
}

// SetSwitchCooldown configures the cooldown between two activations.
func (g *TalentSchemeGuard) SetSwitchCooldown(d time.Duration) error {
	return errNotImplemented
}

// SwitchCooldownRemaining returns the remaining switch cooldown.
func (g *TalentSchemeGuard) SwitchCooldownRemaining(at time.Time) time.Duration {
	return 0
}

// SetSkillCooldown marks a skill of a scheme as cooling down.
func (g *TalentSchemeGuard) SetSkillCooldown(schemeID, skillID string, until time.Time) error {
	return errNotImplemented
}

// SkillCooldownRemaining returns the remaining cooldown of a scheme skill.
func (g *TalentSchemeGuard) SkillCooldownRemaining(schemeID, skillID string, at time.Time) time.Duration {
	return 0
}

// SetLevel records the current character level.
func (g *TalentSchemeGuard) SetLevel(level int) error {
	return errNotImplemented
}

// RespecCost returns the respec price based on the historical max level.
func (g *TalentSchemeGuard) RespecCost() (int64, error) {
	return 0, errNotImplemented
}

// EquipTalentPoints adds talent points granted by equipped items.
func (g *TalentSchemeGuard) EquipTalentPoints(n int) error {
	return errNotImplemented
}

// UnequipTalentPoints removes talent points granted by unequipped items.
func (g *TalentSchemeGuard) UnequipTalentPoints(n int) error {
	return errNotImplemented
}

// EquipmentPoints returns the talent points currently granted by equipment.
func (g *TalentSchemeGuard) EquipmentPoints() int {
	return 0
}

// ImportLegacy imports a legacy talent map without zeroing the points.
func (g *TalentSchemeGuard) ImportLegacy(schemeID string, points map[string]int) error {
	return errNotImplemented
}

// Save persists a scheme if the caller edited the expected version.
func (g *TalentSchemeGuard) Save(schemeID string, expectedVersion int, at time.Time) error {
	return errNotImplemented
}

// Preview computes the value a talent would have without saving it.
func (g *TalentSchemeGuard) Preview(schemeID, talentID string, delta int) (int, error) {
	return 0, errNotImplemented
}

// Reset refunds the talent books of an account and returns how many were refunded.
func (g *TalentSchemeGuard) Reset(accountID string) (int, error) {
	return 0, errNotImplemented
}

// TalentBooks returns the talent books held by an account.
func (g *TalentSchemeGuard) TalentBooks(accountID string) int {
	return 0
}

// SeasonCompensate compensates every player, online or not.
func (g *TalentSchemeGuard) SeasonCompensate(online, all []string) ([]string, error) {
	return nil, errNotImplemented
}

// Validate checks that a scheme respects the talent point budget.
func (g *TalentSchemeGuard) Validate(schemeID string) error {
	return errNotImplemented
}
