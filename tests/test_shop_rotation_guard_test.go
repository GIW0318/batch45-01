package task

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-20"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func item(id string, stock, limit int) impl.ShopItem {
	return impl.ShopItem{ID: id, Price: 100, Stock: stock, LimitPerAccount: limit}
}

func base() time.Time {
	return time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
}

func ids(items []impl.ShopItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func hasID(items []impl.ShopItem, want string) bool {
	for _, it := range items {
		if it.ID == want {
			return true
		}
	}
	return false
}

func TestRotationKeyUsesUTC(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(0))
	zoned := time.Date(2026, 5, 11, 1, 0, 0, 0, time.FixedZone("plus14", 14*3600))
	fromZoned, err := g.RotationAt(zoned)
	must(t, err)
	fromUTC, err := g.RotationAt(zoned.UTC())
	must(t, err)
	if fromZoned != fromUTC {
		t.Fatalf("rotation id %q for a zoned instant differs from the UTC id %q", fromZoned, fromUTC)
	}
}

func TestTimezoneSettingDoesNotShiftRotation(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(0))
	at := base()
	before, err := g.RotationAt(at)
	must(t, err)
	must(t, g.SetTimezone("Asia/Shanghai"))
	after, err := g.RotationAt(at)
	must(t, err)
	if before != after {
		t.Fatalf("changing the display timezone moved the rotation boundary: %q -> %q", before, after)
	}
}

func TestRefreshHourMovesBoundary(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(5))
	before := time.Date(2026, 5, 10, 4, 59, 0, 0, time.UTC)
	after := time.Date(2026, 5, 10, 5, 0, 0, 0, time.UTC)
	kb, err := g.RotationAt(before)
	must(t, err)
	ka, err := g.RotationAt(after)
	must(t, err)
	if kb == ka {
		t.Fatalf("instants around 05:00 UTC map to the same rotation %q", ka)
	}
	next, err := g.RotationAt(time.Date(2026, 5, 11, 4, 59, 0, 0, time.UTC))
	must(t, err)
	if ka != next {
		t.Fatalf("05:00 and the next 04:59 must share a rotation, got %q and %q", ka, next)
	}
}

func TestRerunRotationKeepsPurchaseLimit(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(0))
	at := base()
	items := []impl.ShopItem{item("potion", 5, 2)}
	must(t, g.Rotate(items, at))
	rotation, err := g.RotationAt(at)
	must(t, err)
	must(t, g.Purchase("acc", "char1", "potion", 1, 0, at))
	must(t, g.Rotate(items, at.Add(time.Minute)))
	if got := g.Purchased("acc", "potion", rotation); got != 1 {
		t.Fatalf("purchased = %d after a rotation rerun, want the original 1", got)
	}
	must(t, g.Purchase("acc", "char1", "potion", 1, 0, at.Add(2*time.Minute)))
	if err := g.Purchase("acc", "char2", "potion", 1, 0, at.Add(3*time.Minute)); err == nil {
		t.Fatalf("a rerun must not grant an extra purchase above the limit")
	}
}

func TestStockSharedAcrossShards(t *testing.T) {
	g := impl.NewShopRotationGuard()
	at := base()
	must(t, g.Rotate([]impl.ShopItem{item("ore", 2, 5)}, at))
	must(t, g.Purchase("acc1", "char1", "ore", 1, 0, at))
	if got := g.Stock("ore"); got != 1 {
		t.Fatalf("stock = %d after buying from shard 0, want 1", got)
	}
	if err := g.Purchase("acc2", "char2", "ore", 2, 1, at); err == nil {
		t.Fatalf("shard 1 must observe the stock already consumed by shard 0")
	}
	must(t, g.Purchase("acc2", "char2", "ore", 1, 1, at))
	if got := g.Stock("ore"); got != 0 {
		t.Fatalf("stock = %d, want 0", got)
	}
}

func TestPurchaseLimitPerAccountNotCharacter(t *testing.T) {
	g := impl.NewShopRotationGuard()
	at := base()
	must(t, g.Rotate([]impl.ShopItem{item("gem", 10, 2)}, at))
	must(t, g.Purchase("acc", "char1", "gem", 2, 0, at))
	if err := g.Purchase("acc", "char2", "gem", 1, 1, at); err == nil {
		t.Fatalf("a second character of the same account must share the purchase limit")
	}
	must(t, g.Purchase("other", "char1", "gem", 1, 0, at))
	rotation, err := g.RotationAt(at)
	must(t, err)
	if got := g.Purchased("acc", "gem", rotation); got != 2 {
		t.Fatalf("account purchased = %d, want 2", got)
	}
}

func TestDailyLimitResetsNextRotation(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(0))
	day1 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	items := []impl.ShopItem{item("ticket", 10, 1)}
	must(t, g.Rotate(items, day1))
	must(t, g.Purchase("acc", "char1", "ticket", 1, 0, day1))
	if err := g.Purchase("acc", "char1", "ticket", 1, 0, day1); err == nil {
		t.Fatalf("second purchase in the same rotation must fail")
	}
	must(t, g.Rotate(items, day2))
	must(t, g.Purchase("acc", "char1", "ticket", 1, 0, day2))
	rotation, err := g.RotationAt(day2)
	must(t, err)
	if got := g.Purchased("acc", "ticket", rotation); got != 1 {
		t.Fatalf("purchased in the new rotation = %d, want 1", got)
	}
}

func TestDelistedItemCannotBePurchasedFromCart(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(0))
	day1 := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 11, 9, 0, 0, 0, time.UTC)
	must(t, g.Rotate([]impl.ShopItem{item("hat", 5, 5)}, day1))
	must(t, g.AddToCart("acc", "hat", 1))
	delisted := item("hat", 5, 5)
	delisted.Delisted = true
	must(t, g.Rotate([]impl.ShopItem{delisted}, day2))
	if err := g.Purchase("acc", "char1", "hat", 1, 0, day2); err == nil {
		t.Fatalf("a delisted item left in the cart must not be purchasable")
	}
}

