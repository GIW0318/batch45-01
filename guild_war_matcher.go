package task

import (
	"errors"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// priorityWaitSec is the age at which a guild is moved into the priority
	// queue. Timed-out guilds are matched before ordinary waiters.
	priorityWaitSec = 90

	// maxGuildsPerServer bounds a single server's waiting room. The bound is
	// deliberately finite so a flood of sign-ups cannot grow memory forever.
	maxGuildsPerServer = 1024

	// matchCooldown keeps a guild that just finished a match out of the pool
	// for a short while instead of allowing it to be paired repeatedly.
	matchCooldown = 5 * time.Minute
)

var (
	ErrServerQueueFull = errors.New("guild war queue is full for this server")
	ErrGuildCooldown   = errors.New("guild is still in match cooldown")
	ErrPairLocked      = errors.New("guild pair is already being matched")
	ErrInvalidPair     = errors.New("invalid guild pair")
	ErrPowerMismatch   = errors.New("guild powers exceed the allowed ratio")
)

// Guild is one guild signing up for a guild war.
type Guild struct {
	ID         string
	Power      int
	Server     string
	WaitedSec  int
	LossStreak int
	Score      int
}

// Pair is one matched pair of guilds together with its replay seed.
type Pair struct {
	A    string
	B    string
	Seed uint64
}

// GuildWarMatcher builds fair guild war matches.
type GuildWarMatcher struct {
	mu sync.Mutex

	maxRatio float64

	pool        map[string]Guild
	order       []string
	priority    []string
	perServer   map[string]int
	locked      map[string]struct{}
	cooldown    map[string]time.Time
	matchRecord []Pair
}

// NewGuildWarMatcher builds a matcher with the allowed power ratio.
func NewGuildWarMatcher(maxRatio float64) *GuildWarMatcher {
	return &GuildWarMatcher{
		maxRatio:  maxRatio,
		pool:      make(map[string]Guild),
		perServer: make(map[string]int),
		locked:    make(map[string]struct{}),
		cooldown:  make(map[string]time.Time),
	}
}

// MaxRatio is the configured largest acceptable power difference.
func (m *GuildWarMatcher) MaxRatio() float64 {
	return m.maxRatio
}

// Enqueue adds a guild to the pool, rejecting duplicates.
//
// Re-signing the same guild is a no-op rather than an error: matchmaking is
// idempotent and a guild must never occupy more than one slot.
func (m *GuildWarMatcher) Enqueue(g Guild) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pool[g.ID]; exists {
		return nil
	}
	if until, exists := m.cooldown[g.ID]; exists {
		if time.Now().Before(until) {
			return ErrGuildCooldown
		}
		delete(m.cooldown, g.ID)
	}
	if m.perServer[g.Server] >= maxGuildsPerServer {
		return ErrServerQueueFull
	}

	m.pool[g.ID] = g
	m.order = append(m.order, g.ID)
	m.perServer[g.Server]++
	if g.WaitedSec >= priorityWaitSec {
		m.priority = append(m.priority, g.ID)
	}

	// A timed-out guild must not wait for an unrelated matching round. Try
	// to place it as soon as another candidate is available.
	m.matchPriorityLocked()
	return nil
}

// PoolSize is the number of guilds currently waiting.
func (m *GuildWarMatcher) PoolSize() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pool)
}

// PairUp matches two guilds and records the replay seed.
func (m *GuildWarMatcher) PairUp(a, b Guild) (Pair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pairUpLocked(a, b)
}

// CanPair reports whether two guilds are close enough to fight.
func (m *GuildWarMatcher) CanPair(a, b Guild) bool {
	return m.canPairWithin(a, b, m.maxRatio)
}

// PowerRatio is the power of the stronger guild divided by the weaker one.
func (m *GuildWarMatcher) PowerRatio(a, b Guild) float64 {
	return powerRatio(a.Power, b.Power)
}

