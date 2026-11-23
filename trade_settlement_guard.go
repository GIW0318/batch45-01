package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"
)

const (
	defaultFeeRateBps = 500
	maxFeeRateBps     = 10_000
	maxAuditLines     = 256
	snapshotVersion   = 1

	orderStatusPending   = "pending"
	orderStatusSettled   = "settled"
	orderStatusCancelled = "cancelled"
	orderStatusExpired   = "expired"
)

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

type orderState struct {
	order         Order
	status        string
	escrowGross   int64
	escrowQty     int
	settlement    Settlement
	settlementKey string
	audit         []string
}

// TradeSettlementGuard settles trades exactly once and keeps money integral.
type TradeSettlementGuard struct {
	mu sync.Mutex

	initialized    bool
	feeRateBps     int
	memberFeeRates map[int]int

	balances map[string]int64
	items    map[string]map[string]int

	orders           map[string]*orderState
	settlementCounts map[string]int
	keyOrders        map[string]string
	watermark        uint64
}

// NewTradeSettlementGuard builds an empty settlement guard.
func NewTradeSettlementGuard() *TradeSettlementGuard {
	g := &TradeSettlementGuard{}
	g.initLocked()
	return g
}

func (g *TradeSettlementGuard) initLocked() {
	if !g.initialized {
		g.feeRateBps = defaultFeeRateBps
		g.initialized = true
	}
	if g.memberFeeRates == nil {
		g.memberFeeRates = make(map[int]int)
	}
	if g.balances == nil {
		g.balances = make(map[string]int64)
	}
	if g.items == nil {
		g.items = make(map[string]map[string]int)
	}
	if g.orders == nil {
		g.orders = make(map[string]*orderState)
	}
	if g.settlementCounts == nil {
		g.settlementCounts = make(map[string]int)
	}
	if g.keyOrders == nil {
		g.keyOrders = make(map[string]string)
	}
}

