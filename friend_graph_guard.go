package task

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// errNotImplemented is kept for compatibility with the original scaffold.
var errNotImplemented = errors.New("not implemented")

const (
	defaultFriendLimit     = 100
	pendingRequestLimit    = 64
	inspectionLogRetention = 30 * 24 * time.Hour
)

var (
	errInvalidInput = errors.New("invalid input")
	errFriendLimit  = errors.New("friend limit reached")
)

// Relation is one directed edge of the friend graph as replicated between servers.
type Relation struct {
	PlayerID  string
	FriendID  string
	Remark    string
	UpdatedAt time.Time
	Version   int64
}

// FriendRequest is a pending friend application.
type FriendRequest struct {
	ID        string
	From      string
	To        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// InspectionLogEntry records the result of a one-way edge inspection.
type InspectionLogEntry struct {
	At       time.Time
	PlayerID string
	FriendID string
	Action   string
}

type relationKey struct {
	low  string
	high string
}

type relationState struct {
	remark    string
	version   int64
	updatedAt time.Time
}

// FriendGraphGuard keeps friendships symmetric and consistent across servers.
type FriendGraphGuard struct {
	mu sync.RWMutex

	limit     int
	friends   map[string]map[string]struct{}
	relations map[relationKey]*relationState
	servers   map[string]string

	requests   map[string]FriendRequest
	pending    map[string]map[string]string
	requestSeq uint64

	blocked map[string]map[string]struct{}

	inspectionLog []InspectionLogEntry
}

// NewFriendGraphGuard builds an empty friend graph with the default friend limit.
func NewFriendGraphGuard() *FriendGraphGuard {
	return &FriendGraphGuard{
		limit:     defaultFriendLimit,
		friends:   make(map[string]map[string]struct{}),
		relations: make(map[relationKey]*relationState),
		servers:   make(map[string]string),
		requests:  make(map[string]FriendRequest),
		pending:   make(map[string]map[string]string),
		blocked:   make(map[string]map[string]struct{}),
	}
}

func (g *FriendGraphGuard) initLocked() {
	if g.friends == nil {
		g.friends = make(map[string]map[string]struct{})
	}
	if g.relations == nil {
		g.relations = make(map[relationKey]*relationState)
	}
	if g.servers == nil {
		g.servers = make(map[string]string)
	}
	if g.requests == nil {
		g.requests = make(map[string]FriendRequest)
	}
	if g.pending == nil {
		g.pending = make(map[string]map[string]string)
	}
	if g.blocked == nil {
		g.blocked = make(map[string]map[string]struct{})
	}
}

func normalizeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%w: empty player id", errInvalidInput)
	}
	return id, nil
}

func normalizePair(a, b string) (string, string, error) {
	a, err := normalizeID(a)
	if err != nil {
		return "", "", err
	}
	b, err = normalizeID(b)
	if err != nil {
		return "", "", err
	}
	if a == b {
		return "", "", fmt.Errorf("%w: player cannot target themselves", errInvalidInput)
	}
	return a, b, nil
}

func pairOf(a, b string) relationKey {
	if a > b {
		a, b = b, a
	}
	return relationKey{low: a, high: b}
}

func (g *FriendGraphGuard) addEdgeLocked(a, b string) {
	if g.friends[a] == nil {
		g.friends[a] = make(map[string]struct{})
	}
	g.friends[a][b] = struct{}{}
}

func (g *FriendGraphGuard) removeEdgeLocked(a, b string) {
	set := g.friends[a]
	if set == nil {
		return
	}
	delete(set, b)
	if len(set) == 0 {
		delete(g.friends, a)
	}
}

func (g *FriendGraphGuard) areFriendsLocked(a, b string) bool {
	_, forward := g.friends[a][b]
	_, reverse := g.friends[b][a]
	return forward && reverse
}

func (g *FriendGraphGuard) setFriendPairLocked(a, b string) {
	g.addEdgeLocked(a, b)
	g.addEdgeLocked(b, a)
	if g.relations[pairOf(a, b)] == nil {
		g.relations[pairOf(a, b)] = &relationState{updatedAt: time.Now().UTC()}
	}
	g.deletePendingBetweenLocked(a, b)
}

