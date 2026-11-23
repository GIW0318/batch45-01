package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxDedupePerPlayerRound = 4096
	maxAuditLinesPerPlayer  = 512
	defaultPenaltyAmount    = int64(10)
	snapshotVersion         = 1
)

var (
	// ErrInvalidRound is returned when a round identifier or type is invalid.
	ErrInvalidRound = errors.New("invalid round")
	// ErrRoundNotFound is returned when an operation refers to an unknown round.
	ErrRoundNotFound = errors.New("round not found")
	// ErrInvalidPlayer is returned when a player identifier is empty.
	ErrInvalidPlayer = errors.New("invalid player")
	// ErrInvalidThreshold is returned when an idle threshold is not positive.
	ErrInvalidThreshold = errors.New("invalid idle threshold")
	// ErrAppealOpen is returned when a penalty is attempted during an appeal.
	ErrAppealOpen = errors.New("penalty frozen by an open appeal")
	// ErrInvalidSnapshot is returned when snapshot data cannot be decoded.
	ErrInvalidSnapshot = errors.New("invalid afk guard snapshot")
)

var defaultIdleThresholds = map[string]int{
	"arena":         3,
	"battleground":  5,
	"battle_ground": 5,
	"bg":            5,
	"default":       3,
}

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

type dedupeEntry struct {
	At     int64  `json:"at"`
	Idle   int    `json:"idle"`
	Active bool   `json:"active"`
	Seq    uint64 `json:"seq"`
}

type playerRoundState struct {
	IdleCount     int                    `json:"idle_count"`
	Dedupe        map[int64]*dedupeEntry `json:"dedupe"`
	Order         []int64                `json:"-"`
	Penalized     bool                   `json:"penalized"`
	PenaltyAmount int64                  `json:"penalty_amount"`
}

type roundState struct {
	BgType  string                       `json:"bg_type"`
	Players map[string]*playerRoundState `json:"players"`
}

type playerRoundKey struct {
	PlayerID string `json:"player_id"`
	RoundID  string `json:"round_id"`
}

// AfkGuard decides whether a player was idle during a battlefield round.
//
// All state is guarded by a mutex so reports from several detectors may be
// delivered concurrently. Counters and the deduplication index are scoped to a
// player and a round, which keeps a new battlefield from inheriting old data.
type AfkGuard struct {
	mu sync.RWMutex

	rounds     map[string]*roundState
	thresholds map[string]int
	scores     map[string]int64
	appeals    map[playerRoundKey]bool
	teams      map[string][]string
	audit      map[string][]string
	nextSeq    uint64
}

// NewAfkGuard builds an empty guard with default penalties.
func NewAfkGuard() *AfkGuard {
	g := &AfkGuard{}
	g.initLocked()
	return g
}

func (g *AfkGuard) initLocked() {
	if g.rounds == nil {
		g.rounds = make(map[string]*roundState)
	}
	if g.thresholds == nil {
		g.thresholds = make(map[string]int, len(defaultIdleThresholds))
		for bgType, threshold := range defaultIdleThresholds {
			g.thresholds[bgType] = threshold
		}
	}
	if g.scores == nil {
		g.scores = make(map[string]int64)
	}
	if g.appeals == nil {
		g.appeals = make(map[playerRoundKey]bool)
	}
	if g.teams == nil {
		g.teams = make(map[string][]string)
	}
	if g.audit == nil {
		g.audit = make(map[string][]string)
	}
}

func normalizeBgType(bgType string) string {
	return strings.ToLower(strings.TrimSpace(bgType))
}

func (g *AfkGuard) thresholdLocked(bgType string) int {
	bgType = normalizeBgType(bgType)
	if threshold, ok := g.thresholds[bgType]; ok && threshold > 0 {
		return threshold
	}
	if threshold, ok := defaultIdleThresholds[bgType]; ok && threshold > 0 {
		return threshold
	}
	if threshold, ok := g.thresholds["default"]; ok && threshold > 0 {
		return threshold
	}
	return defaultIdleThresholds["default"]
}