// PercentilePower is the combat power at the given percentile of a guild group.
func (m *GuildWarMatcher) PercentilePower(guilds []Guild, percentile int) int {
	if len(guilds) == 0 {
		return 0
	}
	if percentile <= 0 {
		percentile = 0
	}
	if percentile >= 100 {
		percentile = 100
	}

	sorted := append([]Guild(nil), guilds...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Power < sorted[j].Power
	})

	// Nearest-rank percentile: p90 of ten guilds is the ninth value.
	index := (percentile*len(sorted)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index].Power
}

// LossProtection raises the effective power of a guild on a losing streak.
func (m *GuildWarMatcher) LossProtection(base, streak int) int {
	if base <= 0 || streak <= 0 {
		return base
	}
	return int(float64(base) * (1 + 0.05*float64(streak)))
}

// CrossServerGroups counts the latency groups in a guild group.
func (m *GuildWarMatcher) CrossServerGroups(guilds []Guild) int {
	servers := make(map[string]struct{}, len(guilds))
	for _, g := range guilds {
		servers[g.Server] = struct{}{}
	}
	return len(servers)
}

// TimeoutRatio is the relaxed power ratio after a long queue wait.
//
// Relaxation is intentionally bounded: two minutes of waiting buys a 20%
// wider band, after which no further conditions are dropped.
func (m *GuildWarMatcher) TimeoutRatio(waitedSec int) float64 {
	if waitedSec <= 0 {
		return 1
	}
	if waitedSec >= 120 {
		return 1.2
	}
	return 1 + 0.2*float64(waitedSec)/120
}

// SmurfBlocked reports whether a guild looks like a farming smurf guild.
func (m *GuildWarMatcher) SmurfBlocked(winRate, battles int) bool {
	return winRate >= 85 && battles >= 40
}

// Seed derives a reproducible pairing seed from a pair key.
func (m *GuildWarMatcher) Seed(key string) uint64 {
	key = canonicalSeedKey(key)
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	seed := h.Sum64()
	if seed == 0 {
		// The FNV offset basis is non-zero, but keep the contract explicit.
		seed = 0x9e3779b97f4a7c15
	}
	return seed
}

// LockPair claims a pair, returning false when it is already being matched.
//
// The pair key is canonical, so (a, b) and (b, a) are the same claim.
func (m *GuildWarMatcher) LockPair(a, b string) bool {
	if a == "" || b == "" || a == b {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := pairKey(a, b)
	if _, exists := m.locked[key]; exists {
		return false
	}
	m.locked[key] = struct{}{}
	return true
}

// Decay reduces a season score for every week of the new season.
func (m *GuildWarMatcher) Decay(score, weeks int) int {
	if score <= 0 || weeks <= 0 {
		return score
	}
	decayed := score - weeks*100
	if decayed < 0 {
		return 0
	}
	return decayed
}

// RejectPenalty is the matchmaking penalty after rejecting matches.
func (m *GuildWarMatcher) RejectPenalty(rejects int) int {
	if rejects <= 0 {
		return 0
	}
	return rejects * 10
}

// MatchLog returns the pairs created so far, in order.
func (m *GuildWarMatcher) MatchLog() []Pair {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Pair(nil), m.matchRecord...)
}

