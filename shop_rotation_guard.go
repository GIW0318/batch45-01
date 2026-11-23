package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	errInvalidRefreshHour = errors.New("refresh hour must be in [0, 23]")
	errInvalidQuantity    = errors.New("quantity must be positive")
	errEmptyIdentifier    = errors.New("identifier must not be empty")
	errNoRotation         = errors.New("no active rotation")
	errStaleRotation      = errors.New("purchase time belongs to a different rotation")
	errItemNotOffered     = errors.New("item is not offered in the current rotation")
	errItemDelisted       = errors.New("item is delisted")
	errInsufficientStock  = errors.New("insufficient stock")
	errLimitExceeded      = errors.New("account purchase limit exceeded")
	errCartFull           = errors.New("cart is full")
	errInvalidTTL         = errors.New("lock ttl must be positive")
)

// maxCartEntries bounds how many distinct lines one account cart may hold.
const maxCartEntries = 32

// ShopItem is one item offered by a shop rotation.
type ShopItem struct {
	ID              string
	Price           int64
	Stock           int
	LimitPerAccount int
	Delisted        bool
}

// rotationState is one applied rotation window.
type rotationState struct {
	id    string
	items []ShopItem
	stock map[string]int
}

// ShopRotationGuard keeps shop rotations, stock and limits consistent.
// All state mutations happen under a single mutex so purchases and
// rotations never observe a half applied update.
type ShopRotationGuard struct {
	mu          sync.Mutex
	refreshHour int
	timezone    string
	rotation    *rotationState
	count       int
	purchased   map[string]map[string]map[string]int
	carts       map[string]map[string]int
	audit       []string
	hook        func() error
	lockOwner   string
	lockExpiry  time.Time
	locked      bool
}

// NewShopRotationGuard builds an empty shop guard.
func NewShopRotationGuard() *ShopRotationGuard {
	return &ShopRotationGuard{
		purchased: make(map[string]map[string]map[string]int),
		carts:     make(map[string]map[string]int),
	}
}

// SetRefreshHourUTC configures the UTC hour at which the shop rotates.
func (g *ShopRotationGuard) SetRefreshHourUTC(hour int) error {
	if hour < 0 || hour > 23 {
		return errInvalidRefreshHour
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshHour = hour
	return nil
}

// SetTimezone records the display timezone without moving the boundary.
func (g *ShopRotationGuard) SetTimezone(tz string) error {
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.timezone = tz
	return nil
}

// rotationIDLocked derives the rotation id from the UTC instant shifted back
// by the configured refresh hour. The id is the year plus the period number
// inside that year, so periods are never reused across years.
func (g *ShopRotationGuard) rotationIDLocked(t time.Time) string {
	shifted := t.UTC().Add(-time.Duration(g.refreshHour) * time.Hour)
	return fmt.Sprintf("%04d-%03d", shifted.Year(), shifted.YearDay())
}

// RotationAt returns the rotation id that owns the given instant.
func (g *ShopRotationGuard) RotationAt(t time.Time) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rotationIDLocked(t), nil
}

// Rotate installs a new rotation for the window of the given instant.
// Repeating a rotation for the already active window is a no-op so reruns
// keep the first window and its purchase baseline.
func (g *ShopRotationGuard) Rotate(items []ShopItem, at time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := g.rotationIDLocked(at)
	if g.rotation != nil && g.rotation.id == id {
		return nil
	}
	if g.hook != nil {
		if err := g.hook(); err != nil {
			return fmt.Errorf("rotation hook failed: %w", err)
		}
	}
	next := &rotationState{
		id:    id,
		items: make([]ShopItem, len(items)),
		stock: make(map[string]int, len(items)),
	}
	for i, it := range items {
		next.items[i] = it
		next.stock[it.ID] += it.Stock
	}
	g.rotation = next
	g.count++
	g.audit = append(g.audit, fmt.Sprintf("rotate rotation=%s at=%s", id, at.UTC().Format(time.RFC3339)))
	return nil
}

// CurrentItems returns the items of the active rotation.
func (g *ShopRotationGuard) CurrentItems() []ShopItem {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rotation == nil {
		return nil
	}
	out := make([]ShopItem, len(g.rotation.items))
	copy(out, g.rotation.items)
	return out
}

// RotationCount reports how many new rotation windows were applied.
func (g *ShopRotationGuard) RotationCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.count
}