func (g *AfkGuard) roundLocked(roundID string) (*roundState, error) {
	if strings.TrimSpace(roundID) == "" {
		return nil, ErrInvalidRound
	}
	round, ok := g.rounds[roundID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
	}
	return round, nil
}

func (g *AfkGuard) playerStateLocked(round *roundState, playerID string) *playerRoundState {
	state, ok := round.Players[playerID]
	if !ok || state == nil {
		state = &playerRoundState{Dedupe: make(map[int64]*dedupeEntry)}
		round.Players[playerID] = state
	}
	if state.Dedupe == nil {
		state.Dedupe = make(map[int64]*dedupeEntry)
	}
	return state
}

func (g *AfkGuard) trimDedupeLocked(state *playerRoundState) {
	for len(state.Order) > maxDedupePerPlayerRound {
		oldest := state.Order[0]
		state.Order = state.Order[1:]
		delete(state.Dedupe, oldest)
	}
}

func (g *AfkGuard) addAuditLocked(playerID, line string) {
	lines := append(g.audit[playerID], line)
	if len(lines) > maxAuditLinesPerPlayer {
		trimmed := make([]string, maxAuditLinesPerPlayer)
		copy(trimmed, lines[len(lines)-maxAuditLinesPerPlayer:])
		lines = trimmed
	}
	g.audit[playerID] = lines
}

func signalIsActive(s Signal) bool {
	return s.MovedDistance > 0 || s.DamageDealt > 0 || s.HealingDone > 0 || s.OnVehicle
}

