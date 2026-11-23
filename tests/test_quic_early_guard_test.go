package task

import (
	"sync"
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-01"
)

func newGuard() *taskpkg.EarlyDataGuard {
	return taskpkg.NewEarlyDataGuard(taskpkg.Config{
		AmplificationFactor: 3,
		TicketLifetime:      10 * time.Second,
		MaxEarlyDataBytes:   4096,
		RetryTokenKey:       []byte("cluster-shared-retry-key"),
	})
}

// The client changed networks and resent the same Early Data; it must not be
// executed a second time.
func TestReplayAfterNetworkChangeRejected(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	first, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("order-42"), now)
	if err != nil {
		t.Fatalf("first OpenEarlyData: %v", err)
	}
	if !first.Accepted {
		t.Fatalf("first 0-RTT payload must be accepted, reason=%q", first.Reason)
	}
	second, err := g.OpenEarlyData("conn-b", ticket.ID, []byte("order-42"), now)
	if err != nil {
		t.Fatalf("replayed OpenEarlyData: %v", err)
	}
	if second.Accepted {
		t.Fatalf("replayed 0-RTT payload accepted on a new connection")
	}
}

// A ticket is single use even when the same connection presents it twice.
func TestTicketSequentialReuseRejected(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	if _, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("first"), now); err != nil {
		t.Fatalf("first OpenEarlyData: %v", err)
	}
	second, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("second"), now)
	if err != nil {
		t.Fatalf("second OpenEarlyData: %v", err)
	}
	if second.Accepted {
		t.Fatalf("the same ticket was accepted twice")
	}
	if second.Reason == "" {
		t.Fatalf("rejected ticket must carry a reason")
	}
}

// Two connections using one ticket at the same time: exactly one may win.
func TestConcurrentTicketUseOnlyOneWins(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	var wg sync.WaitGroup
	results := make([]taskpkg.EarlyResult, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = g.OpenEarlyData("conn-race", ticket.ID, []byte("same-order"), now)
		}(i)
	}
	wg.Wait()
	accepted := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("OpenEarlyData[%d]: %v", i, errs[i])
		}
		if results[i].Accepted {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("want exactly one accepted concurrent use, got %d", accepted)
	}
}

// The fallback (0-RTT) key must be unique per ticket so that one ticket cannot
// decrypt the data of another.
func TestFallbackKeyNotReusedAcrossTickets(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	t1, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket 1: %v", err)
	}
	t2, err := g.IssueTicket("conn-b", now)
	if err != nil {
		t.Fatalf("IssueTicket 2: %v", err)
	}
	k1, err := g.FallbackKey(t1.ID)
	if err != nil {
		t.Fatalf("FallbackKey 1: %v", err)
	}
	k2, err := g.FallbackKey(t2.ID)
	if err != nil {
		t.Fatalf("FallbackKey 2: %v", err)
	}
	if len(k1) == 0 || len(k2) == 0 {
		t.Fatalf("fallback keys must not be empty")
	}
	if string(k1) == string(k2) {
		t.Fatalf("two tickets share the same fallback key")
	}
}

// 0-RTT data must be buffered until the handshake is confirmed.
func TestEarlyDataNotDeliverableBeforeHandshakeConfirmed(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	res, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("order-42"), now)
	if err != nil {
		t.Fatalf("OpenEarlyData: %v", err)
	}
	if !res.Accepted || !res.Buffered {
		t.Fatalf("early data must be accepted and buffered, got %+v", res)
	}
	pending, err := g.Deliverable("conn-a")
	if err != nil {
		t.Fatalf("Deliverable before confirm: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("0-RTT data became visible to the application before handshake confirmation")
	}
	n, err := g.ConfirmHandshake("conn-a")
	if err != nil {
		t.Fatalf("ConfirmHandshake: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 payload released, got %d", n)
	}
	after, err := g.Deliverable("conn-a")
	if err != nil {
		t.Fatalf("Deliverable after confirm: %v", err)
	}
	if len(after) != 1 || string(after[0]) != "order-42" {
		t.Fatalf("confirmed payload not delivered: %q", after)
	}
}