// AddToCart adds an item to an account cart.
func (g *ShopRotationGuard) AddToCart(accountID, itemID string, qty int) error {
	if accountID == "" || itemID == "" {
		return errEmptyIdentifier
	}
	if qty <= 0 {
		return errInvalidQuantity
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cart, ok := g.carts[accountID]
	if !ok {
		cart = make(map[string]int)
		g.carts[accountID] = cart
	}
	if _, exists := cart[itemID]; !exists && len(cart) >= maxCartEntries {
		return errCartFull
	}
	cart[itemID] += qty
	return nil
}

// Purchase buys an item from a given shard.
// Stock is shared across shards and the per account limit is enforced in the
// same critical section as the stock deduction.
func (g *ShopRotationGuard) Purchase(accountID, characterID, itemID string, qty, shard int, at time.Time) error {
	if accountID == "" || characterID == "" || itemID == "" {
		return errEmptyIdentifier
	}
	if qty <= 0 {
		return errInvalidQuantity
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rotation == nil {
		return errNoRotation
	}
	id := g.rotationIDLocked(at)
	if id != g.rotation.id {
		return errStaleRotation
	}
	var offered *ShopItem
	for i := range g.rotation.items {
		if g.rotation.items[i].ID == itemID {
			offered = &g.rotation.items[i]
			break
		}
	}
	if offered == nil {
		return errItemNotOffered
	}
	if offered.Delisted {
		return errItemDelisted
	}
	if g.rotation.stock[itemID] < qty {
		return errInsufficientStock
	}
	byAccount, ok := g.purchased[id]
	if !ok {
		byAccount = make(map[string]map[string]int)
		g.purchased[id] = byAccount
	}
	byItem, ok := byAccount[accountID]
	if !ok {
		byItem = make(map[string]int)
		byAccount[accountID] = byItem
	}
	if byItem[itemID]+qty > offered.LimitPerAccount {
		return errLimitExceeded
	}
	g.rotation.stock[itemID] -= qty
	byItem[itemID] += qty
	return nil
}

// Purchased reports how many units an account bought in one rotation.
func (g *ShopRotationGuard) Purchased(accountID, itemID, rotationID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.purchased[rotationID][accountID][itemID]
}

// Stock returns the remaining stock of an item across every shard.
func (g *ShopRotationGuard) Stock(itemID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rotation == nil {
		return 0
	}
	return g.rotation.stock[itemID]
}

// ManualRefresh forces an operator triggered refresh of the current window.
func (g *ShopRotationGuard) ManualRefresh(operator string, at time.Time) error {
	if operator == "" {
		return errEmptyIdentifier
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rotation := "none"
	if g.rotation != nil {
		rotation = g.rotation.id
	}
	g.audit = append(g.audit, fmt.Sprintf("manual-refresh operator=%s rotation=%s at=%s",
		operator, rotation, at.UTC().Format(time.RFC3339)))
	return nil
}

// AuditTrail returns the audit lines of the shop.
func (g *ShopRotationGuard) AuditTrail() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.audit))
	copy(out, g.audit)
	return out
}

// Lock acquires the rotation lock for a limited time.
// An expired lock may be taken over by any owner.
func (g *ShopRotationGuard) Lock(owner string, at time.Time, ttl time.Duration) (bool, error) {
	if owner == "" {
		return false, errEmptyIdentifier
	}
	if ttl <= 0 {
		return false, errInvalidTTL
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.locked && g.lockOwner != owner && at.Before(g.lockExpiry) {
		return false, nil
	}
	g.locked = true
	g.lockOwner = owner
	g.lockExpiry = at.Add(ttl)
	return true, nil
}

// SetRotationHook installs a hook that runs inside the rotation transaction.
func (g *ShopRotationGuard) SetRotationHook(h func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hook = h
	return nil
}

// snapshotState is the serialisable view of the guard.
type snapshotState struct {
	RefreshHour int                                  `json:"refresh_hour"`
	Timezone    string                               `json:"timezone"`
	RotationID  string                               `json:"rotation_id"`
	Items       []ShopItem                           `json:"items"`
	Stock       map[string]int                       `json:"stock"`
	Count       int                                  `json:"count"`
	Purchased   map[string]map[string]map[string]int `json:"purchased"`
	Carts       map[string]map[string]int            `json:"carts"`
	Audit       []string                             `json:"audit"`
	Locked      bool                                 `json:"locked"`
	LockOwner   string                               `json:"lock_owner"`
	LockExpiry  time.Time                            `json:"lock_expiry"`
}

// Snapshot serialises the guard state for a restart.
func (g *ShopRotationGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := snapshotState{
		RefreshHour: g.refreshHour,
		Timezone:    g.timezone,
		Count:       g.count,
		Purchased:   g.purchased,
		Carts:       g.carts,
		Audit:       g.audit,
		Locked:      g.locked,
		LockOwner:   g.lockOwner,
		LockExpiry:  g.lockExpiry,
	}
	if g.rotation != nil {
		snap.RotationID = g.rotation.id
		snap.Items = g.rotation.items
		snap.Stock = g.rotation.stock
	}
	return json.Marshal(snap)
}

// Restore loads a snapshot produced by Snapshot.
func (g *ShopRotationGuard) Restore(data []byte) error {
	var snap snapshotState
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("invalid snapshot: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshHour = snap.RefreshHour
	g.timezone = snap.Timezone
	g.count = snap.Count
	g.purchased = snap.Purchased
	if g.purchased == nil {
		g.purchased = make(map[string]map[string]map[string]int)
	}
	g.carts = snap.Carts
	if g.carts == nil {
		g.carts = make(map[string]map[string]int)
	}
	g.audit = snap.Audit
	g.locked = snap.Locked
	g.lockOwner = snap.LockOwner
	g.lockExpiry = snap.LockExpiry
	if snap.RotationID != "" {
		items := make([]ShopItem, len(snap.Items))
		copy(items, snap.Items)
		stock := make(map[string]int, len(snap.Stock))
		for k, v := range snap.Stock {
			stock[k] = v
		}
		g.rotation = &rotationState{id: snap.RotationID, items: items, stock: stock}
	} else {
		g.rotation = nil
	}
	return nil
}
