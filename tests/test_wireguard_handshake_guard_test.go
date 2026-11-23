package task

import (
	"testing"
	"time"

	taskpkg "gsb/batch45-safety-a-02"
)

func newGuard() *taskpkg.HandshakeGuard {
	return taskpkg.NewHandshakeGuard(taskpkg.Config{
		HandshakeTimeout: 5 * time.Second,
		MaxRetransmits:   2,
		ReplayWindow:     64,
		IdleTimeout:      120 * time.Second,
		SharedKey:        []byte("cluster-shared-handshake-key"),
	})
}

// A retransmission must reuse the ephemeral public key and session id, or the
// responder treats every retry as a brand new session.
func TestRetransmitReusesEphemeralKey(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	first, err := g.Initiate("peer-a", now)
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	retry, err := g.Retransmit("peer-a", now.Add(time.Second))
	if err != nil {
		t.Fatalf("Retransmit: %v", err)
	}
	if retry.EphemeralPub != first.EphemeralPub {
		t.Fatalf("retransmission generated a new ephemeral key: %q -> %q", first.EphemeralPub, retry.EphemeralPub)
	}
	if retry.SessionID != first.SessionID {
		t.Fatalf("retransmission changed session id: %d -> %d", first.SessionID, retry.SessionID)
	}
}

// Repeated retransmissions must not leak additional half-open sessions.
func TestRetransmitKeepsSinglePendingSession(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	if _, err := g.Initiate("peer-a", now); err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := g.Retransmit("peer-a", now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatalf("Retransmit %d: %v", i, err)
		}
	}
	pending, err := g.PendingSessions("peer-a", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("PendingSessions: %v", err)
	}
	if pending != 1 {
		t.Fatalf("want one half-open session after retransmits, got %d", pending)
	}
}

// The replay window belongs to the key epoch and is shared by all message
// types, so the same counter cannot be replayed under another message type.
func TestReplayWindowSharedAcrossMessageTypes(t *testing.T) {
	g := newGuard()
	epoch, err := g.RotateKey("peer-a")
	if err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	ok, err := g.AcceptCounter("peer-a", epoch, 1, 77)
	if err != nil {
		t.Fatalf("AcceptCounter type 1: %v", err)
	}
	if !ok {
		t.Fatalf("fresh counter 77 rejected")
	}
	replay, err := g.AcceptCounter("peer-a", epoch, 2, 77)
	if err != nil {
		t.Fatalf("AcceptCounter type 2: %v", err)
	}
	if replay {
		t.Fatalf("counter 77 replayed under a different message type was accepted")
	}
}

// After a key switch the new epoch starts with a fresh replay window.
func TestReplayWindowResetsPerKeyEpoch(t *testing.T) {
	g := newGuard()
	epoch1, err := g.RotateKey("peer-a")
	if err != nil {
		t.Fatalf("RotateKey 1: %v", err)
	}
	if ok, err := g.AcceptCounter("peer-a", epoch1, 1, 100); err != nil || !ok {
		t.Fatalf("AcceptCounter epoch1: ok=%v err=%v", ok, err)
	}
	epoch2, err := g.RotateKey("peer-a")
	if err != nil {
		t.Fatalf("RotateKey 2: %v", err)
	}
	if epoch2 == epoch1 {
		t.Fatalf("key rotation did not change the epoch")
	}
	ok, err := g.AcceptCounter("peer-a", epoch2, 1, 100)
	if err != nil {
		t.Fatalf("AcceptCounter epoch2: %v", err)
	}
	if !ok {
		t.Fatalf("counter 100 in the new epoch was blocked by the old epoch window")
	}
}

// A cookie reply must be bound to the source address, including the port.
func TestCookieBoundToSourceAddress(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	cookie, err := g.CookieReply("peer-a", "192.0.2.10:51820", now)
	if err != nil {
		t.Fatalf("CookieReply: %v", err)
	}
	ok, err := g.VerifyCookie("peer-a", "192.0.2.10:51820", cookie, now)
	if err != nil {
		t.Fatalf("VerifyCookie same addr: %v", err)
	}
	if !ok {
		t.Fatalf("cookie rejected for the address it was minted for")
	}
	other, err := g.VerifyCookie("peer-a", "192.0.2.10:51821", cookie, now)
	if err != nil {
		t.Fatalf("VerifyCookie other port: %v", err)
	}
	if other {
		t.Fatalf("cookie minted for one port was accepted for another port")
	}
}

