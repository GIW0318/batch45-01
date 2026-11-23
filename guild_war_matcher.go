package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

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
	maxRatio float64
}

// NewGuildWarMatcher builds a matcher with the allowed power ratio.
func NewGuildWarMatcher(maxRatio float64) *GuildWarMatcher {
	return &GuildWarMatcher{maxRatio: maxRatio}
}

// MaxRatio is the configured largest acceptable power difference.
func (m *GuildWarMatcher) MaxRatio() float64 {
	return 0
}

// Enqueue adds a guild to the pool, rejecting duplicates.
func (m *GuildWarMatcher) Enqueue(g Guild) error {
	return errNotImplemented
}

// PoolSize is the number of guilds currently waiting.
func (m *GuildWarMatcher) PoolSize() int {
	return 0
}

// PairUp matches two guilds and records the replay seed.
func (m *GuildWarMatcher) PairUp(a, b Guild) (Pair, error) {
	return Pair{}, errNotImplemented
}

// CanPair reports whether two guilds are close enough to fight.
func (m *GuildWarMatcher) CanPair(a, b Guild) bool {
	return false
}

// PowerRatio is the power of the stronger guild divided by the weaker one.
func (m *GuildWarMatcher) PowerRatio(a, b Guild) float64 {
	return 0
}

// PercentilePower is the combat power at the given percentile of a guild group.
func (m *GuildWarMatcher) PercentilePower(guilds []Guild, percentile int) int {
	return 0
}

// LossProtection raises the effective power of a guild on a losing streak.
func (m *GuildWarMatcher) LossProtection(base, streak int) int {
	return 0
}

// CrossServerGroups counts the latency groups in a guild group.
func (m *GuildWarMatcher) CrossServerGroups(guilds []Guild) int {
	return 0
}

// TimeoutRatio is the relaxed power ratio after a long queue wait.
func (m *GuildWarMatcher) TimeoutRatio(waitedSec int) float64 {
	return 0
}

// SmurfBlocked reports whether a guild looks like a farming smurf guild.
func (m *GuildWarMatcher) SmurfBlocked(winRate, battles int) bool {
	return false
}

// Seed derives a reproducible pairing seed from a pair key.
func (m *GuildWarMatcher) Seed(key string) uint64 {
	return 0
}

// LockPair claims a pair, returning false when it is already being matched.
func (m *GuildWarMatcher) LockPair(a, b string) bool {
	return false
}

// Decay reduces a season score for every week of the new season.
func (m *GuildWarMatcher) Decay(score, weeks int) int {
	return 0
}

// RejectPenalty is the matchmaking penalty after rejecting matches.
func (m *GuildWarMatcher) RejectPenalty(rejects int) int {
	return 0
}

// MatchLog returns the pairs created so far, in order.
func (m *GuildWarMatcher) MatchLog() []Pair {
	return nil
}
