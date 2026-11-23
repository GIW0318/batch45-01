package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// ShopItem is one item offered by a shop rotation.
type ShopItem struct {
	ID              string
	Price           int64
	Stock           int
	LimitPerAccount int
	Delisted        bool
}

// ShopRotationGuard keeps shop rotations, stock and limits consistent.
type ShopRotationGuard struct {
}

// NewShopRotationGuard builds an empty shop guard.
func NewShopRotationGuard() *ShopRotationGuard {
	return &ShopRotationGuard{}
}

// SetRefreshHourUTC configures the UTC hour at which the shop rotates.
func (g *ShopRotationGuard) SetRefreshHourUTC(hour int) error {
	return errNotImplemented
}

// SetTimezone records the display timezone without moving the boundary.
func (g *ShopRotationGuard) SetTimezone(tz string) error {
	return errNotImplemented
}

// RotationAt returns the rotation id that owns the given instant.
func (g *ShopRotationGuard) RotationAt(t time.Time) (string, error) {
	return "", errNotImplemented
}

// Rotate installs a new rotation for the window of the given instant.
func (g *ShopRotationGuard) Rotate(items []ShopItem, at time.Time) error {
	return errNotImplemented
}

// CurrentItems returns the items of the active rotation.
func (g *ShopRotationGuard) CurrentItems() []ShopItem {
	return nil
}

// RotationCount reports how many new rotation windows were applied.
func (g *ShopRotationGuard) RotationCount() int {
	return 0
}

// AddToCart adds an item to an account cart.
func (g *ShopRotationGuard) AddToCart(accountID, itemID string, qty int) error {
	return errNotImplemented
}

// Purchase buys an item from a given shard.
func (g *ShopRotationGuard) Purchase(accountID, characterID, itemID string, qty, shard int, at time.Time) error {
	return errNotImplemented
}

// Purchased reports how many units an account bought in one rotation.
func (g *ShopRotationGuard) Purchased(accountID, itemID, rotationID string) int {
	return 0
}

// Stock returns the remaining stock of an item across every shard.
func (g *ShopRotationGuard) Stock(itemID string) int {
	return 0
}

// ManualRefresh forces an operator triggered refresh of the current window.
func (g *ShopRotationGuard) ManualRefresh(operator string, at time.Time) error {
	return errNotImplemented
}

// AuditTrail returns the audit lines of the shop.
func (g *ShopRotationGuard) AuditTrail() []string {
	return nil
}

// Lock acquires the rotation lock for a limited time.
func (g *ShopRotationGuard) Lock(owner string, at time.Time, ttl time.Duration) (bool, error) {
	return false, errNotImplemented
}

// SetRotationHook installs a hook that runs inside the rotation transaction.
func (g *ShopRotationGuard) SetRotationHook(h func() error) error {
	return errNotImplemented
}

// Snapshot serialises the guard state for a restart.
func (g *ShopRotationGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (g *ShopRotationGuard) Restore(data []byte) error {
	return errNotImplemented
}
