package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Signal is one activity observation reported by a battlefield detector.
type Signal struct {
	PlayerID      string
	RoundID       string
	DetectorID    string
	MovedDistance float64
	DamageDealt   int64
	HealingDone   int64
	OnVehicle     bool
	At            time.Time
}

// AfkGuard decides whether a player was idle during a battlefield round.
type AfkGuard struct {
}

// NewAfkGuard builds an empty guard with default penalties.
func NewAfkGuard() *AfkGuard {
	return &AfkGuard{}
}

// StartRound registers the battlefield type of a round.
func (g *AfkGuard) StartRound(roundID, bgType string) error {
	return errNotImplemented
}

// SetIdleThreshold configures how many idle observations mark a player AFK.
func (g *AfkGuard) SetIdleThreshold(bgType string, idleSignals int) error {
	return errNotImplemented
}

// Report records one activity observation, merging duplicate detectors.
func (g *AfkGuard) Report(s Signal) error {
	return errNotImplemented
}

// IdleCount returns the idle observations of a player in a round.
func (g *AfkGuard) IdleCount(playerID, roundID string) int {
	return 0
}

// Evaluate reports whether the player is considered AFK in the round.
func (g *AfkGuard) Evaluate(playerID, roundID string) (bool, error) {
	return false, errNotImplemented
}

// ApplyPenalty deducts score for an AFK round and returns the amount deducted.
func (g *AfkGuard) ApplyPenalty(playerID, roundID string) (int64, error) {
	return 0, errNotImplemented
}

// Score returns the current battleground score of a player.
func (g *AfkGuard) Score(playerID string) int64 {
	return 0
}

// SetStartScore seeds the score of a player before the round.
func (g *AfkGuard) SetStartScore(playerID string, score int64) error {
	return errNotImplemented
}

// FileAppeal freezes penalties for a player in a round.
func (g *AfkGuard) FileAppeal(playerID, roundID string) error {
	return errNotImplemented
}

// AuditTrail returns the audit lines recorded for a player.
func (g *AfkGuard) AuditTrail(playerID string) []string {
	return nil
}

// MergeRemote merges idle observations replicated from another server.
func (g *AfkGuard) MergeRemote(roundID, playerID, detectorID string, idle int, at time.Time) error {
	return errNotImplemented
}

// Snapshot serialises the guard state for a detector restart.
func (g *AfkGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (g *AfkGuard) Restore(data []byte) error {
	return errNotImplemented
}

// RegisterTeam records the members of a battlefield team.
func (g *AfkGuard) RegisterTeam(teamID string, players []string) error {
	return errNotImplemented
}

// PenalizeTeam applies the AFK penalty to every idle member of a team.
func (g *AfkGuard) PenalizeTeam(teamID, roundID string) ([]string, error) {
	return nil, errNotImplemented
}
