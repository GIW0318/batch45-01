package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrNotImplemented    = errors.New("not implemented")
	ErrExpRollback       = errors.New("experience rollback rejected")
	ErrLevelLocked       = errors.New("level not unlocked")
	ErrTrackNotPurchased = errors.New("paid track not purchased")
	ErrSeasonClosed      = errors.New("season already closed")
	ErrUnknownAccount    = errors.New("unknown account")
	ErrInvalidTrack      = errors.New("invalid reward track")
	ErrInvalidSnapshot   = errors.New("invalid snapshot")
)

const (
	expPerLevel    int64 = 100
	snapshotFormat       = 1
	maxInt64       int64 = 1<<63 - 1
)

// Track identifies a reward track of the battle pass.
type Track int

const (
	// TrackFree is the reward track available to every player.
	TrackFree Track = iota
	// TrackPaid is the reward track unlocked by purchasing the pass.
	TrackPaid
)

// RewardGrant describes the outcome of one reward claim.
type RewardGrant struct {
	Level          int
	Track          Track
	MailID         string
	AlreadyClaimed bool
}

// ArchiveRecord is the archived progress of one closed season.
type ArchiveRecord struct {
	Season    string `json:"season"`
	AccountID string `json:"account_id"`
	Exp       int64  `json:"exp"`
	Level     int    `json:"level"`
}

type claimKey struct {
	level int
	track Track
}

type accountState struct {
	exp       int64
	purchased bool
	claims    map[claimKey]string
}

// BattlePassLedger keeps season pass progress and reward claims.
type BattlePassLedger struct {
	mu          sync.RWMutex
	season      string
	maxLevel    int
	closed      bool
	accounts    map[string]*accountState
	roles       map[string]string
	archives    map[string][]ArchiveRecord
	lastArchive map[string]ArchiveRecord
}

// NewBattlePassLedger returns a ledger for the given season and level cap.
func NewBattlePassLedger(season string, maxLevel int) *BattlePassLedger {
	if maxLevel < 0 {
		maxLevel = 0
	}
	return &BattlePassLedger{
		season:      season,
		maxLevel:    maxLevel,
		accounts:    make(map[string]*accountState),
		roles:       make(map[string]string),
		archives:    make(map[string][]ArchiveRecord),
		lastArchive: make(map[string]ArchiveRecord),
	}
}

// Season reports the active season identifier.
func (b *BattlePassLedger) Season() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.season
}

// MaxLevel reports the level cap of the pass.
func (b *BattlePassLedger) MaxLevel() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.maxLevel
}

// BindRole binds a game role to an account so both roles share progress.
func (b *BattlePassLedger) BindRole(accountID, roleID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLocked()
	if bound, ok := b.roles[roleID]; ok && bound != accountID {
		return fmt.Errorf("role %q is already bound to account %q", roleID, bound)
	}
	b.roles[roleID] = accountID
	return nil
}

// AddExp adds experience for a role and returns the account total.
func (b *BattlePassLedger) AddExp(accountID, roleID string, amount int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLocked()

	if amount < 0 {
		return b.expLocked(accountID), ErrExpRollback
	}
	if b.closed {
		return b.expLocked(accountID), ErrSeasonClosed
	}

	account := b.accountLocked(accountID)
	account.exp = addCapped(account.exp, amount, b.expCapLocked())

	if roleID != "" {
		if _, exists := b.roles[roleID]; !exists {
			b.roles[roleID] = accountID
		}
	}
	return account.exp, nil
}

// Exp reports the account experience, capped at the level cap.
func (b *BattlePassLedger) Exp(accountID string) int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.expLocked(accountID)
}

// Level reports the account level derived from experience.
func (b *BattlePassLedger) Level(accountID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.levelLocked(b.expLocked(accountID))
}

// Purchase unlocks the paid reward track for the account.
func (b *BattlePassLedger) Purchase(accountID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLocked()
	if b.closed {
		return ErrSeasonClosed
	}
	b.accountLocked(accountID).purchased = true
	return nil
}