// pairUpLocked performs an atomic claim and removal. The caller must hold mu.
func (m *GuildWarMatcher) pairUpLocked(a, b Guild) (Pair, error) {
	if a.ID == b.ID {
		return Pair{}, ErrInvalidPair
	}

	key := pairKey(a.ID, b.ID)
	if _, exists := m.locked[key]; exists {
		return Pair{}, ErrPairLocked
	}

	now := time.Now()
	for _, id := range []string{a.ID, b.ID} {
		if until, exists := m.cooldown[id]; exists && now.Before(until) {
			return Pair{}, ErrGuildCooldown
		}
	}

	waited := a.WaitedSec
	if queued, exists := m.pool[a.ID]; exists && queued.WaitedSec > waited {
		waited = queued.WaitedSec
	}
	if b.WaitedSec > waited {
		waited = b.WaitedSec
	}
	if queued, exists := m.pool[b.ID]; exists && queued.WaitedSec > waited {
		waited = queued.WaitedSec
	}

	limit := m.maxRatio
	if waited >= priorityWaitSec {
		limit *= m.TimeoutRatio(waited)
	}
	if !m.canPairWithin(a, b, limit) {
		return Pair{}, ErrPowerMismatch
	}

	m.locked[key] = struct{}{}
	m.removeFromPoolLocked(a.ID)
	m.removeFromPoolLocked(b.ID)

	until := now.Add(matchCooldown)
	m.cooldown[a.ID] = until
	m.cooldown[b.ID] = until

	pair := Pair{
		A:    a.ID,
		B:    b.ID,
		Seed: m.Seed(key),
	}
	m.matchRecord = append(m.matchRecord, pair)
	return pair, nil
}

func (m *GuildWarMatcher) removeFromPoolLocked(id string) {
	g, exists := m.pool[id]
	if !exists {
		return
	}
	delete(m.pool, id)
	if m.perServer[g.Server] <= 1 {
		delete(m.perServer, g.Server)
	} else {
		m.perServer[g.Server]--
	}
}

// matchPriorityLocked pairs timed-out guilds in FIFO order. The caller must
// hold mu. A priority guild without an acceptable partner stays queued for the
// next sign-up; one blocked guild must not starve the rest of the queue.
func (m *GuildWarMatcher) matchPriorityLocked() {
	for {
		paired := false
		for _, id := range m.priority {
			g, exists := m.pool[id]
			if !exists {
				continue
			}
			best, found := m.bestPartnerLocked(g)
			if !found {
				continue
			}
			if _, err := m.pairUpLocked(g, best); err == nil {
				paired = true
				break
			}
		}
		if !paired {
			return
		}
	}
}

// bestPartnerLocked returns the closest acceptable partner for a timed-out
// guild. Iterating order (rather than the map) keeps replay deterministic;
// equal ratios are broken by arrival order.
func (m *GuildWarMatcher) bestPartnerLocked(g Guild) (Guild, bool) {
	var best Guild
	bestRatio := math.Inf(1)
	found := false
	for _, id := range m.order {
		candidate, exists := m.pool[id]
		if !exists || candidate.ID == g.ID {
			continue
		}
		waited := g.WaitedSec
		if candidate.WaitedSec > waited {
			waited = candidate.WaitedSec
		}
		limit := m.maxRatio
		if waited >= priorityWaitSec {
			limit *= m.TimeoutRatio(waited)
		}
		if !m.canPairWithin(g, candidate, limit) {
			continue
		}
		ratio := m.effectiveRatio(g, candidate)
		if !found || ratio < bestRatio {
			best = candidate
			bestRatio = ratio
			found = true
		}
	}
	return best, found
}

func (m *GuildWarMatcher) canPairWithin(a, b Guild, limit float64) bool {
	if limit < 0 {
		return false
	}
	return m.effectiveRatio(a, b) <= limit+1e-9
}

func (m *GuildWarMatcher) effectiveRatio(a, b Guild) float64 {
	return powerRatio(
		m.LossProtection(a.Power, a.LossStreak),
		m.LossProtection(b.Power, b.LossStreak),
	)
}

func powerRatio(a, b int) float64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a < b {
		a, b = b, a
	}
	if b == 0 {
		if a == 0 {
			return 1
		}
		return math.Inf(1)
	}
	return float64(a) / float64(b)
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// canonicalSeedKey makes the seed independent of which guild was passed as A
// when the key is a normal "guild|guild" pair key.
func canonicalSeedKey(key string) string {
	if strings.Count(key, "|") != 1 {
		return key
	}
	parts := strings.SplitN(key, "|", 2)
	return pairKey(parts[0], parts[1])
}