// An expired ticket is rejected and the client is told to retransmit in 1-RTT
// instead of silently losing the order.
func TestExpiredTicketRejectedWithRetransmitSignal(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	late := now.Add(11 * time.Second)
	res, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("order-42"), late)
	if err != nil {
		t.Fatalf("OpenEarlyData: %v", err)
	}
	if res.Accepted {
		t.Fatalf("expired ticket was accepted")
	}
	if !res.RetransmitAfter {
		t.Fatalf("client must be told to retransmit after rejection, got %+v", res)
	}
}

// Anti-amplification must be counted in bytes, not packets.
func TestAntiAmplificationCountsBytes(t *testing.T) {
	g := newGuard()
	if err := g.RecordReceived("conn-a", 1000); err != nil {
		t.Fatalf("RecordReceived: %v", err)
	}
	ok, err := g.AllowSend("conn-a", 3000)
	if err != nil {
		t.Fatalf("AllowSend 3000: %v", err)
	}
	if !ok {
		t.Fatalf("3000 bytes must be allowed for 1000 bytes received at factor 3")
	}
	over, err := g.AllowSend("conn-a", 3001)
	if err != nil {
		t.Fatalf("AllowSend 3001: %v", err)
	}
	if over {
		t.Fatalf("byte-based anti-amplification limit exceeded at 3001 bytes")
	}
}

// A Retry token minted by one node must verify on another node of the cluster.
func TestRetryTokenVerifiableOnAnotherNode(t *testing.T) {
	cfg := taskpkg.Config{
		AmplificationFactor: 3,
		TicketLifetime:      10 * time.Second,
		RetryTokenKey:       []byte("cluster-shared-retry-key"),
	}
	nodeA := taskpkg.NewEarlyDataGuard(cfg)
	nodeB := taskpkg.NewEarlyDataGuard(cfg)
	now := time.Unix(1700000000, 0)
	token, err := nodeA.IssueRetryToken("203.0.113.9:443", now)
	if err != nil {
		t.Fatalf("IssueRetryToken: %v", err)
	}
	if token == "" {
		t.Fatalf("retry token must not be empty")
	}
	ok, err := nodeB.VerifyRetryToken(token, "203.0.113.9:443", now.Add(time.Second))
	if err != nil {
		t.Fatalf("VerifyRetryToken on node B: %v", err)
	}
	if !ok {
		t.Fatalf("token minted on node A was rejected on node B")
	}
}

// After migration the new path must be validated before its packets count.
func TestPathValidationRequiredAfterMigration(t *testing.T) {
	g := newGuard()
	if err := g.MigrateConnection("conn-a", "path-b"); err != nil {
		t.Fatalf("MigrateConnection: %v", err)
	}
	ok, err := g.AcceptPathPacket("conn-a", "path-b")
	if err != nil {
		t.Fatalf("AcceptPathPacket before validation: %v", err)
	}
	if ok {
		t.Fatalf("packets accepted on an unvalidated path after migration")
	}
	if err := g.ValidatePath("conn-a", "path-b"); err != nil {
		t.Fatalf("ValidatePath: %v", err)
	}
	ok, err = g.AcceptPathPacket("conn-a", "path-b")
	if err != nil {
		t.Fatalf("AcceptPathPacket after validation: %v", err)
	}
	if !ok {
		t.Fatalf("validated path still rejected")
	}
}

// Packets that arrive on the abandoned path must not be written into the
// migrated connection.
func TestOldPathPacketsRejectedAfterMigration(t *testing.T) {
	g := newGuard()
	if err := g.MigrateConnection("conn-a", "path-b"); err != nil {
		t.Fatalf("MigrateConnection: %v", err)
	}
	if err := g.ValidatePath("conn-a", "path-b"); err != nil {
		t.Fatalf("ValidatePath: %v", err)
	}
	ok, err := g.AcceptPathPacket("conn-a", "path-a")
	if err != nil {
		t.Fatalf("AcceptPathPacket on old path: %v", err)
	}
	if ok {
		t.Fatalf("packet from the old path was accepted after migration")
	}
}