// Purchased reports whether the paid track is unlocked.
func (b *BattlePassLedger) Purchased(accountID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	account := b.accounts[accountID]
	return account != nil && account.purchased
}

// Claim grants one level reward on one track.
func (b *BattlePassLedger) Claim(accountID string, level int, track Track) (RewardGrant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLocked()
	return b.claimLocked(accountID, level, track)
}

func (b *BattlePassLedger) claimLocked(accountID string, level int, track Track) (RewardGrant, error) {
	if b.closed {
		return RewardGrant{}, ErrSeasonClosed
	}
	if track != TrackFree && track != TrackPaid {
		return RewardGrant{}, ErrInvalidTrack
	}

	account := b.accounts[accountID]
	if track == TrackPaid && (account == nil || !account.purchased) {
		return RewardGrant{}, ErrTrackNotPurchased
	}
	if level < 1 || level > b.maxLevel || account == nil || account.exp < requiredExp(level) {
		return RewardGrant{}, ErrLevelLocked
	}

	key := claimKey{level: level, track: track}
	if mailID, claimed := account.claims[key]; claimed {
		return RewardGrant{
			Level:          level,
			Track:          track,
			MailID:         mailID,
			AlreadyClaimed: true,
		}, nil
	}

	mailID := b.mailIDLocked(accountID, level, track)
	account.claims[key] = mailID
	return RewardGrant{Level: level, Track: track, MailID: mailID}, nil
}

// GrantCount reports how many times a reward was granted.
func (b *BattlePassLedger) GrantCount(accountID string, level int, track Track) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	account := b.accounts[accountID]
	if account == nil {
		return 0
	}
	if _, ok := account.claims[claimKey{level: level, track: track}]; ok {
		return 1
	}
	return 0
}

// MailCount reports how many reward mails were sent to the account.
func (b *BattlePassLedger) MailCount(accountID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	account := b.accounts[accountID]
	if account == nil {
		return 0
	}
	return len(account.claims)
}

// ApplyCompensation grants missed level rewards in ascending level order.
func (b *BattlePassLedger) ApplyCompensation(accountID string, levels []int) ([]int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLocked()

	if b.closed {
		return nil, ErrSeasonClosed
	}

	ordered := uniqueSortedLevels(levels)
	account := b.accounts[accountID]
	for _, level := range ordered {
		if level < 1 || level > b.maxLevel || account == nil || account.exp < requiredExp(level) {
			return nil, ErrLevelLocked
		}
	}

	applied := make([]int, 0, len(ordered))
	for _, level := range ordered {
		granted := false
		tracks := []Track{TrackFree}
		if account.purchased {
			tracks = append(tracks, TrackPaid)
		}
		for _, track := range tracks {
			key := claimKey{level: level, track: track}
			if _, claimed := account.claims[key]; claimed {
				continue
			}
			account.claims[key] = b.mailIDLocked(accountID, level, track)
			granted = true
		}
		if granted {
			applied = append(applied, level)
		}
	}
	return applied, nil
}

// SwitchSeason archives the account progress and starts a new season.
//
// A season is ledger-wide, so all account progresses are archived together.
// The returned record belongs to accountID.
func (b *BattlePassLedger) SwitchSeason(season, accountID string) (ArchiveRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLocked()

	if season == b.season {
		if record, ok := b.lastArchive[accountID]; ok {
			return record, nil
		}
		return b.archiveRecordLocked(accountID), nil
	}

	oldSeason := b.season
	records := make(map[string]ArchiveRecord, len(b.accounts)+1)
	for id := range b.accounts {
		records[id] = b.archiveRecordLocked(id)
	}
	if _, ok := records[accountID]; !ok {
		records[accountID] = ArchiveRecord{Season: oldSeason, AccountID: accountID}
	}
	for id, record := range records {
		b.archives[id] = append(b.archives[id], record)
		b.lastArchive[id] = record
	}

	b.accounts = make(map[string]*accountState)
	b.season = season
	b.closed = false
	return records[accountID], nil
}

// Close closes the current season so that no further claims are accepted.
func (b *BattlePassLedger) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

