package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Order is one trade listing between two players.
type Order struct {
	ID          string
	SellerID    string
	BuyerID     string
	ItemID      string
	Quantity    int
	Gross       int64
	MemberLevel int
	Status      string
}

// Settlement is the audit record of one settled order.
type Settlement struct {
	OrderID string
	Gross   int64
	Fee     int64
	Net     int64
	Key     string
	At      time.Time
}

// TradeSettlementGuard settles trades exactly once and keeps money integral.
type TradeSettlementGuard struct {
}

// NewTradeSettlementGuard builds an empty settlement guard.
func NewTradeSettlementGuard() *TradeSettlementGuard {
	return &TradeSettlementGuard{}
}

// SetFeeRateBps configures the base fee rate in basis points.
func (g *TradeSettlementGuard) SetFeeRateBps(bps int) error {
	return errNotImplemented
}

// SetMemberFeeRateBps overrides the fee rate for one membership level.
func (g *TradeSettlementGuard) SetMemberFeeRateBps(level, bps int) error {
	return errNotImplemented
}

// Credit adds funds to a player wallet.
func (g *TradeSettlementGuard) Credit(playerID string, amount int64) error {
	return errNotImplemented
}

// Balance returns the wallet balance of a player.
func (g *TradeSettlementGuard) Balance(playerID string) int64 {
	return 0
}

// Stock adds items to a player bag.
func (g *TradeSettlementGuard) Stock(playerID, itemID string, qty int) error {
	return errNotImplemented
}

// Items returns how many of each item a player owns.
func (g *TradeSettlementGuard) Items(playerID string) map[string]int {
	return nil
}

// CreateOrder reserves the seller item and escrows the buyer funds.
func (g *TradeSettlementGuard) CreateOrder(o Order) error {
	return errNotImplemented
}

// CancelOrder cancels a pending order and refunds both sides.
func (g *TradeSettlementGuard) CancelOrder(orderID string) error {
	return errNotImplemented
}

// ExpireOrder expires a pending order and refunds both sides.
func (g *TradeSettlementGuard) ExpireOrder(orderID string, at time.Time) error {
	return errNotImplemented
}

// Settle settles an order under the given idempotency key.
func (g *TradeSettlementGuard) Settle(orderID, key string, at time.Time) (Settlement, error) {
	return Settlement{}, errNotImplemented
}

// FeeFor computes the fee of a gross amount for a membership level.
func (g *TradeSettlementGuard) FeeFor(gross int64, memberLevel int) (int64, error) {
	return 0, errNotImplemented
}

// SettlementCount reports how many times an order was settled.
func (g *TradeSettlementGuard) SettlementCount(orderID string) int {
	return 0
}

// AuditLog returns the audit lines recorded for an order.
func (g *TradeSettlementGuard) AuditLog(orderID string) []string {
	return nil
}

// PendingOrders lists the ids of orders that still need settling.
func (g *TradeSettlementGuard) PendingOrders() []string {
	return nil
}

// Snapshot serialises the guard state for a restart.
func (g *TradeSettlementGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (g *TradeSettlementGuard) Restore(data []byte) error {
	return errNotImplemented
}