// SetFeeRateBps configures the base fee rate in basis points.
func (g *TradeSettlementGuard) SetFeeRateBps(bps int) error {
	if bps < 0 || bps > maxFeeRateBps {
		return fmt.Errorf("fee rate %d bps is outside [0,%d]", bps, maxFeeRateBps)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.feeRateBps = bps
	return nil
}

// SetMemberFeeRateBps overrides the fee rate for one membership level.
func (g *TradeSettlementGuard) SetMemberFeeRateBps(level, bps int) error {
	if level < 0 {
		return fmt.Errorf("membership level must not be negative: %d", level)
	}
	if bps < 0 || bps > maxFeeRateBps {
		return fmt.Errorf("member fee rate %d bps is outside [0,%d]", bps, maxFeeRateBps)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.memberFeeRates[level] = bps
	return nil
}

// Credit adds funds to a player wallet.
func (g *TradeSettlementGuard) Credit(playerID string, amount int64) error {
	if playerID == "" {
		return errors.New("player id is required")
	}
	if amount < 0 {
		return fmt.Errorf("credit amount must not be negative: %d", amount)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	balance, ok := addInt64(g.balances[playerID], amount)
	if !ok {
		return errors.New("wallet balance overflow")
	}
	g.balances[playerID] = balance
	return nil
}

// Balance returns the wallet balance of a player.
func (g *TradeSettlementGuard) Balance(playerID string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.balances[playerID]
}

// Stock adds items to a player bag.
func (g *TradeSettlementGuard) Stock(playerID, itemID string, qty int) error {
	if playerID == "" || itemID == "" {
		return errors.New("player id and item id are required")
	}
	if qty <= 0 {
		return fmt.Errorf("stock quantity must be positive: %d", qty)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	current := g.itemCountLocked(playerID, itemID)
	next, ok := addInt(current, qty)
	if !ok {
		return errors.New("item quantity overflow")
	}
	g.setItemCountLocked(playerID, itemID, next)
	return nil
}

// Items returns how many of each item a player owns.
func (g *TradeSettlementGuard) Items(playerID string) map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	result := make(map[string]int)
	for itemID, qty := range g.items[playerID] {
		result[itemID] = qty
	}
	return result
}

// CreateOrder reserves the seller item and escrows the buyer funds.
func (g *TradeSettlementGuard) CreateOrder(o Order) error {
	if err := validateOrder(o); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	if _, exists := g.orders[o.ID]; exists {
		return fmt.Errorf("order %q already exists", o.ID)
	}
	if g.balances[o.BuyerID] < o.Gross {
		return fmt.Errorf("buyer %q has insufficient funds: have %d, need %d", o.BuyerID, g.balances[o.BuyerID], o.Gross)
	}
	sellerQty := g.itemCountLocked(o.SellerID, o.ItemID)
	if sellerQty < o.Quantity {
		return fmt.Errorf("seller %q has insufficient item %q: have %d, need %d", o.SellerID, o.ItemID, sellerQty, o.Quantity)
	}

	// All checks above pass before any state changes, so creation is atomic.
	g.balances[o.BuyerID] -= o.Gross
	g.setItemCountLocked(o.SellerID, o.ItemID, sellerQty-o.Quantity)

	o.Status = orderStatusPending
	state := &orderState{
		order:       o,
		status:      orderStatusPending,
		escrowGross: o.Gross,
		escrowQty:   o.Quantity,
	}
	g.appendAuditLocked(state, fmt.Sprintf(
		"create order=%s seller=%s buyer=%s item=%s qty=%d gross=%d",
		o.ID, o.SellerID, o.BuyerID, o.ItemID, o.Quantity, o.Gross,
	))
	g.appendAuditLocked(state, fmt.Sprintf("escrow-funds buyer=%s amount=%d", o.BuyerID, o.Gross))
	g.appendAuditLocked(state, fmt.Sprintf("escrow-item seller=%s item=%s qty=%d", o.SellerID, o.ItemID, o.Quantity))
	g.orders[o.ID] = state
	return nil
}

// CancelOrder cancels a pending order and refunds both sides.
func (g *TradeSettlementGuard) CancelOrder(orderID string) error {
	if orderID == "" {
		return errors.New("order id is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	state, exists := g.orders[orderID]
	if !exists {
		return fmt.Errorf("order %q does not exist", orderID)
	}
	if state.status != orderStatusPending {
		return fmt.Errorf("order %q is %s and cannot be cancelled", orderID, state.status)
	}
	if err := g.refundPendingLocked(state); err != nil {
		return err
	}
	state.status = orderStatusCancelled
	state.order.Status = orderStatusCancelled
	state.escrowGross = 0
	state.escrowQty = 0
	g.appendAuditLocked(state, fmt.Sprintf(
		"cancel order=%s refund=%d item=%s qty=%d",
		orderID, state.order.Gross, state.order.ItemID, state.order.Quantity,
	))
	return nil
}

// ExpireOrder expires a pending order and refunds both sides.
func (g *TradeSettlementGuard) ExpireOrder(orderID string, at time.Time) error {
	if orderID == "" {
		return errors.New("order id is required")
	}
	if at.IsZero() {
		return errors.New("expiry time is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	state, exists := g.orders[orderID]
	if !exists {
		return fmt.Errorf("order %q does not exist", orderID)
	}
	if state.status != orderStatusPending {
		return fmt.Errorf("order %q is %s and cannot expire", orderID, state.status)
	}
	if err := g.refundPendingLocked(state); err != nil {
		return err
	}
	state.status = orderStatusExpired
	state.order.Status = orderStatusExpired
	state.escrowGross = 0
	state.escrowQty = 0
	g.appendAuditLocked(state, fmt.Sprintf(
		"expire order=%s at=%s refund=%d item=%s qty=%d",
		orderID, at.UTC().Format(time.RFC3339Nano), state.order.Gross, state.order.ItemID, state.order.Quantity,
	))
	return nil
}

// Settle settles an order under the given idempotency key.
func (g *TradeSettlementGuard) Settle(orderID, key string, at time.Time) (Settlement, error) {
	if orderID == "" {
		return Settlement{}, errors.New("order id is required")
	}
	if key == "" {
		return Settlement{}, errors.New("idempotency key is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	state, exists := g.orders[orderID]
	if !exists {
		return Settlement{}, fmt.Errorf("order %q does not exist", orderID)
	}
	if state.status == orderStatusSettled {
		if state.settlementKey == key {
			g.appendAuditLocked(state, fmt.Sprintf("settle-replay order=%s key=%s count=1", orderID, key))
			return state.settlement, nil
		}
		g.appendAuditLocked(state, fmt.Sprintf(
			"reject-settle order=%s key=%s original-key=%s reason=key-mismatch",
			orderID, key, state.settlementKey,
		))
		return Settlement{}, fmt.Errorf("order %q was already settled under a different idempotency key", orderID)
	}
	if state.status != orderStatusPending {
		g.appendAuditLocked(state, fmt.Sprintf(
			"reject-settle order=%s key=%s reason=status-%s",
			orderID, key, state.status,
		))
		return Settlement{}, fmt.Errorf("order %q is %s and cannot be settled", orderID, state.status)
	}
	if owner, used := g.keyOrders[key]; used && owner != orderID {
		return Settlement{}, fmt.Errorf("idempotency key %q was already used by order %q", key, owner)
	}

	fee, err := g.feeForLocked(state.order.Gross, state.order.MemberLevel)
	if err != nil {
		return Settlement{}, err
	}
	net := state.order.Gross - fee
	sellerBalance, ok := addInt64(g.balances[state.order.SellerID], net)
	if !ok {
		return Settlement{}, errors.New("seller wallet balance overflow")
	}
	buyerItemQty := g.itemCountLocked(state.order.BuyerID, state.order.ItemID)
	deliveredQty, ok := addInt(buyerItemQty, state.escrowQty)
	if !ok {
		return Settlement{}, errors.New("buyer item quantity overflow")
	}

	// Commit after every operation above has been validated.
	g.balances[state.order.SellerID] = sellerBalance
	g.setItemCountLocked(state.order.BuyerID, state.order.ItemID, deliveredQty)
	state.escrowGross = 0
	state.escrowQty = 0
	state.status = orderStatusSettled
	state.order.Status = orderStatusSettled
	state.settlementKey = key
	state.settlement = Settlement{
		OrderID: orderID,
		Gross:   state.order.Gross,
		Fee:     fee,
		Net:     net,
		Key:     key,
		At:      at,
	}
	g.settlementCounts[orderID] = 1
	g.keyOrders[key] = orderID
	g.watermark++
	g.appendAuditLocked(state, fmt.Sprintf(
		"settle order=%s key=%s gross=%d fee=%d net=%d at=%s",
		orderID, key, state.settlement.Gross, fee, net, at.UTC().Format(time.RFC3339Nano),
	))
	g.appendAuditLocked(state, fmt.Sprintf("credit-seller seller=%s amount=%d", state.order.SellerID, net))
	g.appendAuditLocked(state, fmt.Sprintf(
		"deliver-item buyer=%s item=%s qty=%d",
		state.order.BuyerID, state.order.ItemID, state.order.Quantity,
	))
	return state.settlement, nil
}

// FeeFor computes the fee of a gross amount for a membership level.
func (g *TradeSettlementGuard) FeeFor(gross int64, memberLevel int) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.feeForLocked(gross, memberLevel)
}

// SettlementCount reports how many times an order was settled.
func (g *TradeSettlementGuard) SettlementCount(orderID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.settlementCounts[orderID]
}

// AuditLog returns the audit lines recorded for an order.
func (g *TradeSettlementGuard) AuditLog(orderID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	state := g.orders[orderID]
	if state == nil {
		return nil
	}
	return append([]string(nil), state.audit...)
}

// PendingOrders lists the ids of orders that still need settling.
func (g *TradeSettlementGuard) PendingOrders() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	ids := make([]string, 0)
	for id, state := range g.orders {
		if state.status == orderStatusPending {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Snapshot serialises the guard state for a restart.
func (g *TradeSettlementGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	snapshot := snapshotState{
		Version:              snapshotVersion,
		PendingOrders:        make([]snapshotOrder, 0),
		IdempotencyWatermark: g.watermark,
	}
	ids := make([]string, 0)
	for id, state := range g.orders {
		if state.status == orderStatusPending {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	for _, id := range ids {
		state := g.orders[id]
		if state.escrowGross != state.order.Gross || state.escrowQty != state.order.Quantity {
			return nil, fmt.Errorf("order %q has inconsistent escrow state", id)
		}
		sum, ok := addInt64(snapshot.EscrowBalance, state.escrowGross)
		if !ok {
			return nil, errors.New("escrow balance overflow")
		}
		snapshot.EscrowBalance = sum
		snapshot.PendingOrders = append(snapshot.PendingOrders, snapshotOrder{
			ID:          state.order.ID,
			SellerID:    state.order.SellerID,
			BuyerID:     state.order.BuyerID,
			ItemID:      state.order.ItemID,
			Quantity:    state.order.Quantity,
			Gross:       state.order.Gross,
			MemberLevel: state.order.MemberLevel,
			EscrowGross: state.escrowGross,
			EscrowQty:   state.escrowQty,
			Audit:       append([]string(nil), state.audit...),
		})
	}
	return json.Marshal(snapshot)
}

// Restore loads a snapshot produced by Snapshot.
func (g *TradeSettlementGuard) Restore(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("snapshot is empty")
	}
	var snapshot snapshotState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fmt.Errorf("decode snapshot trailing data: %w", err)
		}
		return errors.New("snapshot contains trailing data")
	}
	if snapshot.Version != snapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d", snapshot.Version)
	}
	if snapshot.PendingOrders == nil {
		return errors.New("snapshot is missing pending_orders")
	}

	restoredOrders := make(map[string]*orderState, len(snapshot.PendingOrders))
	var escrowTotal int64
	for _, item := range snapshot.PendingOrders {
		order := Order{
			ID:          item.ID,
			SellerID:    item.SellerID,
			BuyerID:     item.BuyerID,
			ItemID:      item.ItemID,
			Quantity:    item.Quantity,
			Gross:       item.Gross,
			MemberLevel: item.MemberLevel,
			Status:      orderStatusPending,
		}
		if err := validateOrder(order); err != nil {
			return fmt.Errorf("invalid pending order in snapshot: %w", err)
		}
		if _, exists := restoredOrders[item.ID]; exists {
			return fmt.Errorf("duplicate pending order %q in snapshot", item.ID)
		}
		if item.EscrowGross != item.Gross {
			return fmt.Errorf("order %q escrow gross %d does not match gross %d", item.ID, item.EscrowGross, item.Gross)
		}
		if item.EscrowQty != item.Quantity {
			return fmt.Errorf("order %q escrow quantity %d does not match quantity %d", item.ID, item.EscrowQty, item.Quantity)
		}
		if item.EscrowGross < 0 || item.EscrowQty <= 0 {
			return fmt.Errorf("order %q has invalid escrow state", item.ID)
		}
		if len(item.Audit) > maxAuditLines {
			item.Audit = append([]string(nil), item.Audit[len(item.Audit)-maxAuditLines:]...)
		} else {
			item.Audit = append([]string(nil), item.Audit...)
		}
		var ok bool
		escrowTotal, ok = addInt64(escrowTotal, item.EscrowGross)
		if !ok {
			return errors.New("snapshot escrow balance overflow")
		}
		restoredOrders[item.ID] = &orderState{
			order:       order,
			status:      orderStatusPending,
			escrowGross: item.EscrowGross,
			escrowQty:   item.EscrowQty,
			audit:       item.Audit,
		}
	}
	if escrowTotal != snapshot.EscrowBalance {
		return fmt.Errorf("snapshot escrow balance %d does not match pending orders %d", snapshot.EscrowBalance, escrowTotal)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.balances = make(map[string]int64)
	g.items = make(map[string]map[string]int)
	g.orders = restoredOrders
	g.settlementCounts = make(map[string]int)
	g.keyOrders = make(map[string]string)
	g.watermark = snapshot.IdempotencyWatermark
	return nil
}

type snapshotState struct {
	Version              int             `json:"version"`
	PendingOrders        []snapshotOrder `json:"pending_orders"`
	EscrowBalance        int64           `json:"escrow_balance"`
	IdempotencyWatermark uint64          `json:"idempotency_watermark"`
}

type snapshotOrder struct {
	ID          string   `json:"id"`
	SellerID    string   `json:"seller_id"`
	BuyerID     string   `json:"buyer_id"`
	ItemID      string   `json:"item_id"`
	Quantity    int      `json:"quantity"`
	Gross       int64    `json:"gross"`
	MemberLevel int      `json:"member_level"`
	EscrowGross int64    `json:"escrow_gross"`
	EscrowQty   int      `json:"escrow_quantity"`
	Audit       []string `json:"audit,omitempty"`
}

func validateOrder(o Order) error {
	switch {
	case o.ID == "":
		return errors.New("order id is required")
	case o.SellerID == "":
		return errors.New("seller id is required")
	case o.BuyerID == "":
		return errors.New("buyer id is required")
	case o.SellerID == o.BuyerID:
		return errors.New("seller and buyer must be different players")
	case o.ItemID == "":
		return errors.New("item id is required")
	case o.Quantity <= 0:
		return fmt.Errorf("order quantity must be positive: %d", o.Quantity)
	case o.Gross < 0:
		return fmt.Errorf("order gross must not be negative: %d", o.Gross)
	case o.MemberLevel < 0:
		return fmt.Errorf("membership level must not be negative: %d", o.MemberLevel)
	}
	return nil
}

func (g *TradeSettlementGuard) feeForLocked(gross int64, memberLevel int) (int64, error) {
	if gross < 0 {
		return 0, fmt.Errorf("gross must not be negative: %d", gross)
	}
	if memberLevel < 0 {
		return 0, fmt.Errorf("membership level must not be negative: %d", memberLevel)
	}
	bps := g.feeRateBps
	if memberBps, ok := g.memberFeeRates[memberLevel]; ok {
		bps = memberBps
	}
	if gross == 0 || bps == 0 {
		return 0, nil
	}

	// Splitting the gross keeps gross*bps from overflowing while preserving
	// exact integer half-up rounding; all amounts are in cents.
	whole := gross / maxFeeRateBps
	remainder := gross % maxFeeRateBps
	fee := whole*int64(bps) + (remainder*int64(bps)+5_000)/maxFeeRateBps
	return fee, nil
}

func (g *TradeSettlementGuard) refundPendingLocked(state *orderState) error {
	buyerBalance, ok := addInt64(g.balances[state.order.BuyerID], state.escrowGross)
	if !ok {
		return errors.New("buyer wallet balance overflow")
	}
	sellerQty := g.itemCountLocked(state.order.SellerID, state.order.ItemID)
	returnedQty, ok := addInt(sellerQty, state.escrowQty)
	if !ok {
		return errors.New("seller item quantity overflow")
	}
	g.balances[state.order.BuyerID] = buyerBalance
	g.setItemCountLocked(state.order.SellerID, state.order.ItemID, returnedQty)
	return nil
}

func (g *TradeSettlementGuard) appendAuditLocked(state *orderState, line string) {
	state.audit = append(state.audit, line)
	if len(state.audit) > maxAuditLines {
		state.audit = append([]string(nil), state.audit[len(state.audit)-maxAuditLines:]...)
	}
}

func (g *TradeSettlementGuard) itemCountLocked(playerID, itemID string) int {
	return g.items[playerID][itemID]
}

func (g *TradeSettlementGuard) setItemCountLocked(playerID, itemID string, qty int) {
	if qty == 0 {
		bag := g.items[playerID]
		if bag == nil {
			return
		}
		delete(bag, itemID)
		if len(bag) == 0 {
			delete(g.items, playerID)
		}
		return
	}
	bag := g.items[playerID]
	if bag == nil {
		bag = make(map[string]int)
		g.items[playerID] = bag
	}
	bag[itemID] = qty
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func addInt(a, b int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if b > 0 && a > maxInt-b {
		return 0, false
	}
	if b < 0 && a < minInt-b {
		return 0, false
	}
	return a + b, true
}