// SyncExp merges the account experience from another shard.
func (b *BattlePassLedger) SyncExp(accountID string, exp int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLocked()

	if exp < 0 {
		return ErrExpRollback
	}
	if b.closed {
		return ErrSeasonClosed
	}
	account := b.accountLocked(accountID)
	incoming := exp
	if cap := b.expCapLocked(); incoming > cap {
		incoming = cap
	}
	if incoming > account.exp {
		account.exp = incoming
	}
	return nil
}

// Snapshot serializes the ledger so that claims survive a restart.
//
// The exported copy is cloned under the read lock and marshaled after the lock
// has been released, so a large export does not hold the ledger write lock.
func (b *BattlePassLedger) Snapshot() ([]byte, error) {
	snapshot := b.readSnapshot()
	return json.Marshal(snapshot)
}

// Restore rebuilds the ledger from a snapshot.
func (b *BattlePassLedger) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrInvalidSnapshot
	}

	var snapshot ledgerSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if snapshot.Version != snapshotFormat {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidSnapshot, snapshot.Version)
	}
	if snapshot.MaxLevel < 0 {
		return fmt.Errorf("%w: negative max level", ErrInvalidSnapshot)
	}

	accounts := make(map[string]*accountState, len(snapshot.Accounts))
	for accountID, source := range snapshot.Accounts {
		if source.Exp < 0 {
			return fmt.Errorf("%w: negative experience for %q", ErrInvalidSnapshot, accountID)
		}
		account := &accountState{
			exp:       source.Exp,
			purchased: source.Purchased,
			claims:    make(map[claimKey]string, len(source.Claims)),
		}
		for _, claim := range source.Claims {
			if claim.Level < 1 || (claim.Track != TrackFree && claim.Track != TrackPaid) {
				return fmt.Errorf("%w: invalid claim for %q", ErrInvalidSnapshot, accountID)
			}
			key := claimKey{level: claim.Level, track: claim.Track}
			if _, duplicate := account.claims[key]; duplicate {
				return fmt.Errorf("%w: duplicate claim for %q", ErrInvalidSnapshot, accountID)
			}
			if claim.MailID == "" {
				return fmt.Errorf("%w: empty mail id for %q", ErrInvalidSnapshot, accountID)
			}
			account.claims[key] = claim.MailID
		}
		accounts[accountID] = account
	}
	for _, account := range accounts {
		if cap := expCap(snapshot.MaxLevel); account.exp > cap {
			account.exp = cap
		}
	}

	roles := make(map[string]string, len(snapshot.Roles))
	for roleID, accountID := range snapshot.Roles {
		roles[roleID] = accountID
	}
	archives := make(map[string][]ArchiveRecord, len(snapshot.Archives))
	lastArchive := make(map[string]ArchiveRecord, len(snapshot.Archives))
	for accountID, records := range snapshot.Archives {
		copied := append([]ArchiveRecord(nil), records...)
		archives[accountID] = copied
		if len(copied) > 0 {
			lastArchive[accountID] = copied[len(copied)-1]
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.season = snapshot.Season
	b.maxLevel = snapshot.MaxLevel
	b.closed = snapshot.Closed
	b.accounts = accounts
	b.roles = roles
	b.archives = archives
	b.lastArchive = lastArchive
	return nil
}

func (b *BattlePassLedger) ensureLocked() {
	if b.accounts == nil {
		b.accounts = make(map[string]*accountState)
	}
	if b.roles == nil {
		b.roles = make(map[string]string)
	}
	if b.archives == nil {
		b.archives = make(map[string][]ArchiveRecord)
	}
	if b.lastArchive == nil {
		b.lastArchive = make(map[string]ArchiveRecord)
	}
}

func (b *BattlePassLedger) accountLocked(accountID string) *accountState {
	account := b.accounts[accountID]
	if account == nil {
		account = &accountState{claims: make(map[claimKey]string)}
		b.accounts[accountID] = account
	}
	if account.claims == nil {
		account.claims = make(map[claimKey]string)
	}
	return account
}

func (b *BattlePassLedger) expLocked(accountID string) int64 {
	if account := b.accounts[accountID]; account != nil {
		return account.exp
	}
	return 0
}

func (b *BattlePassLedger) expCapLocked() int64 {
	return expCap(b.maxLevel)
}

func (b *BattlePassLedger) levelLocked(exp int64) int {
	if exp <= 0 || b.maxLevel == 0 {
		return 0
	}
	level := exp / expPerLevel
	if level > int64(b.maxLevel) {
		return b.maxLevel
	}
	return int(level)
}

func (b *BattlePassLedger) mailIDLocked(accountID string, level int, track Track) string {
	return fmt.Sprintf("bp:%s:%s:%d:%d", b.season, accountID, int(track), level)
}

func (b *BattlePassLedger) archiveRecordLocked(accountID string) ArchiveRecord {
	record := ArchiveRecord{Season: b.season, AccountID: accountID}
	if account := b.accounts[accountID]; account != nil {
		record.Exp = account.exp
		record.Level = b.levelLocked(account.exp)
	}
	return record
}

func expCap(maxLevel int) int64 {
	if maxLevel <= 0 {
		return 0
	}
	if int64(maxLevel) > maxInt64/expPerLevel {
		return maxInt64
	}
	return int64(maxLevel) * expPerLevel
}

func requiredExp(level int) int64 {
	if level <= 0 {
		return 0
	}
	if int64(level) > maxInt64/expPerLevel {
		return maxInt64
	}
	return int64(level) * expPerLevel
}

func addCapped(current, amount, cap int64) int64 {
	if current >= cap {
		return cap
	}
	room := cap - current
	if amount >= room {
		return cap
	}
	return current + amount
}

func uniqueSortedLevels(levels []int) []int {
	if len(levels) == 0 {
		return []int{}
	}
	seen := make(map[int]struct{}, len(levels))
	ordered := make([]int, 0, len(levels))
	for _, level := range levels {
		if _, exists := seen[level]; exists {
			continue
		}
		seen[level] = struct{}{}
		ordered = append(ordered, level)
	}
	sort.Ints(ordered)
	return ordered
}

type claimSnapshot struct {
	Level  int    `json:"level"`
	Track  Track  `json:"track"`
	MailID string `json:"mail_id"`
}

type accountSnapshot struct {
	Exp       int64           `json:"exp"`
	Purchased bool            `json:"purchased"`
	Claims    []claimSnapshot `json:"claims,omitempty"`
}

type ledgerSnapshot struct {
	Version  int                        `json:"version"`
	Season   string                     `json:"season"`
	MaxLevel int                        `json:"max_level"`
	Closed   bool                       `json:"closed"`
	Accounts map[string]accountSnapshot `json:"accounts"`
	Roles    map[string]string          `json:"roles,omitempty"`
	Archives map[string][]ArchiveRecord `json:"archives,omitempty"`
}

func (b *BattlePassLedger) readSnapshot() ledgerSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	snapshot := ledgerSnapshot{
		Version:  snapshotFormat,
		Season:   b.season,
		MaxLevel: b.maxLevel,
		Closed:   b.closed,
		Accounts: make(map[string]accountSnapshot, len(b.accounts)),
		Roles:    make(map[string]string, len(b.roles)),
		Archives: make(map[string][]ArchiveRecord, len(b.archives)),
	}
	for accountID, account := range b.accounts {
		claims := make([]claimSnapshot, 0, len(account.claims))
		for key, mailID := range account.claims {
			claims = append(claims, claimSnapshot{
				Level:  key.level,
				Track:  key.track,
				MailID: mailID,
			})
		}
		sort.Slice(claims, func(i, j int) bool {
			if claims[i].Level != claims[j].Level {
				return claims[i].Level < claims[j].Level
			}
			return claims[i].Track < claims[j].Track
		})
		snapshot.Accounts[accountID] = accountSnapshot{
			Exp:       account.exp,
			Purchased: account.purchased,
			Claims:    claims,
		}
	}
	for roleID, accountID := range b.roles {
		snapshot.Roles[roleID] = accountID
	}
	for accountID, records := range b.archives {
		snapshot.Archives[accountID] = append([]ArchiveRecord(nil), records...)
	}
	return snapshot
}