// Retiring the key that sealed a ticket must invalidate that ticket.
func TestRetiredTicketKeyRejectsOldTicket(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	newKey, err := g.RotateTicketKey()
	if err != nil {
		t.Fatalf("RotateTicketKey: %v", err)
	}
	if newKey == "" {
		t.Fatalf("rotated key id must not be empty")
	}
	if err := g.RetireTicketKey(ticket.KeyID); err != nil {
		t.Fatalf("RetireTicketKey: %v", err)
	}
	res, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("order-42"), now)
	if err != nil {
		t.Fatalf("OpenEarlyData: %v", err)
	}
	if res.Accepted {
		t.Fatalf("ticket sealed with a retired key was still accepted")
	}
}

// 0-RTT and 1-RTT stream allocations on one connection must not collide.
func TestEarlyAndOneRTTStreamIDsDistinct(t *testing.T) {
	g := newGuard()
	seen := make(map[uint64]bool)
	for i := 0; i < 3; i++ {
		id, err := g.AllocateStream("conn-a", true)
		if err != nil {
			t.Fatalf("AllocateStream early: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate stream id %d", id)
		}
		seen[id] = true
	}
	for i := 0; i < 3; i++ {
		id, err := g.AllocateStream("conn-a", false)
		if err != nil {
			t.Fatalf("AllocateStream 1-RTT: %v", err)
		}
		if seen[id] {
			t.Fatalf("stream id %d collides with an earlier allocation", id)
		}
		seen[id] = true
	}
}

// 0-RTT packets must not consume the congestion window.
func TestZeroRTTBytesExcludedFromCongestionWindow(t *testing.T) {
	g := newGuard()
	if err := g.AccountPacket("conn-a", 1200, true); err != nil {
		t.Fatalf("AccountPacket early: %v", err)
	}
	if err := g.AccountPacket("conn-a", 1200, true); err != nil {
		t.Fatalf("AccountPacket early: %v", err)
	}
	inFlight, err := g.CongestionBytes("conn-a")
	if err != nil {
		t.Fatalf("CongestionBytes: %v", err)
	}
	if inFlight != 0 {
		t.Fatalf("0-RTT bytes counted against congestion window: got %d", inFlight)
	}
	if err := g.AccountPacket("conn-a", 1200, false); err != nil {
		t.Fatalf("AccountPacket 1-RTT: %v", err)
	}
	inFlight, err = g.CongestionBytes("conn-a")
	if err != nil {
		t.Fatalf("CongestionBytes: %v", err)
	}
	if inFlight != 1200 {
		t.Fatalf("1-RTT bytes must be counted, got %d", inFlight)
	}
}

// Closing the connection must invalidate tickets that were not yet used.
func TestClosedConnectionInvalidatesTicket(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	if err := g.CloseConnection("conn-a"); err != nil {
		t.Fatalf("CloseConnection: %v", err)
	}
	res, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("order-42"), now)
	if err != nil {
		t.Fatalf("OpenEarlyData after close: %v", err)
	}
	if res.Accepted {
		t.Fatalf("ticket of a closed connection was still accepted")
	}
}

// A restarted node must keep rejecting replayed 0-RTT data.
func TestReplayCacheSurvivesRestart(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	ticket, err := g.IssueTicket("conn-a", now)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	first, err := g.OpenEarlyData("conn-a", ticket.ID, []byte("order-42"), now)
	if err != nil {
		t.Fatalf("OpenEarlyData: %v", err)
	}
	if !first.Accepted {
		t.Fatalf("first use must be accepted, reason=%q", first.Reason)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored, err := taskpkg.NewEarlyDataGuardFromSnapshot(taskpkg.Config{TicketLifetime: 10 * time.Second}, snap)
	if err != nil {
		t.Fatalf("NewEarlyDataGuardFromSnapshot: %v", err)
	}
	replay, err := restored.OpenEarlyData("conn-c", ticket.ID, []byte("order-42"), now)
	if err != nil {
		t.Fatalf("OpenEarlyData after restart: %v", err)
	}
	if replay.Accepted {
		t.Fatalf("replayed 0-RTT data accepted after cluster restart")
	}
}
