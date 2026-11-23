package task

import (
	"strings"
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-14"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func at(min int) time.Time {
	return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute)
}

func TestFeeIsComputedOnGross(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.SetFeeRateBps(500))
	fee, err := g.FeeFor(1000, 0)
	must(t, err)
	if fee != 50 {
		t.Fatalf("fee = %d, want 5%% of the gross 1000 = 50", fee)
	}
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	s, err := g.Settle("o1", "k1", at(1))
	must(t, err)
	if s.Gross != 1000 || s.Fee != 50 || s.Net != 950 {
		t.Fatalf("settlement = %+v, want gross 1000 fee 50 net 950", s)
	}
	if got := g.Balance("seller"); got != 950 {
		t.Fatalf("seller balance = %d, want 950 after the fee", got)
	}
}

func TestFeeRoundsHalfUp(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.SetFeeRateBps(500))
	cases := []struct {
		gross int64
		want  int64
	}{
		{199, 10},
		{101, 5},
		{99, 5},
	}
	for _, tc := range cases {
		fee, err := g.FeeFor(tc.gross, 0)
		must(t, err)
		if fee != tc.want {
			t.Fatalf("fee(%d) = %d, want %d rounded half up", tc.gross, fee, tc.want)
		}
	}
}

func TestMemberLevelFeeIsLower(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.SetFeeRateBps(500))
	must(t, g.SetMemberFeeRateBps(3, 200))
	normal, err := g.FeeFor(1000, 0)
	must(t, err)
	vip, err := g.FeeFor(1000, 3)
	must(t, err)
	if vip <= 0 {
		t.Fatalf("vip fee = %d, want a positive discounted fee", vip)
	}
	if vip >= normal {
		t.Fatalf("vip fee %d is not lower than the normal fee %d", vip, normal)
	}
}

func TestSettlementMovesMoneyAndItems(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.SetFeeRateBps(500))
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 2))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 2, Gross: 1000}))
	if got := g.Balance("buyer"); got != 0 {
		t.Fatalf("buyer balance after escrow = %d, want 0", got)
	}
	if got := g.Items("seller")["sword"]; got != 0 {
		t.Fatalf("seller still holds %d swords after listing, want 0 in escrow", got)
	}
	_, err := g.Settle("o1", "k1", at(1))
	must(t, err)
	if got := g.Balance("seller"); got != 950 {
		t.Fatalf("seller balance = %d, want 950", got)
	}
	if got := g.Items("buyer")["sword"]; got != 2 {
		t.Fatalf("buyer swords = %d, want 2", got)
	}
}

func TestSettlementIsIdempotentByKey(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	first, err := g.Settle("o1", "run-key", at(1))
	must(t, err)
	second, err := g.Settle("o1", "run-key", at(2))
	must(t, err)
	if first.Net != second.Net {
		t.Fatalf("rerun produced a different settlement: %+v vs %+v", first, second)
	}
	if got := g.SettlementCount("o1"); got != 1 {
		t.Fatalf("settlement count = %d after a rerun, want 1", got)
	}
	if got := g.Balance("seller"); got != 950 {
		t.Fatalf("seller balance = %d after a rerun, want 950", got)
	}
}

func TestConcurrentSettlementCreditsOnce(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := g.Settle("o1", "same-key", at(i)); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success == 0 {
		t.Fatalf("no concurrent settle call succeeded")
	}
	if got := g.SettlementCount("o1"); got != 1 {
		t.Fatalf("settlement count = %d, want 1", got)
	}
	if got := g.Balance("seller"); got != 950 {
		t.Fatalf("seller balance = %d, want a single credit of 950", got)
	}
}

func TestCancelledOrderCannotSettle(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	must(t, g.CancelOrder("o1"))
	if _, err := g.Settle("o1", "k1", at(1)); err == nil {
		t.Fatalf("cancelled order must not settle")
	}
	if got := g.Balance("buyer"); got != 1000 {
		t.Fatalf("buyer balance = %d after cancel, want the full refund 1000", got)
	}
	if got := g.Items("seller")["sword"]; got != 1 {
		t.Fatalf("seller swords = %d after cancel, want the item returned", got)
	}
	if got := g.SettlementCount("o1"); got != 0 {
		t.Fatalf("settlement count = %d, want 0", got)
	}
}

func TestCrossServerReplayRejected(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	_, err := g.Settle("o1", "server-a-key", at(1))
	must(t, err)
	if _, err := g.Settle("o1", "server-b-key", at(2)); err == nil {
		t.Fatalf("a replay with a different key must be rejected")
	}
	if got := g.SettlementCount("o1"); got != 1 {
		t.Fatalf("settlement count = %d after the replay, want 1", got)
	}
	if got := g.Balance("seller"); got != 950 {
		t.Fatalf("seller balance = %d, want 950", got)
	}
}