func TestFailedRotationKeepsPreviousItems(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(0))
	day1 := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 11, 9, 0, 0, 0, time.UTC)
	must(t, g.Rotate([]impl.ShopItem{item("old-a", 5, 5), item("old-b", 5, 5)}, day1))
	must(t, g.SetRotationHook(func() error { return errors.New("boom") }))
	if err := g.Rotate([]impl.ShopItem{item("new-a", 5, 5)}, day2); err == nil {
		t.Fatalf("rotation must fail when the hook fails")
	}
	current := g.CurrentItems()
	if len(current) != 2 {
		t.Fatalf("current items = %d after a failed rotation, want the previous 2", len(current))
	}
	if !hasID(current, "old-a") || !hasID(current, "old-b") {
		t.Fatalf("previous items were replaced by a failed rotation: %v", ids(current))
	}
	if got := g.RotationCount(); got != 1 {
		t.Fatalf("rotation count = %d, want 1", got)
	}
}

func TestManualRefreshAudited(t *testing.T) {
	g := impl.NewShopRotationGuard()
	at := base()
	must(t, g.Rotate([]impl.ShopItem{item("a", 5, 5)}, at))
	must(t, g.ManualRefresh("gm-alice", at.Add(time.Minute)))
	log := strings.Join(g.AuditTrail(), "\n")
	if !strings.Contains(log, "gm-alice") {
		t.Fatalf("audit trail %q does not record the manual refresh operator", log)
	}
}

func TestRotationLockExpires(t *testing.T) {
	g := impl.NewShopRotationGuard()
	at := base()
	ok, err := g.Lock("a", at, 10*time.Second)
	must(t, err)
	if !ok {
		t.Fatalf("first lock must succeed")
	}
	ok, err = g.Lock("b", at.Add(5*time.Second), 10*time.Second)
	must(t, err)
	if ok {
		t.Fatalf("lock must not be taken while another holder is inside its ttl")
	}
	ok, err = g.Lock("b", at.Add(11*time.Second), 10*time.Second)
	must(t, err)
	if !ok {
		t.Fatalf("lock must be acquirable after the ttl expired")
	}
}

func TestConcurrentRotateAppliesSingleSet(t *testing.T) {
	g := impl.NewShopRotationGuard()
	at := base()
	setA := []impl.ShopItem{item("a1", 5, 5), item("a2", 5, 5)}
	setB := []impl.ShopItem{item("b1", 5, 5), item("b2", 5, 5)}
	var wg sync.WaitGroup
	nilCount := 0
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			items := setA
			if i%2 == 1 {
				items = setB
			}
			if err := g.Rotate(items, at); err == nil {
				mu.Lock()
				nilCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if nilCount < 1 {
		t.Fatalf("no concurrent rotate call succeeded")
	}
	if got := g.RotationCount(); got != 1 {
		t.Fatalf("rotation count = %d, want exactly 1 applied window", got)
	}
	current := g.CurrentItems()
	if len(current) != 2 {
		t.Fatalf("current items = %d, want a single 2 item set", len(current))
	}
	inA := hasID(current, "a1") && hasID(current, "a2")
	inB := hasID(current, "b1") && hasID(current, "b2")
	if !inA && !inB {
		t.Fatalf("current items mix two rotations: %v", ids(current))
	}
}

func TestPurchaseAndRotateStayConsistent(t *testing.T) {
	g := impl.NewShopRotationGuard()
	must(t, g.SetRefreshHourUTC(0))
	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	items := []impl.ShopItem{item("potion", 10, 10)}
	must(t, g.Rotate(items, t0))
	must(t, g.Purchase("seed", "char1", "potion", 1, 0, t0))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			account := "acc" + itoa(i%5)
			_ = g.Purchase(account, "char1", "potion", 1, i%2, t0)
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = g.Rotate(items, t1)
	}()
	wg.Wait()
	if got := g.Stock("potion"); got < 0 {
		t.Fatalf("stock became negative: %d", got)
	}
	rotation, err := g.RotationAt(t1)
	must(t, err)
	if got := g.Purchased("acc0", "potion", rotation); got > 10 {
		t.Fatalf("account purchased %d units, above the limit", got)
	}
	if got := len(g.CurrentItems()); got != 1 {
		t.Fatalf("current items = %d, want 1 consistent item", got)
	}
}

func TestPurchaseBeyondStockRejected(t *testing.T) {
	g := impl.NewShopRotationGuard()
	at := base()
	must(t, g.Rotate([]impl.ShopItem{item("limited", 1, 5)}, at))
	if err := g.Purchase("acc", "char1", "limited", 2, 0, at); err == nil {
		t.Fatalf("buying more than the remaining stock must fail")
	}
	if got := g.Stock("limited"); got != 1 {
		t.Fatalf("stock = %d after a rejected purchase, want 1", got)
	}
	must(t, g.Purchase("acc", "char1", "limited", 1, 0, at))
	if got := g.Stock("limited"); got != 0 {
		t.Fatalf("stock = %d, want 0", got)
	}
}

func TestAuditTrailRecordsRotation(t *testing.T) {
	g := impl.NewShopRotationGuard()
	at := base()
	must(t, g.Rotate([]impl.ShopItem{item("a", 5, 5)}, at))
	rotation, err := g.RotationAt(at)
	must(t, err)
	log := strings.Join(g.AuditTrail(), "\n")
	if !strings.Contains(log, rotation) {
		t.Fatalf("audit trail %q does not mention rotation %s", log, rotation)
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