// StartRound registers the battlefield type of a round.
func (g *AfkGuard) StartRound(roundID, bgType string) error {
	if strings.TrimSpace(roundID) == "" || strings.TrimSpace(bgType) == "" {
		return ErrInvalidRound
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	bgType = normalizeBgType(bgType)
	if existing, ok := g.rounds[roundID]; ok {
		if existing.BgType == bgType {
			// Registering the same round again is idempotent. This also makes
			// restoring a snapshot followed by a start notification safe.
			return nil
		}
		// Reusing a round ID for another battlefield is a new battle; do not
		// let counters, dedupe keys, or appeals leak into it.
		g.rounds[roundID] = &roundState{
			BgType:  bgType,
			Players: make(map[string]*playerRoundState),
		}
		for key := range g.appeals {
			if key.RoundID == roundID {
				delete(g.appeals, key)
			}
		}
		return nil
	}

	g.rounds[roundID] = &roundState{
		BgType:  bgType,
		Players: make(map[string]*playerRoundState),
	}
	return nil
}

// SetIdleThreshold configures how many idle observations mark a player AFK.
func (g *AfkGuard) SetIdleThreshold(bgType string, idleSignals int) error {
	if strings.TrimSpace(bgType) == "" || idleSignals <= 0 {
		return ErrInvalidThreshold
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.thresholds[normalizeBgType(bgType)] = idleSignals
	return nil
}

// Report records one activity observation, merging duplicate detectors.
func (g *AfkGuard) Report(s Signal) error {
	if strings.TrimSpace(s.PlayerID) == "" {
		return ErrInvalidPlayer
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	round, err := g.roundLocked(s.RoundID)
	if err != nil {
		return err
	}
	state := g.playerStateLocked(round, s.PlayerID)
	at := s.At.UnixNano()
	active := signalIsActive(s)

	if entry, seen := state.Dedupe[at]; seen {
		// An activity observation at the same instant upgrades an idle one.
		// A duplicate idle observation (possibly from another detector) is
		// intentionally a no-op.
		if active && !entry.Active {
			state.IdleCount -= entry.Idle
			if state.IdleCount < 0 {
				state.IdleCount = 0
			}
			entry.Idle = 0
			entry.Active = true
			g.addAuditLocked(s.PlayerID, fmt.Sprintf(
				"activity player=%s round=%s at=%d moved=%.3f damage=%d healing=%d vehicle=%t",
				s.PlayerID, s.RoundID, at, s.MovedDistance, s.DamageDealt, s.HealingDone, s.OnVehicle))
		}
		return nil
	}

	entry := &dedupeEntry{At: at, Seq: g.nextSeq}
	g.nextSeq++
	if active {
		entry.Active = true
		state.IdleCount = 0
		g.addAuditLocked(s.PlayerID, fmt.Sprintf(
			"activity player=%s round=%s at=%d moved=%.3f damage=%d healing=%d vehicle=%t",
			s.PlayerID, s.RoundID, at, s.MovedDistance, s.DamageDealt, s.HealingDone, s.OnVehicle))
	} else {
		entry.Idle = 1
		state.IdleCount++
		threshold := g.thresholdLocked(round.BgType)
		if state.IdleCount == threshold {
			g.addAuditLocked(s.PlayerID, fmt.Sprintf(
				"afk player=%s round=%s idle=%d threshold=%d",
				s.PlayerID, s.RoundID, state.IdleCount, threshold))
		}
	}

	state.Dedupe[at] = entry
	state.Order = append(state.Order, at)
	g.trimDedupeLocked(state)
	return nil
}

// IdleCount returns the idle observations of a player in a round.
func (g *AfkGuard) IdleCount(playerID, roundID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	round, ok := g.rounds[roundID]
	if !ok || round == nil {
		return 0
	}
	state, ok := round.Players[playerID]
	if !ok || state == nil {
		return 0
	}
	return state.IdleCount
}

// Evaluate reports whether the player is considered AFK in the round.
func (g *AfkGuard) Evaluate(playerID, roundID string) (bool, error) {
	if strings.TrimSpace(playerID) == "" {
		return false, ErrInvalidPlayer
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	round, err := g.roundLocked(roundID)
	if err != nil {
		return false, err
	}

	idle := 0
	if state, ok := round.Players[playerID]; ok && state != nil {
		idle = state.IdleCount
	}
	threshold := g.thresholdLocked(round.BgType)
	afk := idle >= threshold
	g.addAuditLocked(playerID, fmt.Sprintf(
		"evaluate player=%s round=%s bg=%s afk=%t idle=%d threshold=%d",
		playerID, roundID, round.BgType, afk, idle, threshold))
	return afk, nil
}

func (g *AfkGuard) applyPenaltyLocked(playerID, roundID string) (int64, error) {
	if strings.TrimSpace(playerID) == "" {
		return 0, ErrInvalidPlayer
	}

	round, err := g.roundLocked(roundID)
	if err != nil {
		return 0, err
	}
	state, ok := round.Players[playerID]
	if ok && state != nil && state.Penalized {
		// A round penalty is idempotent even if an appeal is opened later.
		return 0, nil
	}
	key := playerRoundKey{PlayerID: playerID, RoundID: roundID}
	if g.appeals[key] {
		return 0, ErrAppealOpen
	}

	if !ok || state == nil {
		return 0, nil
	}
	if state.IdleCount < g.thresholdLocked(round.BgType) {
		return 0, nil
	}

	state.Penalized = true
	state.PenaltyAmount = defaultPenaltyAmount
	g.scores[playerID] -= defaultPenaltyAmount
	g.addAuditLocked(playerID, fmt.Sprintf(
		"penalty player=%s round=%s amount=%d score=%d",
		playerID, roundID, defaultPenaltyAmount, g.scores[playerID]))
	return defaultPenaltyAmount, nil
}

// ApplyPenalty deducts score for an AFK round and returns the amount deducted.
func (g *AfkGuard) ApplyPenalty(playerID, roundID string) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.applyPenaltyLocked(playerID, roundID)
}

// Score returns the current battleground score of a player.
func (g *AfkGuard) Score(playerID string) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.scores[playerID]
}

// SetStartScore seeds the score of a player before the round.
func (g *AfkGuard) SetStartScore(playerID string, score int64) error {
	if strings.TrimSpace(playerID) == "" {
		return ErrInvalidPlayer
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.scores[playerID] = score
	return nil
}

// FileAppeal freezes penalties for a player in a round.
func (g *AfkGuard) FileAppeal(playerID, roundID string) error {
	if strings.TrimSpace(playerID) == "" {
		return ErrInvalidPlayer
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	if _, err := g.roundLocked(roundID); err != nil {
		return err
	}
	key := playerRoundKey{PlayerID: playerID, RoundID: roundID}
	if g.appeals[key] {
		return nil
	}
	g.appeals[key] = true
	g.addAuditLocked(playerID, fmt.Sprintf("appeal player=%s round=%s", playerID, roundID))
	return nil
}

// AuditTrail returns the audit lines recorded for a player.
func (g *AfkGuard) AuditTrail(playerID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	lines := g.audit[playerID]
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}

// MergeRemote merges idle observations replicated from another server.
func (g *AfkGuard) MergeRemote(roundID, playerID, detectorID string, idle int, at time.Time) error {
	if strings.TrimSpace(playerID) == "" {
		return ErrInvalidPlayer
	}
	if idle < 0 {
		return fmt.Errorf("invalid remote idle count: %d", idle)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	round, err := g.roundLocked(roundID)
	if err != nil {
		return err
	}

	state := g.playerStateLocked(round, playerID)
	if idle == 0 {
		return nil
	}

	instant := at.UnixNano()
	if entry, seen := state.Dedupe[instant]; seen {
		if entry.Active {
			return nil
		}
		// A replayed remote batch must not be counted twice. If a later
		// replication contains more observations for the same instant, merge
		// only the additional amount.
		if idle > entry.Idle {
			state.IdleCount += idle - entry.Idle
			entry.Idle = idle
		}
		return nil
	}

	entry := &dedupeEntry{At: instant, Idle: idle, Seq: g.nextSeq}
	g.nextSeq++
	state.Dedupe[instant] = entry
	state.Order = append(state.Order, instant)
	state.IdleCount += idle
	g.trimDedupeLocked(state)
	g.addAuditLocked(playerID, fmt.Sprintf(
		"remote player=%s round=%s detector=%s idle=%d at=%d",
		playerID, roundID, detectorID, idle, instant))
	return nil
}

type snapshotDedupe struct {
	At     int64  `json:"at"`
	Idle   int    `json:"idle"`
	Active bool   `json:"active"`
	Seq    uint64 `json:"seq"`
}

type snapshotPlayer struct {
	IdleCount     int              `json:"idle_count"`
	Dedupe        []snapshotDedupe `json:"dedupe"`
	Penalized     bool             `json:"penalized"`
	PenaltyAmount int64            `json:"penalty_amount"`
}

type snapshotRound struct {
	BgType  string                    `json:"bg_type"`
	Players map[string]snapshotPlayer `json:"players"`
}

type snapshotGuard struct {
	Version    int                      `json:"version"`
	Rounds     map[string]snapshotRound `json:"rounds"`
	Thresholds map[string]int           `json:"thresholds"`
	Scores     map[string]int64         `json:"scores"`
	Appeals    []playerRoundKey         `json:"appeals"`
	Teams      map[string][]string      `json:"teams"`
	Audit      map[string][]string      `json:"audit"`
	NextSeq    uint64                   `json:"next_seq"`
}

// Snapshot serialises the guard state for a detector restart.
func (g *AfkGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	snap := snapshotGuard{
		Version:    snapshotVersion,
		Rounds:     make(map[string]snapshotRound, len(g.rounds)),
		Thresholds: make(map[string]int, len(g.thresholds)),
		Scores:     make(map[string]int64, len(g.scores)),
		Teams:      make(map[string][]string, len(g.teams)),
		Audit:      make(map[string][]string, len(g.audit)),
		NextSeq:    g.nextSeq,
	}

	for roundID, round := range g.rounds {
		if round == nil {
			continue
		}
		sr := snapshotRound{
			BgType:  round.BgType,
			Players: make(map[string]snapshotPlayer, len(round.Players)),
		}
		for playerID, state := range round.Players {
			if state == nil {
				continue
			}
			dedupe := make([]snapshotDedupe, 0, len(state.Dedupe))
			for _, entry := range state.Dedupe {
				if entry == nil {
					continue
				}
				dedupe = append(dedupe, snapshotDedupe{
					At:     entry.At,
					Idle:   entry.Idle,
					Active: entry.Active,
					Seq:    entry.Seq,
				})
			}
			sort.Slice(dedupe, func(i, j int) bool { return dedupe[i].Seq < dedupe[j].Seq })
			sr.Players[playerID] = snapshotPlayer{
				IdleCount:     state.IdleCount,
				Dedupe:        dedupe,
				Penalized:     state.Penalized,
				PenaltyAmount: state.PenaltyAmount,
			}
		}
		snap.Rounds[roundID] = sr
	}

	for bgType, threshold := range g.thresholds {
		snap.Thresholds[bgType] = threshold
	}
	for playerID, score := range g.scores {
		snap.Scores[playerID] = score
	}
	for key, open := range g.appeals {
		if open {
			snap.Appeals = append(snap.Appeals, key)
		}
	}
	sort.Slice(snap.Appeals, func(i, j int) bool {
		if snap.Appeals[i].RoundID != snap.Appeals[j].RoundID {
			return snap.Appeals[i].RoundID < snap.Appeals[j].RoundID
		}
		return snap.Appeals[i].PlayerID < snap.Appeals[j].PlayerID
	})
	for teamID, players := range g.teams {
		members := make([]string, len(players))
		copy(members, players)
		snap.Teams[teamID] = members
	}
	for playerID, lines := range g.audit {
		copied := make([]string, len(lines))
		copy(copied, lines)
		snap.Audit[playerID] = copied
	}

	return json.Marshal(snap)
}

// Restore loads a snapshot produced by Snapshot.
func (g *AfkGuard) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrInvalidSnapshot
	}

	var snap snapshotGuard
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if snap.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidSnapshot, snap.Version)
	}

	rounds := make(map[string]*roundState, len(snap.Rounds))
	nextSeq := snap.NextSeq
	for roundID, sr := range snap.Rounds {
		if strings.TrimSpace(roundID) == "" || strings.TrimSpace(sr.BgType) == "" {
			return fmt.Errorf("%w: invalid round record", ErrInvalidSnapshot)
		}
		round := &roundState{
			BgType:  normalizeBgType(sr.BgType),
			Players: make(map[string]*playerRoundState, len(sr.Players)),
		}
		for playerID, sp := range sr.Players {
			if strings.TrimSpace(playerID) == "" {
				return fmt.Errorf("%w: invalid player record", ErrInvalidSnapshot)
			}
			if sp.IdleCount < 0 {
				return fmt.Errorf("%w: negative idle count", ErrInvalidSnapshot)
			}
			state := &playerRoundState{
				IdleCount:     sp.IdleCount,
				Dedupe:        make(map[int64]*dedupeEntry, len(sp.Dedupe)),
				Penalized:     sp.Penalized,
				PenaltyAmount: sp.PenaltyAmount,
			}
			for _, d := range sp.Dedupe {
				if d.Idle < 0 {
					return fmt.Errorf("%w: negative dedupe count", ErrInvalidSnapshot)
				}
				entry := &dedupeEntry{
					At:     d.At,
					Idle:   d.Idle,
					Active: d.Active,
					Seq:    d.Seq,
				}
				state.Dedupe[d.At] = entry
				if d.Seq >= nextSeq {
					nextSeq = d.Seq + 1
				}
			}
			order := make([]int64, 0, len(state.Dedupe))
			for at := range state.Dedupe {
				order = append(order, at)
			}
			sort.Slice(order, func(i, j int) bool {
				left, right := state.Dedupe[order[i]], state.Dedupe[order[j]]
				if left.Seq != right.Seq {
					return left.Seq < right.Seq
				}
				return left.At < right.At
			})
			if len(order) > maxDedupePerPlayerRound {
				keep := make(map[int64]bool, maxDedupePerPlayerRound)
				order = order[len(order)-maxDedupePerPlayerRound:]
				for _, at := range order {
					keep[at] = true
				}
				for at := range state.Dedupe {
					if !keep[at] {
						delete(state.Dedupe, at)
					}
				}
			}
			state.Order = order
			round.Players[playerID] = state
		}
		rounds[roundID] = round
	}

	thresholds := make(map[string]int, len(snap.Thresholds))
	for bgType, threshold := range snap.Thresholds {
		if threshold > 0 {
			thresholds[normalizeBgType(bgType)] = threshold
		}
	}
	if len(thresholds) == 0 {
		for bgType, threshold := range defaultIdleThresholds {
			thresholds[bgType] = threshold
		}
	}

	scores := make(map[string]int64, len(snap.Scores))
	for playerID, score := range snap.Scores {
		if strings.TrimSpace(playerID) != "" {
			scores[playerID] = score
		}
	}

	appeals := make(map[playerRoundKey]bool, len(snap.Appeals))
	for _, key := range snap.Appeals {
		if strings.TrimSpace(key.PlayerID) != "" && strings.TrimSpace(key.RoundID) != "" {
			appeals[key] = true
		}
	}

	teams := make(map[string][]string, len(snap.Teams))
	for teamID, players := range snap.Teams {
		if strings.TrimSpace(teamID) == "" {
			continue
		}
		members := make([]string, 0, len(players))
		for _, playerID := range players {
			if strings.TrimSpace(playerID) != "" {
				members = append(members, playerID)
			}
		}
		teams[teamID] = members
	}

	audit := make(map[string][]string, len(snap.Audit))
	for playerID, lines := range snap.Audit {
		if strings.TrimSpace(playerID) == "" {
			continue
		}
		copied := make([]string, len(lines))
		copy(copied, lines)
		if len(copied) > maxAuditLinesPerPlayer {
			copied = copied[len(copied)-maxAuditLinesPerPlayer:]
		}
		audit[playerID] = copied
	}

	g.mu.Lock()
	g.rounds = rounds
	g.thresholds = thresholds
	g.scores = scores
	g.appeals = appeals
	g.teams = teams
	g.audit = audit
	g.nextSeq = nextSeq
	g.mu.Unlock()
	return nil
}

// RegisterTeam records the members of a battlefield team.
func (g *AfkGuard) RegisterTeam(teamID string, players []string) error {
	if strings.TrimSpace(teamID) == "" {
		return errors.New("invalid team")
	}

	members := make([]string, 0, len(players))
	seen := make(map[string]bool, len(players))
	for _, playerID := range players {
		if strings.TrimSpace(playerID) == "" {
			return ErrInvalidPlayer
		}
		if seen[playerID] {
			continue
		}
		seen[playerID] = true
		members = append(members, playerID)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.teams[teamID] = members
	return nil
}

// PenalizeTeam applies the AFK penalty to every idle member of a team.
func (g *AfkGuard) PenalizeTeam(teamID, roundID string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	if _, err := g.roundLocked(roundID); err != nil {
		return nil, err
	}
	members, ok := g.teams[teamID]
	if !ok {
		return nil, fmt.Errorf("team not found: %q", teamID)
	}

	penalized := make([]string, 0, len(members))
	for _, playerID := range members {
		amount, err := g.applyPenaltyLocked(playerID, roundID)
		if err != nil {
			// An appeal only freezes that member's penalty; other team members
			// still receive their own deductions.
			if errors.Is(err, ErrAppealOpen) {
				continue
			}
			return nil, err
		}
		if amount > 0 {
			penalized = append(penalized, playerID)
		}
	}
	return penalized, nil
}

// DestroyRound removes all counters, dedupe keys, and appeals for a finished
// battlefield. It is safe to call when the round is already absent.
func (g *AfkGuard) DestroyRound(roundID string) error {
	if strings.TrimSpace(roundID) == "" {
		return ErrInvalidRound
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	delete(g.rounds, roundID)
	for key := range g.appeals {
		if key.RoundID == roundID {
			delete(g.appeals, key)
		}
	}
	return nil
}
