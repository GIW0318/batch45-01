package task

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// errNotImplemented is retained for compatibility with the original skeleton.
var errNotImplemented = errors.New("not implemented")

var (
	errEmptyCharacter = errors.New("character id is empty")
	errEmptyRaid      = errors.New("raid id is empty")
	errEmptyParty     = errors.New("party is empty")
)

const (
	defaultMaxLocksPerCharacter = 64
	defaultSeasonPurgeBatch     = 1024
	retainedSeasons             = 2
	firstClearSeparator         = "\x00"
)

// Lockout is one raid lockout record.
type Lockout struct {
	CharID     string
	RaidID     string
	Difficulty string
	Season     string
	ExpiresAt  int64
}

// AuditEntry records a gm raid lockout reset.
type AuditEntry struct {
	Actor  string
	CharID string
	Reason string
}

// RaidLockoutGuard keeps raid lockouts correct across servers and restarts.
type RaidLockoutGuard struct {
	raidID string

	mu       sync.RWMutex
	settleMu sync.Mutex

	locks     map[string]Lockout
	lockOrder map[string]uint64
	order     uint64

	seasonLocks   map[string]map[string]struct{}
	charLocks     map[string]map[string]struct{}
	currentSeason string

	firstClears map[string]map[string]struct{}

	activeInstances map[string]string
	nextInstance    uint64

	audits []AuditEntry

	maxLocksPerCharacter int
	seasonPurgeBatch     int
}

// NewRaidLockoutGuard builds an empty lockout guard.
func NewRaidLockoutGuard() *RaidLockoutGuard {
	g := &RaidLockoutGuard{}
	g.initLocked()
	return g
}

func (r *RaidLockoutGuard) initLocked() {
	if r.locks == nil {
		r.locks = make(map[string]Lockout)
	}
	if r.lockOrder == nil {
		r.lockOrder = make(map[string]uint64)
	}
	if r.seasonLocks == nil {
		r.seasonLocks = make(map[string]map[string]struct{})
	}
	if r.charLocks == nil {
		r.charLocks = make(map[string]map[string]struct{})
	}
	if r.firstClears == nil {
		r.firstClears = make(map[string]map[string]struct{})
	}
	if r.activeInstances == nil {
		r.activeInstances = make(map[string]string)
	}
	if r.maxLocksPerCharacter <= 0 {
		r.maxLocksPerCharacter = defaultMaxLocksPerCharacter
	}
	if r.seasonPurgeBatch <= 0 {
		r.seasonPurgeBatch = defaultSeasonPurgeBatch
	}
}

// LockKey is the composite lockout key: character, raid, difficulty and season.
func (r *RaidLockoutGuard) LockKey(charID, raidID, difficulty, season string) string {
	return strings.Join([]string{
		escapeKeyPart(charID),
		escapeKeyPart(raidID),
		escapeKeyPart(difficulty),
		escapeKeyPart(season),
	}, ":")
}