// Half-open state must be cleaned up once the handshake times out.
func TestHalfOpenSessionExpires(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	if _, err := g.Initiate("peer-a", now); err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	pending, err := g.PendingSessions("peer-a", now.Add(time.Second))
	if err != nil {
		t.Fatalf("PendingSessions fresh: %v", err)
	}
	if pending != 1 {
		t.Fatalf("want 1 pending session before timeout, got %d", pending)
	}
	expired, err := g.PendingSessions("peer-a", now.Add(6*time.Second))
	if err != nil {
		t.Fatalf("PendingSessions expired: %v", err)
	}
	if expired != 0 {
		t.Fatalf("timed-out half-open session was not cleaned up: %d", expired)
	}
}

// Simultaneous initiation must converge on one session instead of two.
func TestSimultaneousInitiationSingleSession(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	mine, err := g.Initiate("peer-a", now)
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	winner, err := g.RespondConcurrent("peer-a", 9001, now.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("RespondConcurrent: %v", err)
	}
	if winner != mine.SessionID {
		t.Fatalf("simultaneous initiation produced a second session: %d vs %d", winner, mine.SessionID)
	}
	active, err := g.ActiveSessions("peer-a")
	if err != nil {
		t.Fatalf("ActiveSessions: %v", err)
	}
	if active != 1 {
		t.Fatalf("want exactly one session after simultaneous initiation, got %d", active)
	}
}

// The previous key must stay usable until in-flight packets are drained.
func TestOldKeyRetainedUntilDrain(t *testing.T) {
	g := newGuard()
	epoch1, err := g.RotateKey("peer-a")
	if err != nil {
		t.Fatalf("RotateKey 1: %v", err)
	}
	epoch2, err := g.RotateKey("peer-a")
	if err != nil {
		t.Fatalf("RotateKey 2: %v", err)
	}
	if epoch2 == epoch1 {
		t.Fatalf("rotation did not create a new epoch")
	}
	ok, err := g.AcceptCounter("peer-a", epoch1, 2, 500)
	if err != nil {
		t.Fatalf("AcceptCounter old epoch: %v", err)
	}
	if !ok {
		t.Fatalf("packet on the previous epoch was dropped before draining")
	}
}

// Once the old epoch is drained, late packets using it must be rejected.
func TestRetiredEpochRejectsLatePackets(t *testing.T) {
	g := newGuard()
	epoch1, err := g.RotateKey("peer-a")
	if err != nil {
		t.Fatalf("RotateKey 1: %v", err)
	}
	if _, err := g.RotateKey("peer-a"); err != nil {
		t.Fatalf("RotateKey 2: %v", err)
	}
	if err := g.DrainKey("peer-a"); err != nil {
		t.Fatalf("DrainKey: %v", err)
	}
	if err := g.RetireKey("peer-a", epoch1); err != nil {
		t.Fatalf("RetireKey: %v", err)
	}
	ok, err := g.AcceptCounter("peer-a", epoch1, 2, 501)
	if err != nil {
		t.Fatalf("AcceptCounter retired epoch: %v", err)
	}
	if ok {
		t.Fatalf("packet on a retired epoch was accepted")
	}
}

// A keepalive reply must not reset the idle timer; only data does.
func TestKeepaliveDoesNotResetIdle(t *testing.T) {
	g := newGuard()
	base := time.Unix(1700000000, 0)
	if err := g.NoteInbound("peer-a", "data", base); err != nil {
		t.Fatalf("NoteInbound data: %v", err)
	}
	if err := g.NoteInbound("peer-a", "keepalive", base.Add(5*time.Second)); err != nil {
		t.Fatalf("NoteInbound keepalive: %v", err)
	}
	idle, err := g.IdleFor("peer-a", base.Add(10*time.Second))
	if err != nil {
		t.Fatalf("IdleFor: %v", err)
	}
	if idle != 10*time.Second {
		t.Fatalf("keepalive reset the idle timer: idle=%v want 10s", idle)
	}
}

