package task

import (
	"strings"
	"testing"

	impl "gsb/batch45-game-a-09"
)

func TestPlaceEscrowsBuyerFunds(t *testing.T) {
	e := impl.NewAuctionEngine()
	err := e.Place(impl.Order{ID: "o1", ItemKey: "sword", Buyer: "alice", Price: 500, Kind: "bid", Arrived: 1})
	if err != nil {
		t.Fatalf("placing a bid failed: %v", err)
	}
	if got := e.Escrow("alice"); got != 500 {
		t.Fatalf("escrowed %d gold, want the 500 bid", got)
	}
}

func TestMatchExecutesOneTrade(t *testing.T) {
	e := impl.NewAuctionEngine()
	if err := e.Place(impl.Order{ID: "s1", ItemKey: "sword", Buyer: "seller-1", Price: 100, Kind: "sell", Arrived: 1}); err != nil {
		t.Fatalf("placing the sell order failed: %v", err)
	}
	if err := e.Place(impl.Order{ID: "b1", ItemKey: "sword", Buyer: "alice", Price: 120, Kind: "bid", Arrived: 2}); err != nil {
		t.Fatalf("placing the bid failed: %v", err)
	}
	trade, err := e.Match("sword")
	if err != nil {
		t.Fatalf("matching failed: %v", err)
	}
	if trade.Buyer != "alice" || trade.Seller != "seller-1" {
		t.Fatalf("trade = %+v, want alice buying from seller-1", trade)
	}
	if got := e.TradeCount("sword"); got != 1 {
		t.Fatalf("trade count = %d, want 1", got)
	}
}

func TestSecondMatchCannotSellAgain(t *testing.T) {
	e := impl.NewAuctionEngine()
	if err := e.Place(impl.Order{ID: "s1", ItemKey: "sword", Buyer: "seller-1", Price: 100, Kind: "sell", Arrived: 1}); err != nil {
		t.Fatalf("placing the sell order failed: %v", err)
	}
	if err := e.Place(impl.Order{ID: "b1", ItemKey: "sword", Buyer: "alice", Price: 120, Kind: "bid", Arrived: 2}); err != nil {
		t.Fatalf("placing the bid failed: %v", err)
	}
	if _, err := e.Match("sword"); err != nil {
		t.Fatalf("first match failed: %v", err)
	}
	if _, err := e.Match("sword"); err == nil {
		t.Fatalf("a matched item must not be matched a second time")
	}
	if got := e.TradeCount("sword"); got != 1 {
		t.Fatalf("trade count after a rejected second match = %d, want 1", got)
	}
}

func TestRemainingSecondsUsesServerTime(t *testing.T) {
	e := impl.NewAuctionEngine()
	if got := e.RemainingSeconds(100, 250); got != 150 {
		t.Fatalf("remaining time = %d, want 150 seconds", got)
	}
}

func TestResolvePriceKeepsBidAndBuyoutApart(t *testing.T) {
	e := impl.NewAuctionEngine()
	if got := e.ResolvePrice("bid", 100, 500); got != 100 {
		t.Fatalf("bid auction price = %d, want the bid 100", got)
	}
	if got := e.ResolvePrice("buyout", 100, 500); got != 500 {
		t.Fatalf("buyout auction price = %d, want the buyout 500", got)
	}
}

func TestOutbidNoticeNamesPreviousBuyer(t *testing.T) {
	e := impl.NewAuctionEngine()
	notice := e.OutbidNotice("alice")
	if notice == "" {
		t.Fatalf("outbid notice must not be empty")
	}
	if !strings.Contains(notice, "alice") {
		t.Fatalf("outbid notice %q must name the previous buyer alice", notice)
	}
}

func TestCancelRefundsEscrow(t *testing.T) {
	e := impl.NewAuctionEngine()
	if err := e.Place(impl.Order{ID: "o1", ItemKey: "sword", Buyer: "alice", Price: 500, Kind: "bid", Arrived: 1}); err != nil {
		t.Fatalf("placing a bid failed: %v", err)
	}
	if got := e.Cancel("o1"); got != 500 {
		t.Fatalf("refunded %d gold on cancel, want 500", got)
	}
	if got := e.Escrow("alice"); got != 0 {
		t.Fatalf("escrow after cancel = %d, want 0", got)
	}
}