func escapeKeyPart(part string) string {
	if !strings.ContainsAny(part, `:\`) {
		return part
	}
	var b strings.Builder
	b.Grow(len(part) + 4)
	for _, ch := range part {
		if ch == '\\' || ch == ':' {
			b.WriteByte('\\')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

// MapCharacter resolves a cross server character id to the local character id.
func (r *RaidLockoutGuard) MapCharacter(crossID string) string {
	id := crossID

	// Cross-server ids are namespaced. Strip every namespace component so that
	// "cross:char-7", "s3:char-7" and "char-7" all identify the same character.
	for {
		idx := strings.IndexByte(id, ':')
		if idx < 0 {
			break
		}
		id = id[idx+1:]
	}

	for _, prefix := range []string{"cross-", "xserver-"} {
		if strings.HasPrefix(id, prefix) {
			id = strings.TrimPrefix(id, prefix)
			break
		}
	}

	return id
}

func (r *RaidLockoutGuard) localID(id string) string {
	return r.MapCharacter(id)
}

// MarkLocked stores a lockout record.
func (r *RaidLockoutGuard) MarkLocked(l Lockout) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.initLocked()
	l.CharID = r.localID(l.CharID)
	r.putLockLocked(l)
	r.noteSeasonLocked(l.Season)
	r.pruneSeasonsLocked(r.seasonPurgeBatch)
}

func (r *RaidLockoutGuard) putLockLocked(l Lockout) {
	key := r.LockKey(l.CharID, l.RaidID, l.Difficulty, l.Season)
	if _, ok := r.locks[key]; ok {
		r.locks[key] = l
		return
	}

	r.order++
	r.locks[key] = l
	r.lockOrder[key] = r.order
	r.indexLockLocked(key, l)
	r.enforceCharacterLimitLocked(l.CharID)
}

func (r *RaidLockoutGuard) indexLockLocked(key string, l Lockout) {
	set := r.seasonLocks[l.Season]
	if set == nil {
		set = make(map[string]struct{})
		r.seasonLocks[l.Season] = set
	}
	set[key] = struct{}{}

	set = r.charLocks[l.CharID]
	if set == nil {
		set = make(map[string]struct{})
		r.charLocks[l.CharID] = set
	}
	set[key] = struct{}{}
}

func (r *RaidLockoutGuard) removeLockLocked(key string) bool {
	l, ok := r.locks[key]
	if !ok {
		return false
	}

	delete(r.locks, key)
	delete(r.lockOrder, key)
	removeIndexedKey(r.seasonLocks, l.Season, key)
	removeIndexedKey(r.charLocks, l.CharID, key)
	return true
}

func removeIndexedKey(index map[string]map[string]struct{}, group, key string) {
	set := index[group]
	if set == nil {
		return
	}
	delete(set, key)
	if len(set) == 0 {
		delete(index, group)
	}
}

func (r *RaidLockoutGuard) enforceCharacterLimitLocked(charID string) {
	limit := r.maxLocksPerCharacter
	if limit <= 0 {
		limit = defaultMaxLocksPerCharacter
	}
	set := r.charLocks[charID]
	for len(set) > limit {
		var oldestKey string
		var oldestOrder uint64
		first := true
		for key := range set {
			order := r.lockOrder[key]
			if first || order < oldestOrder {
				first = false
				oldestOrder = order
				oldestKey = key
			}
		}
		if oldestKey == "" || !r.removeLockLocked(oldestKey) {
			break
		}
	}
}

// Locked reports whether a character is locked out of a raid.
func (r *RaidLockoutGuard) Locked(charID, raidID, difficulty, season string) bool {
	charID = r.localID(charID)
	key := r.LockKey(charID, raidID, difficulty, season)

	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.locks[key]
	return ok
}

// Persist returns the lockout records that must be written to storage.
func (r *RaidLockoutGuard) Persist() map[string]Lockout {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make(map[string]Lockout, len(r.locks))
	for key, lock := range r.locks {
		out[key] = lock
	}
	return out
}

// Restore reloads lockout records after a restart.
func (r *RaidLockoutGuard) Restore(locks map[string]Lockout) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.initLocked()
	r.locks = make(map[string]Lockout, len(locks))
	r.lockOrder = make(map[string]uint64, len(locks))
	r.seasonLocks = make(map[string]map[string]struct{})
	r.charLocks = make(map[string]map[string]struct{})
	r.order = 0
	r.currentSeason = ""

	for _, lock := range locks {
		lock.CharID = r.localID(lock.CharID)
		key := r.LockKey(lock.CharID, lock.RaidID, lock.Difficulty, lock.Season)
		if _, exists := r.locks[key]; exists {
			continue
		}
		r.order++
		r.locks[key] = lock
		r.lockOrder[key] = r.order
		r.indexLockLocked(key, lock)
		if seasonLater(lock.Season, r.currentSeason) {
			r.currentSeason = lock.Season
		}
	}

	// Restore is a startup path, so it may drain all stale seasons eagerly.
	// Online paths below only recycle one bounded batch at a time.
	r.pruneSeasonsLocked(len(r.locks) + 1)
	for charID := range r.charLocks {
		r.enforceCharacterLimitLocked(charID)
	}
	r.pruneFirstClearsLocked()
}

func (r *RaidLockoutGuard) noteSeasonLocked(season string) {
	if seasonLater(season, r.currentSeason) {
		r.currentSeason = season
		r.pruneFirstClearsLocked()
	}
}

func seasonLater(candidate, current string) bool {
	if candidate == "" {
		return false
	}
	if current == "" {
		return true
	}
	return compareSeason(candidate, current) > 0
}

func compareSeason(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if isDigit(a[i]) && isDigit(b[j]) {
			ai := i
			for ai < len(a) && isDigit(a[ai]) {
				ai++
			}
			bj := j
			for bj < len(b) && isDigit(b[bj]) {
				bj++
			}
			anum := strings.TrimLeft(a[i:ai], "0")
			bnum := strings.TrimLeft(b[j:bj], "0")
			if len(anum) != len(bnum) {
				if len(anum) < len(bnum) {
					return -1
				}
				return 1
			}
			if anum != bnum {
				if anum < bnum {
					return -1
				}
				return 1
			}
			i, j = ai, bj
			continue
		}
		if a[i] != b[j] {
			if a[i] < b[j] {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch {
	case i < len(a):
		return 1
	case j < len(b):
		return -1
	default:
		return 0
	}
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func (r *RaidLockoutGuard) topSeasonsLocked(limit int) map[string]struct{} {
	all := make(map[string]struct{}, len(r.seasonLocks)+1)
	for season := range r.seasonLocks {
		all[season] = struct{}{}
	}
	if r.currentSeason != "" {
		all[r.currentSeason] = struct{}{}
	}

	allowed := make(map[string]struct{}, limit)
	used := make(map[string]struct{}, limit)
	for len(allowed) < limit {
		best := ""
		found := false
		for season := range all {
			if _, ok := used[season]; ok {
				continue
			}
			if !found || compareSeason(season, best) > 0 {
				best = season
				found = true
			}
		}
		if !found {
			break
		}
		used[best] = struct{}{}
		allowed[best] = struct{}{}
	}
	return allowed
}

func (r *RaidLockoutGuard) pruneSeasonsLocked(batch int) {
	if batch <= 0 {
		batch = r.seasonPurgeBatch
	}
	if batch <= 0 {
		batch = defaultSeasonPurgeBatch
	}

	allowed := r.topSeasonsLocked(retainedSeasons)
	removed := 0
	for season, keys := range r.seasonLocks {
		if _, ok := allowed[season]; ok {
			continue
		}
		for key := range keys {
			if r.removeLockLocked(key) {
				removed++
			}
			if removed >= batch {
				return
			}
		}
	}
}

func (r *RaidLockoutGuard) pruneFirstClearsLocked() {
	allowed := r.topSeasonsLocked(retainedSeasons)
	for season := range r.firstClears {
		if _, ok := allowed[season]; !ok {
			delete(r.firstClears, season)
		}
	}
}

func firstClearKey(charID, raidID string) string {
	return charID + firstClearSeparator + raidID
}

func (r *RaidLockoutGuard) claimFirstClearLocked(charID, raidID string) bool {
	season := r.currentSeason
	set := r.firstClears[season]
	if set == nil {
		set = make(map[string]struct{})
		r.firstClears[season] = set
	}
	key := firstClearKey(charID, raidID)
	if _, ok := set[key]; ok {
		return false
	}
	set[key] = struct{}{}
	return true
}

// MarkFirstClear marks the first clear bonus as claimed, returning true when it was free.
func (r *RaidLockoutGuard) MarkFirstClear(charID, raidID string) bool {
	charID = r.localID(charID)
	if charID == "" || raidID == "" {
		return false
	}

	r.settleMu.Lock()
	defer r.settleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.initLocked()
	return r.claimFirstClearLocked(charID, raidID)
}

// ClaimFirstClear atomically claims the weekly first clear bonus.
func (r *RaidLockoutGuard) ClaimFirstClear(charID, raidID string) (bool, error) {
	charID = r.localID(charID)
	if charID == "" {
		return false, errEmptyCharacter
	}
	if raidID == "" {
		return false, errEmptyRaid
	}

	r.settleMu.Lock()
	defer r.settleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.initLocked()
	return r.claimFirstClearLocked(charID, raidID), nil
}

// CanEnterParty checks that every party member may enter the raid.
func (r *RaidLockoutGuard) CanEnterParty(charIDs []string, raidID, difficulty, season string) (bool, error) {
	r.settleMu.Lock()
	defer r.settleMu.Unlock()

	if len(charIDs) == 0 {
		return false, errEmptyParty
	}
	if raidID == "" {
		return false, errEmptyRaid
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, rawID := range charIDs {
		charID := r.localID(rawID)
		if charID == "" {
			return false, errEmptyCharacter
		}
		if _, ok := r.locks[r.LockKey(charID, raidID, difficulty, season)]; ok {
			return false, nil
		}
	}
	return true, nil
}

// NextReset is the next raid reset moment in unix seconds for the given utc offset.
func (r *RaidLockoutGuard) NextReset(now int64, utcOffsetHours int) int64 {
	const secondsPerDay = int64(24 * 60 * 60)
	offset := int64(utcOffsetHours) * 60 * 60
	localDay := floorDiv(now+offset, secondsPerDay)
	return (localDay+1)*secondsPerDay - offset
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// ClaimInstance creates a raid instance once for a party.
func (r *RaidLockoutGuard) ClaimInstance(charIDs []string) (string, error) {
	r.settleMu.Lock()
	defer r.settleMu.Unlock()

	if len(charIDs) == 0 {
		return "", errEmptyParty
	}

	members := make([]string, 0, len(charIDs))
	seen := make(map[string]struct{}, len(charIDs))
	for _, rawID := range charIDs {
		charID := r.localID(rawID)
		if charID == "" {
			return "", errEmptyCharacter
		}
		if _, ok := seen[charID]; ok {
			continue
		}
		seen[charID] = struct{}{}
		members = append(members, charID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.initLocked()

	existingInstance := ""
	allActive := true
	for _, charID := range members {
		instanceID, active := r.activeInstances[charID]
		if !active {
			allActive = false
			continue
		}
		if existingInstance == "" {
			existingInstance = instanceID
		} else if existingInstance != instanceID {
			return "", nil
		}
	}
	if allActive && existingInstance != "" {
		return existingInstance, nil
	}
	if existingInstance != "" {
		return "", nil
	}

	r.nextInstance++
	instanceID := fmt.Sprintf("instance-%d", r.nextInstance)
	for _, charID := range members {
		r.activeInstances[charID] = instanceID
	}
	return instanceID, nil
}

// AuditReset records a gm reset of a character lockout.
func (r *RaidLockoutGuard) AuditReset(actor, charID string) AuditEntry {
	charID = r.localID(charID)
	entry := AuditEntry{
		Actor:  actor,
		CharID: charID,
		Reason: "gm reset",
	}

	r.settleMu.Lock()
	defer r.settleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.initLocked()
	keys := make([]string, 0, len(r.charLocks[charID]))
	for key := range r.charLocks[charID] {
		keys = append(keys, key)
	}
	for _, key := range keys {
		r.removeLockLocked(key)
	}
	r.audits = append(r.audits, entry)
	return entry
}

// AuditLog returns a snapshot of gm reset audit entries.
func (r *RaidLockoutGuard) AuditLog() []AuditEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]AuditEntry, len(r.audits))
	copy(out, r.audits)
	return out
}

// TransferCD moves lockouts from one character to another and reports how many moved.
func (r *RaidLockoutGuard) TransferCD(from, to string) int {
	from = r.localID(from)
	to = r.localID(to)
	if from == "" || to == "" || from == to {
		return 0
	}

	r.settleMu.Lock()
	defer r.settleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.initLocked()
	keys := make([]string, 0, len(r.charLocks[from]))
	for key := range r.charLocks[from] {
		keys = append(keys, key)
	}

	moved := 0
	for _, key := range keys {
		lock, ok := r.locks[key]
		if !ok {
			continue
		}
		r.removeLockLocked(key)

		lock.CharID = to
		newKey := r.LockKey(lock.CharID, lock.RaidID, lock.Difficulty, lock.Season)
		if existing, exists := r.locks[newKey]; exists {
			if lock.ExpiresAt > existing.ExpiresAt {
				existing.ExpiresAt = lock.ExpiresAt
				r.locks[newKey] = existing
			}
		} else {
			r.putLockLocked(lock)
		}
		moved++
	}

	r.moveFirstClearsLocked(from, to)
	return moved
}

func (r *RaidLockoutGuard) moveFirstClearsLocked(from, to string) {
	prefix := from + firstClearSeparator
	for _, set := range r.firstClears {
		moves := make([]string, 0)
		for key := range set {
			if strings.HasPrefix(key, prefix) {
				moves = append(moves, key)
			}
		}
		for _, key := range moves {
			raidID := strings.TrimPrefix(key, prefix)
			delete(set, key)
			set[firstClearKey(to, raidID)] = struct{}{}
		}
	}
}

// PurgeExpired removes expired lockouts and reports how many were removed.
func (r *RaidLockoutGuard) PurgeExpired(now int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.initLocked()
	keys := make([]string, 0)
	for key, lock := range r.locks {
		if lock.ExpiresAt > 0 && lock.ExpiresAt <= now {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		r.removeLockLocked(key)
	}
	return len(keys)
}

// ExpiresAt is the stored expiry of a lockout.
func (r *RaidLockoutGuard) ExpiresAt(charID, raidID, difficulty, season string) int64 {
	charID = r.localID(charID)
	key := r.LockKey(charID, raidID, difficulty, season)

	r.mu.RLock()
	defer r.mu.RUnlock()
	lock, ok := r.locks[key]
	if !ok {
		return 0
	}
	return lock.ExpiresAt
}

// Leave records that a character left the raid, which must keep the lockout.
func (r *RaidLockoutGuard) Leave(charID string) {
	charID = r.localID(charID)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.initLocked()
	delete(r.activeInstances, charID)
}

// SetSeasonPurgeBatch configures how many stale-season locks one online
// operation may recycle. Restore still drains stale seasons eagerly.
func (r *RaidLockoutGuard) SetSeasonPurgeBatch(batch int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if batch > 0 {
		r.seasonPurgeBatch = batch
	}
}

// SetMaxLocksPerCharacter configures the per-character lockout cap.
func (r *RaidLockoutGuard) SetMaxLocksPerCharacter(limit int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 {
		return
	}
	r.maxLocksPerCharacter = limit
	for charID := range r.charLocks {
		r.enforceCharacterLimitLocked(charID)
	}
}

// SetCurrentSeason moves the guard to a season without waiting for the first
// lock of that season to be written.
func (r *RaidLockoutGuard) SetCurrentSeason(season string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initLocked()
	if seasonLater(season, r.currentSeason) {
		r.currentSeason = season
		r.pruneFirstClearsLocked()
	}
	r.pruneSeasonsLocked(r.seasonPurgeBatch)
}
