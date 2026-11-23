package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Order is one auction order: a seller listing or a buyer bid.
type Order struct {
	ID      string
	ItemKey string
	Buyer   string
	Price   int64
	Kind    string
	Arrived int64
}

// Trade is one completed auction trade.
type Trade struct {
	ItemKey string
	Buyer   string
	Seller  string
	Price   int64
	Fee     int64
}

// AuctionEngine matches auction orders without double selling an item.
type AuctionEngine struct {
}

// NewAuctionEngine builds an empty auction matching engine.
func NewAuctionEngine() *AuctionEngine {
	return &AuctionEngine{}
}

// Place accepts an order and escrows the money of a buyer order.
func (e *AuctionEngine) Place(o Order) error {
	return errNotImplemented
}

// Escrow is the money currently held for a buyer.
func (e *AuctionEngine) Escrow(buyer string) int64 {
	return 0
}

// Match executes at most one trade for an item.
func (e *AuctionEngine) Match(itemKey string) (Trade, error) {
	return Trade{}, errNotImplemented
}

// TradeCount is the number of trades executed for an item.
func (e *AuctionEngine) TradeCount(itemKey string) int {
	return 0
}

// RemainingSeconds is the auction time left according to the server clock.
func (e *AuctionEngine) RemainingSeconds(now, expire int64) int64 {
	return 0
}

// ResolvePrice picks either the bid price or the buyout price, never a mix.
func (e *AuctionEngine) ResolvePrice(kind string, bid, buyout int64) int64 {
	return 0
}

// OutbidNotice is the notice sent to the previously highest buyer.
func (e *AuctionEngine) OutbidNotice(prevBuyer string) string {
	return ""
}

// Cancel cancels an order and returns the refunded escrow.
func (e *AuctionEngine) Cancel(orderID string) int64 {
	return 0
}

// DuplicateBid reports whether a buyer already holds a bid on the item.
func (e *AuctionEngine) DuplicateBid(buyer, itemKey string) bool {
	return false
}

// Priority orders order ids by price first, arrival time second.
func (e *AuctionEngine) Priority(orders []Order) []string {
	return nil
}

// SyncInventory propagates sold inventory to another server.
func (e *AuctionEngine) SyncInventory(server, itemKey string, count int) int {
	return 0
}

// ExpireReturn returns the item of an expired auction to its seller.
func (e *AuctionEngine) ExpireReturn(itemKey string) (string, error) {
	return "", errNotImplemented
}

// Fee is the auction house cut for a net trade price.
func (e *AuctionEngine) Fee(net int64) int64 {
	return 0
}

// Restore reloads unfinished orders after the engine restarts.
func (e *AuctionEngine) Restore(orders []Order) int {
	return 0
}

// HighestBid is the highest bid currently standing for an item.
func (e *AuctionEngine) HighestBid(itemKey string) int64 {
	return 0
}

// SellOnce consumes an item so that it cannot be sold twice.
func (e *AuctionEngine) SellOnce(itemKey string) (bool, error) {
	return false, errNotImplemented
}