func TestDuplicateBidRejected(t *testing.T) {
	e := impl.NewAuctionEngine()
	if err := e.Place(impl.Order{ID: "o1", ItemKey: "sword", Buyer: "alice", Price: 500, Kind: "bid", Arrived: 1}); err != nil {
		t.Fatalf("placing a bid failed: %v", err)
	}
	if !e.DuplicateBid("alice", "sword") {
		t.Fatalf("a repeated bid by the same buyer must be detected")
	}
	if e.DuplicateBid("bob", "sword") {
		t.Fatalf("a first bid by another buyer must not be a duplicate")
	}
}

func TestPriorityOrdersByPriceNotArrival(t *testing.T) {
	e := impl.NewAuctionEngine()
	got := e.Priority([]impl.Order{
		{ID: "o1", Price: 100, Arrived: 1},
		{ID: "o2", Price: 300, Arrived: 5},
		{ID: "o3", Price: 200, Arrived: 0},
	})
	if len(got) != 3 {
		t.Fatalf("priority list has %d orders, want 3", len(got))
	}
	want := []string{"o2", "o3", "o1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("priority order = %v, want %v (price first, arrival last resort)", got, want)
		}
	}
}

func TestCrossServerInventorySynced(t *testing.T) {
	e := impl.NewAuctionEngine()
	if got := e.SyncInventory("s2", "sword", 4); got != 4 {
		t.Fatalf("synced inventory = %d, want 4 on the remote server", got)
	}
}

func TestExpiredAuctionReturnsItemToSeller(t *testing.T) {
	e := impl.NewAuctionEngine()
	if err := e.Place(impl.Order{ID: "s1", ItemKey: "sword", Buyer: "seller-1", Price: 100, Kind: "sell", Arrived: 1}); err != nil {
		t.Fatalf("placing the sell order failed: %v", err)
	}
	seller, err := e.ExpireReturn("sword")
	if err != nil {
		t.Fatalf("expiring the auction failed: %v", err)
	}
	if seller != "seller-1" {
		t.Fatalf("expired item returned to %q, want seller-1", seller)
	}
}

func TestFeeComputedBeforeMatch(t *testing.T) {
	e := impl.NewAuctionEngine()
	if got := e.Fee(1000); got != 50 {
		t.Fatalf("fee on 1000 gold = %d, want the 5%% cut of 50", got)
	}
}

func TestRestoreReloadsUnfinishedOrders(t *testing.T) {
	e := impl.NewAuctionEngine()
	restored := e.Restore([]impl.Order{
		{ID: "o1", ItemKey: "sword", Buyer: "alice", Price: 500, Kind: "bid", Arrived: 1},
		{ID: "o2", ItemKey: "shield", Buyer: "bob", Price: 300, Kind: "bid", Arrived: 2},
	})
	if restored != 2 {
		t.Fatalf("restored %d orders after restart, want 2", restored)
	}
}

func TestHighestBidTracked(t *testing.T) {
	e := impl.NewAuctionEngine()
	if err := e.Place(impl.Order{ID: "o1", ItemKey: "sword", Buyer: "alice", Price: 500, Kind: "bid", Arrived: 1}); err != nil {
		t.Fatalf("placing alice bid failed: %v", err)
	}
	if err := e.Place(impl.Order{ID: "o2", ItemKey: "sword", Buyer: "bob", Price: 700, Kind: "bid", Arrived: 2}); err != nil {
		t.Fatalf("placing bob bid failed: %v", err)
	}
	if got := e.HighestBid("sword"); got != 700 {
		t.Fatalf("highest bid = %d, want the 700 of bob", got)
	}
}

func TestSellOncePreventsDoubleSale(t *testing.T) {
	e := impl.NewAuctionEngine()
	sold, err := e.SellOnce("sword")
	if err != nil {
		t.Fatalf("marking the item sold failed: %v", err)
	}
	if !sold {
		t.Fatalf("first sale of an item must succeed")
	}
	again, err := e.SellOnce("sword")
	if err != nil {
		t.Fatalf("second sale returned error: %v", err)
	}
	if again {
		t.Fatalf("the same item must not be sold twice")
	}
}