func (g *FriendGraphGuard) deletePendingBetweenLocked(a, b string) {
	g.deletePendingLocked(a, b)
	g.deletePendingLocked(b, a)
}

func (g *FriendGraphGuard) deletePendingLocked(to, from string) {
	pool := g.pending[to]
	if pool == nil {
		return
	}
	id, ok := pool[from]
	if !ok {
		return
	}
	delete(pool, from)
	delete(g.requests, id)
	if len(pool) == 0 {
		delete(g.pending, to)
	}
}

func (g *FriendGraphGuard) purgeExpiredRequestsLocked(to string, now time.Time) {
	pool := g.pending[to]
	for from, id := range pool {
		req, ok := g.requests[id]
		if !ok || !now.Before(req.ExpiresAt) {
			delete(pool, from)
			delete(g.requests, id)
		}
	}
	if len(pool) == 0 {
		delete(g.pending, to)
	}
}

func (g *FriendGraphGuard) addFriendLocked(a, b string) error {
	if g.areFriendsLocked(a, b) {
		g.setFriendPairLocked(a, b)
		return nil
	}

	_, forward := g.friends[a][b]
	_, reverse := g.friends[b][a]
	if forward || reverse {
		// A partially replicated pair is repaired instead of being counted twice.
		g.setFriendPairLocked(a, b)
		return nil
	}

	if len(g.friends[a]) >= g.limit || len(g.friends[b]) >= g.limit {
		return fmt.Errorf("%w: %s has %d/%d friends, %s has %d/%d friends",
			errFriendLimit,
			a, len(g.friends[a]), g.limit,
			b, len(g.friends[b]), g.limit)
	}

	g.setFriendPairLocked(a, b)
	return nil
}

func (g *FriendGraphGuard) isBlockedLocked(a, b string) bool {
	_, ok := g.blocked[a][b]
	return ok
}

// SetFriendLimit configures the maximum number of friends one player may have.
func (g *FriendGraphGuard) SetFriendLimit(limit int) error {
	if limit <= 0 {
		return fmt.Errorf("%w: friend limit must be positive", errInvalidInput)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limit = limit
	return nil
}

// FriendLimit reports the configured friend limit.
func (g *FriendGraphGuard) FriendLimit() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.limit
}

// AddFriend creates a symmetric friendship between a and b.
func (g *FriendGraphGuard) AddFriend(a, b string) error {
	a, b, err := normalizePair(a, b)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.addFriendLocked(a, b)
}

// RemoveFriend deletes the friendship between a and b from both sides.
func (g *FriendGraphGuard) RemoveFriend(a, b string) error {
	a, b, err := normalizePair(a, b)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.removeEdgeLocked(a, b)
	g.removeEdgeLocked(b, a)
	delete(g.relations, pairOf(a, b))
	g.deletePendingBetweenLocked(a, b)
	return nil
}