// Retransmissions must be capped so a broken link cannot retry forever.
func TestRetransmitCapEnforced(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	if _, err := g.Initiate("peer-a", now); err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := g.Retransmit("peer-a", now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatalf("Retransmit %d within cap: %v", i, err)
		}
	}
	if _, err := g.Retransmit("peer-a", now.Add(3*time.Second)); err == nil {
		t.Fatalf("retransmit beyond MaxRetransmits was allowed")
	}
}

// The handshake MAC must cover the receiver index.
func TestMACIncludesReceiverIndex(t *testing.T) {
	g := newGuard()
	if err := g.RegisterPeer("peer-a", "cHVia2V5LWE="); err != nil {
		t.Fatalf("RegisterPeer: %v", err)
	}
	mac, err := g.ComputeMAC("peer-a", 11, 22, []byte("handshake-body"))
	if err != nil {
		t.Fatalf("ComputeMAC: %v", err)
	}
	ok, err := g.VerifyMAC("peer-a", 11, 22, []byte("handshake-body"), mac)
	if err != nil {
		t.Fatalf("VerifyMAC matching: %v", err)
	}
	if !ok {
		t.Fatalf("MAC rejected for the values it was computed over")
	}
	wrong, err := g.VerifyMAC("peer-a", 11, 23, []byte("handshake-body"), mac)
	if err != nil {
		t.Fatalf("VerifyMAC wrong receiver index: %v", err)
	}
	if wrong {
		t.Fatalf("MAC verified although the receiver index was tampered with")
	}
}

// A public key may be registered to one peer only; a second registration must
// not silently overwrite the first.
func TestDuplicatePublicKeyRejected(t *testing.T) {
	g := newGuard()
	if err := g.RegisterPeer("peer-a", "c2FtZS1wdWJsaWMta2V5"); err != nil {
		t.Fatalf("RegisterPeer peer-a: %v", err)
	}
	err := g.RegisterPeer("peer-b", "c2FtZS1wdWJsaWMta2V5")
	if err == nil {
		t.Fatalf("duplicate public key was registered for a second peer")
	}
}

// Deleting a tunnel must stop its timers.
func TestTunnelDeleteStopsTimers(t *testing.T) {
	g := newGuard()
	now := time.Unix(1700000000, 0)
	if _, err := g.Initiate("peer-a", now); err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	before, err := g.ActiveTimers()
	if err != nil {
		t.Fatalf("ActiveTimers before delete: %v", err)
	}
	if before == 0 {
		t.Fatalf("handshake timer was not armed")
	}
	if err := g.DeleteTunnel("peer-a"); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	after, err := g.ActiveTimers()
	if err != nil {
		t.Fatalf("ActiveTimers after delete: %v", err)
	}
	if after != 0 {
		t.Fatalf("timers kept running after tunnel deletion: %d", after)
	}
}

// A restart must restore sessions together with their replay windows.
func TestRestartRestoresReplayWindow(t *testing.T) {
	g := newGuard()
	epoch, err := g.RotateKey("peer-a")
	if err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	if ok, err := g.AcceptCounter("peer-a", epoch, 1, 5); err != nil || !ok {
		t.Fatalf("AcceptCounter before restart: ok=%v err=%v", ok, err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored, err := taskpkg.NewHandshakeGuardFromSnapshot(taskpkg.Config{ReplayWindow: 64}, snap)
	if err != nil {
		t.Fatalf("NewHandshakeGuardFromSnapshot: %v", err)
	}
	ok, err := restored.AcceptCounter("peer-a", epoch, 1, 5)
	if err != nil {
		t.Fatalf("AcceptCounter after restart: %v", err)
	}
	if ok {
		t.Fatalf("counter 5 was replayed successfully after restart")
	}
}