func TestExpiredOrderRefundsBuyer(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	must(t, g.ExpireOrder("o1", at(60)))
	if got := g.Balance("buyer"); got != 1000 {
		t.Fatalf("buyer balance = %d after expiry, want 1000", got)
	}
	if got := g.Items("seller")["sword"]; got != 1 {
		t.Fatalf("seller swords = %d after expiry, want 1", got)
	}
	if _, err := g.Settle("o1", "k1", at(61)); err == nil {
		t.Fatalf("expired order must not settle")
	}
}

func TestExpireTwiceRefundsOnce(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	must(t, g.ExpireOrder("o1", at(60)))
	if err := g.ExpireOrder("o1", at(61)); err == nil {
		t.Fatalf("expiring an already expired order must fail")
	}
	if got := g.Balance("buyer"); got != 1000 {
		t.Fatalf("buyer balance = %d, want a single refund of 1000", got)
	}
}

func TestOrderWithoutFundsRejected(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Stock("seller", "sword", 1))
	if err := g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}); err == nil {
		t.Fatalf("creating an order without buyer funds must fail")
	}
	if got := len(g.PendingOrders()); got != 0 {
		t.Fatalf("pending orders = %d, want 0", got)
	}
	if got := g.Items("seller")["sword"]; got != 1 {
		t.Fatalf("seller swords = %d, want the item untouched", got)
	}
}

func TestAuditLogRecordsEveryStep(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 2))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	_, err := g.Settle("o1", "k1", at(1))
	must(t, err)
	log := strings.Join(g.AuditLog("o1"), "\n")
	if !strings.Contains(log, "settle") {
		t.Fatalf("audit log %q does not record the settlement", log)
	}
	must(t, g.Credit("buyer", 1000))
	must(t, g.CreateOrder(impl.Order{ID: "o2", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	must(t, g.CancelOrder("o2"))
	log2 := strings.Join(g.AuditLog("o2"), "\n")
	if !strings.Contains(log2, "cancel") {
		t.Fatalf("audit log %q does not record the cancellation", log2)
	}
}

func TestSettlementTotalsHaveNoDrift(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.SetFeeRateBps(333))
	must(t, g.Stock("seller", "ore", 250))
	amounts := make([]int64, 250)
	var grossTotal, netTotal int64
	for i := range amounts {
		gross := int64(17 + i%13)
		amounts[i] = gross
		grossTotal += gross
		fee, err := g.FeeFor(gross, 0)
		must(t, err)
		netTotal += gross - fee
	}
	must(t, g.Credit("buyer", grossTotal))
	for i, gross := range amounts {
		must(t, g.CreateOrder(impl.Order{
			ID: "o" + itoa(i), SellerID: "seller", BuyerID: "buyer",
			ItemID: "ore", Quantity: 1, Gross: gross,
		}))
	}
	for i := range amounts {
		_, err := g.Settle("o"+itoa(i), "key-"+itoa(i), at(i))
		must(t, err)
	}
	if got := g.Balance("seller"); got != netTotal {
		t.Fatalf("seller balance = %d, want the exact integer sum %d", got, netTotal)
	}
	if got := g.Items("buyer")["ore"]; got != 250 {
		t.Fatalf("buyer ore = %d, want 250", got)
	}
}

func TestSnapshotRestoreKeepsPendingOrders(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "o1", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 1000}))
	data, err := g.Snapshot()
	must(t, err)
	restored := impl.NewTradeSettlementGuard()
	must(t, restored.Restore(data))
	if got := len(restored.PendingOrders()); got != 1 {
		t.Fatalf("pending orders after restore = %d, want 1", got)
	}
	if got := restored.Balance("buyer"); got != 0 {
		t.Fatalf("restored buyer balance = %d, want the escrowed 0", got)
	}
	_, err = restored.Settle("o1", "k1", at(1))
	must(t, err)
	if got := restored.Balance("seller"); got != 950 {
		t.Fatalf("seller balance after restored settle = %d, want 950", got)
	}
}

func TestSettleUnknownOrderRejected(t *testing.T) {
	g := impl.NewTradeSettlementGuard()
	must(t, g.Credit("buyer", 1000))
	must(t, g.Stock("seller", "sword", 1))
	must(t, g.CreateOrder(impl.Order{ID: "known", SellerID: "seller", BuyerID: "buyer", ItemID: "sword", Quantity: 1, Gross: 500}))
	_, err := g.Settle("known", "k1", at(1))
	must(t, err)
	if _, err := g.Settle("missing", "k2", at(2)); err == nil {
		t.Fatalf("settling an unknown order must fail")
	}
	if got := g.SettlementCount("missing"); got != 0 {
		t.Fatalf("unknown order settlement count = %d, want 0", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