// FriendsOf lists the friend ids of player.
func (g *FriendGraphGuard) FriendsOf(player string) []string {
	player, err := normalizeID(player)
	if err != nil {
		return nil
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	set := g.friends[player]
	if len(set) == 0 {
		return nil
	}
	friends := make([]string, 0, len(set))
	for friend := range set {
		friends = append(friends, friend)
	}
	sort.Strings(friends)
	return friends
}

// FriendCount reports how many friends player has, counting both directions.
func (g *FriendGraphGuard) FriendCount(player string) int {
	player, err := normalizeID(player)
	if err != nil {
		return 0
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.friends[player])
}

// SendRequest creates a friend request from from to to that expires after ttl.
func (g *FriendGraphGuard) SendRequest(from, to string, ttl time.Duration, now time.Time) (FriendRequest, error) {
	from, to, err := normalizePair(from, to)
	if err != nil {
		return FriendRequest{}, err
	}
	if ttl <= 0 {
		return FriendRequest{}, fmt.Errorf("%w: request ttl must be positive", errInvalidInput)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	if g.areFriendsLocked(from, to) {
		return FriendRequest{}, fmt.Errorf("%w: players are already friends", errInvalidInput)
	}
	if g.isBlockedLocked(from, to) || g.isBlockedLocked(to, from) {
		return FriendRequest{}, fmt.Errorf("%w: friend request is blocked", errInvalidInput)
	}

	g.purgeExpiredRequestsLocked(to, now)
	if id, ok := g.pending[to][from]; ok {
		return g.requests[id], nil
	}
	if len(g.pending[to]) >= pendingRequestLimit {
		return FriendRequest{}, fmt.Errorf("%w: pending request pool is full", errFriendLimit)
	}

	g.requestSeq++
	id := fmt.Sprintf("%s->%s:%d", from, to, g.requestSeq)
	req := FriendRequest{
		ID:        id,
		From:      from,
		To:        to,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	g.requests[id] = req
	if g.pending[to] == nil {
		g.pending[to] = make(map[string]string)
	}
	g.pending[to][from] = id
	return req, nil
}

// PendingRequests lists unexpired incoming requests for to.
func (g *FriendGraphGuard) PendingRequests(to string, now time.Time) []FriendRequest {
	to, err := normalizeID(to)
	if err != nil {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.purgeExpiredRequestsLocked(to, now)

	pool := g.pending[to]
	if len(pool) == 0 {
		return nil
	}
	requests := make([]FriendRequest, 0, len(pool))
	for _, id := range pool {
		requests = append(requests, g.requests[id])
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].CreatedAt.Equal(requests[j].CreatedAt) {
			return requests[i].ID < requests[j].ID
		}
		return requests[i].CreatedAt.Before(requests[j].CreatedAt)
	})
	return requests
}

// AcceptRequest accepts the request with the given id.
func (g *FriendGraphGuard) AcceptRequest(id string, now time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: empty request id", errInvalidInput)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	req, ok := g.requests[id]
	if !ok {
		return fmt.Errorf("%w: request not found", errInvalidInput)
	}
	if !now.Before(req.ExpiresAt) {
		g.deletePendingLocked(req.To, req.From)
		return fmt.Errorf("%w: request has expired", errInvalidInput)
	}
	if err := g.addFriendLocked(req.From, req.To); err != nil {
		return err
	}
	g.deletePendingLocked(req.To, req.From)
	return nil
}

// Block makes a block b, which also stops message delivery.
func (g *FriendGraphGuard) Block(a, b string) error {
	a, b, err := normalizePair(a, b)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	if g.blocked[a] == nil {
		g.blocked[a] = make(map[string]struct{})
	}
	g.blocked[a][b] = struct{}{}
	g.deletePendingBetweenLocked(a, b)
	return nil
}

// IsBlocked reports whether a blocked b.
func (g *FriendGraphGuard) IsBlocked(a, b string) bool {
	a, b, err := normalizePair(a, b)
	if err != nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.isBlockedLocked(a, b)
}

// CanMessage reports whether from may send a message to to.
func (g *FriendGraphGuard) CanMessage(from, to string) bool {
	from, to, err := normalizePair(from, to)
	if err != nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.areFriendsLocked(from, to) {
		return false
	}
	return !g.isBlockedLocked(from, to) && !g.isBlockedLocked(to, from)
}

// SetRemark stores the remark owner keeps for friend.
func (g *FriendGraphGuard) SetRemark(owner, friend, remark string) error {
	owner, friend, err := normalizePair(owner, friend)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	if !g.areFriendsLocked(owner, friend) {
		return fmt.Errorf("%w: remark target is not a friend", errInvalidInput)
	}

	key := pairOf(owner, friend)
	state := g.relations[key]
	if state == nil {
		state = &relationState{}
		g.relations[key] = state
	}
	state.remark = remark
	state.version++
	state.updatedAt = time.Now().UTC()
	return nil
}

// Remark returns the remark owner keeps for friend.
func (g *FriendGraphGuard) Remark(owner, friend string) string {
	owner, friend, err := normalizePair(owner, friend)
	if err != nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.areFriendsLocked(owner, friend) {
		return ""
	}
	state := g.relations[pairOf(owner, friend)]
	if state == nil {
		return ""
	}
	return state.remark
}

// SyncFromRemote merges one relation replicated from another server.
func (g *FriendGraphGuard) SyncFromRemote(r Relation) error {
	a, b, err := normalizePair(r.PlayerID, r.FriendID)
	if err != nil {
		return err
	}
	if r.Version < 0 {
		return fmt.Errorf("%w: relation version must not be negative", errInvalidInput)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	key := pairOf(a, b)
	if state := g.relations[key]; state != nil && r.Version <= state.version {
		// Replication is whole-record LWW: a stale or replayed version is dropped
		// before any edge, remark, or timestamp is touched.
		return nil
	}

	g.addEdgeLocked(a, b)
	g.addEdgeLocked(b, a)
	g.relations[key] = &relationState{
		remark:    r.Remark,
		version:   r.Version,
		updatedAt: r.UpdatedAt,
	}
	g.deletePendingBetweenLocked(a, b)
	return nil
}

// RelationVersion returns the stored replication version for the pair a,b.
func (g *FriendGraphGuard) RelationVersion(a, b string) int64 {
	a, b, err := normalizePair(a, b)
	if err != nil {
		return 0
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	state := g.relations[pairOf(a, b)]
	if state == nil {
		return 0
	}
	return state.version
}

// TransferPlayer moves player to another server, keeping the graph intact.
func (g *FriendGraphGuard) TransferPlayer(player, newServer string) error {
	player, err := normalizeID(player)
	if err != nil {
		return err
	}
	newServer = strings.TrimSpace(newServer)
	if newServer == "" {
		return fmt.Errorf("%w: empty server id", errInvalidInput)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	// Relations are keyed by player identity, not by server, so changing the
	// affiliation cannot orphan either side of a friendship.
	g.servers[player] = newServer
	return nil
}

// PresenceTargets lists the players that must receive player's presence update.
func (g *FriendGraphGuard) PresenceTargets(player string) []string {
	player, err := normalizeID(player)
	if err != nil {
		return nil
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	friends := g.friends[player]
	if len(friends) == 0 {
		return nil
	}
	targets := make([]string, 0, len(friends))
	for friend := range friends {
		if g.isBlockedLocked(player, friend) || g.isBlockedLocked(friend, player) {
			continue
		}
		targets = append(targets, friend)
	}
	sort.Strings(targets)
	return targets
}

// InspectRelations runs the daily one-way edge inspection. A missing reverse
// edge is repaired, preserving the existing relationship. Conclusions are
// retained for 30 days.
func (g *FriendGraphGuard) InspectRelations(now time.Time) []InspectionLogEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.pruneInspectionLogLocked(now)

	players := make([]string, 0, len(g.friends))
	for player := range g.friends {
		players = append(players, player)
	}
	sort.Strings(players)

	var found []InspectionLogEntry
	for _, player := range players {
		friends := make([]string, 0, len(g.friends[player]))
		for friend := range g.friends[player] {
			friends = append(friends, friend)
		}
		sort.Strings(friends)
		for _, friend := range friends {
			if _, ok := g.friends[friend][player]; ok {
				continue
			}
			g.addEdgeLocked(friend, player)
			if g.relations[pairOf(player, friend)] == nil {
				g.relations[pairOf(player, friend)] = &relationState{updatedAt: now}
			}
			entry := InspectionLogEntry{
				At:       now,
				PlayerID: player,
				FriendID: friend,
				Action:   "repaired-one-way-edge",
			}
			g.inspectionLog = append(g.inspectionLog, entry)
			found = append(found, entry)
		}
	}
	return found
}

// InspectionLog returns the retained inspection conclusions after pruning
// entries older than 30 days.
func (g *FriendGraphGuard) InspectionLog(now time.Time) []InspectionLogEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneInspectionLogLocked(now)
	log := make([]InspectionLogEntry, len(g.inspectionLog))
	copy(log, g.inspectionLog)
	return log
}

func (g *FriendGraphGuard) pruneInspectionLogLocked(now time.Time) {
	cutoff := now.Add(-inspectionLogRetention)
	kept := g.inspectionLog[:0]
	for _, entry := range g.inspectionLog {
		if !entry.At.Before(cutoff) {
			kept = append(kept, entry)
		}
	}
	for i := len(kept); i < len(g.inspectionLog); i++ {
		g.inspectionLog[i] = InspectionLogEntry{}
	}
	g.inspectionLog = kept
}
