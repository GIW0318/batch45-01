package task

import (
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-14"
)

func newMacsec(t *testing.T) *MacsecGuard {
	t.Helper()
	g, err := NewMacsecGuard(make([]byte, 32), SuiteGCM128)
	if err != nil {
		t.Fatalf("NewMacsecGuard: %v", err)
	}
	return g
}

func joinPeer(t *testing.T, g *MacsecGuard, sci string, priority int, at time.Time) {
	t.Helper()
	if err := g.Join(Participant{SCI: sci, Priority: priority, Suite: SuiteGCM128, LastSeen: at}); err != nil {
		t.Fatalf("join %s: %v", sci, err)
	}
}

func TestElectionRequiresUniqueWinner(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newMacsec(t)
	if _, err := g.ElectKeyServer(); !errors.Is(err, ErrNoWinner) {
		t.Fatalf("election without peers error = %v, want ErrNoWinner", err)
	}
	joinPeer(t, g, "00:11:22:33:44:55:01", 10, now)
	joinPeer(t, g, "00:11:22:33:44:55:02", 20, now)
	winner, err := g.ElectKeyServer()
	if err != nil {
		t.Fatalf("election: %v", err)
	}
	if winner != "00:11:22:33:44:55:02" {
		t.Fatalf("winner = %q, want the higher priority peer", winner)
	}
}

func TestPriorityTieBrokenBySCI(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newMacsec(t)
	joinPeer(t, g, "00:11:22:33:44:55:ff", 100, now)
	joinPeer(t, g, "00:11:22:33:44:55:01", 100, now)
	winner, err := g.ElectKeyServer()
	if err != nil {
		t.Fatalf("election: %v", err)
	}
	if winner != "00:11:22:33:44:55:01" {
		t.Fatalf("tie winner = %q, want the smallest SCI", winner)
	}
}

func TestConcurrentElectionSingleWinner(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newMacsec(t)
	joinPeer(t, g, "00:11:22:33:44:55:01", 50, now)
	joinPeer(t, g, "00:11:22:33:44:55:02", 90, now)
	var wg sync.WaitGroup
	results := make([]string, 20)
	errs := make([]error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = g.ElectKeyServer()
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("election %d: %v", i, errs[i])
		}
		if results[i] != "00:11:22:33:44:55:02" {
			t.Fatalf("election %d winner = %q, want single winner", i, results[i])
		}
	}
}

func TestSAKInstallAtomicReplace(t *testing.T) {
	g := newMacsec(t)
	if err := g.InstallSAK("k0", make([]byte, 16), 0); err != nil {
		t.Fatalf("install AN0: %v", err)
	}
	if err := g.InstallSAK("k1", make([]byte, 16), 1); err != nil {
		t.Fatalf("install AN1: %v", err)
	}
	sak, err := g.ActiveSAK()
	if err != nil {
		t.Fatalf("active SAK: %v", err)
	}
	if sak.AN != 1 || sak.KeyID != "k1" {
		t.Fatalf("active SAK = %+v, want AN 1 / k1", sak)
	}
	if _, err := g.NextPN(0); !errors.Is(err, ErrInactiveAN) {
		t.Fatalf("old AN PN error = %v, want ErrInactiveAN", err)
	}
	if _, err := g.NextPN(1); err != nil {
		t.Fatalf("new AN PN: %v", err)
	}
}

func TestPNCountedPerSA(t *testing.T) {
	g := newMacsec(t)
	if err := g.InstallSAK("k0", make([]byte, 16), 0); err != nil {
		t.Fatalf("install AN0: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := g.NextPN(0); err != nil {
			t.Fatalf("AN0 PN %d: %v", i, err)
		}
	}
	if err := g.InstallSAK("k1", make([]byte, 16), 1); err != nil {
		t.Fatalf("install AN1: %v", err)
	}
	pn, err := g.NextPN(1)
	if err != nil {
		t.Fatalf("AN1 first PN: %v", err)
	}
	if pn != 1 {
		t.Fatalf("AN1 first PN = %d, want 1 (counter must be per SA)", pn)
	}
}

func TestPNExhaustionForcesRekey(t *testing.T) {
	g := newMacsec(t)
	if err := g.SetMaxPN(3); err != nil {
		t.Fatalf("set max PN: %v", err)
	}
	if err := g.InstallSAK("k0", make([]byte, 16), 0); err != nil {
		t.Fatalf("install SAK: %v", err)
	}
	for want := uint64(1); want <= 3; want++ {
		got, err := g.NextPN(0)
		if err != nil {
			t.Fatalf("PN %d: %v", want, err)
		}
		if got != want {
			t.Fatalf("PN = %d, want %d", got, want)
		}
	}
	if _, err := g.NextPN(0); !errors.Is(err, ErrPNExhausted) {
		t.Fatalf("PN past ceiling error = %v, want ErrPNExhausted", err)
	}
	if got := g.Counters().Rekeys; got != 1 {
		t.Fatalf("rekeys = %d, want 1 forced rekey", got)
	}
}

func TestICVFailureReported(t *testing.T) {
	g := newMacsec(t)
	sak := make([]byte, 16)
	for i := range sak {
		sak[i] = byte(i + 1)
	}
	if err := g.InstallSAK("k0", sak, 0); err != nil {
		t.Fatalf("install SAK: %v", err)
	}
	frame := []byte("macsec-frame")
	sum := sha256.Sum256(append(append([]byte{}, sak...), frame...))
	valid := sum[:16]
	if err := g.VerifyICV(0, frame, valid); err != nil {
		t.Fatalf("valid ICV rejected: %v", err)
	}
	tampered := append([]byte{}, frame...)
	tampered[0] ^= 0xff
	if err := g.VerifyICV(0, tampered, valid); !errors.Is(err, ErrICV) {
		t.Fatalf("tampered frame error = %v, want ErrICV", err)
	}
	if got := g.Counters().ICVFailures; got != 1 {
		t.Fatalf("ICV failures = %d, want 1", got)
	}
}

func TestControlFrameReplayDetected(t *testing.T) {
	g := newMacsec(t)
	if err := g.HandleControlFrame(1, []byte("a")); err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	if err := g.HandleControlFrame(5, []byte("b")); err != nil {
		t.Fatalf("frame 5: %v", err)
	}
	if err := g.HandleControlFrame(3, []byte("c")); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed frame error = %v, want ErrReplay", err)
	}
	if got := g.Counters().Replays; got != 1 {
		t.Fatalf("replays = %d, want 1", got)
	}
	if err := g.HandleControlFrame(6, []byte("d")); err != nil {
		t.Fatalf("frame 6 after replay: %v", err)
	}
}

func TestExpiredParticipantsRemoved(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newMacsec(t)
	joinPeer(t, g, "00:11:22:33:44:55:01", 100, now.Add(-2*time.Hour))
	joinPeer(t, g, "00:11:22:33:44:55:02", 10, now)
	if err := g.ExpireParticipants(now, time.Hour); err != nil {
		t.Fatalf("expire participants: %v", err)
	}
	participants := g.Participants()
	if len(participants) != 1 {
		t.Fatalf("participants = %d after expiry, want 1", len(participants))
	}
	winner, err := g.ElectKeyServer()
	if err != nil {
		t.Fatalf("election after expiry: %v", err)
	}
	if winner != "00:11:22:33:44:55:02" {
		t.Fatalf("stale participant still won the election: %q", winner)
	}
}

func TestSuiteMismatchRejected(t *testing.T) {
	g := newMacsec(t)
	if err := g.Join(Participant{SCI: "00:11:22:33:44:55:09", Priority: 1, Suite: SuiteGCM256}); !errors.Is(err, ErrSuiteMismatch) {
		t.Fatalf("mismatched suite join error = %v, want ErrSuiteMismatch", err)
	}
	if got := len(g.Participants()); got != 0 {
		t.Fatalf("participants = %d after rejected join, want 0", got)
	}
	joinPeer(t, g, "00:11:22:33:44:55:0a", 1, time.Unix(1700000000, 0))
	if got := len(g.Participants()); got != 1 {
		t.Fatalf("participants = %d after valid join, want 1", got)
	}
}

func TestConfidentialityOffset(t *testing.T) {
	g := newMacsec(t)
	if err := g.InstallSAK("k0", make([]byte, 16), 0); err != nil {
		t.Fatalf("install SAK: %v", err)
	}
	if got, err := g.ConfidentialityOffset(0); err != nil || got != 0 {
		t.Fatalf("gcm offset = %d, %v; want 0, nil", got, err)
	}
	cbc, err := NewMacsecGuard(make([]byte, 32), SuiteCBC128)
	if err != nil {
		t.Fatalf("NewMacsecGuard cbc: %v", err)
	}
	if err := cbc.InstallSAK("k0", make([]byte, 16), 0); err != nil {
		t.Fatalf("install cbc SAK: %v", err)
	}
	if got, err := cbc.ConfidentialityOffset(0); err != nil || got != 8 {
		t.Fatalf("cbc offset = %d, %v; want 8, nil", got, err)
	}
	if _, err := NewMacsecGuard(make([]byte, 32), "gcm-aes-512"); err == nil {
		t.Fatalf("unknown cipher suite was accepted")
	}
}

func TestRestartWithoutCAKRejectsSAK(t *testing.T) {
	g := newMacsec(t)
	if err := g.InstallSAK("k0", make([]byte, 16), 0); err != nil {
		t.Fatalf("install SAK: %v", err)
	}
	if _, err := g.ActiveSAK(); err != nil {
		t.Fatalf("active SAK before restart: %v", err)
	}
	if err := g.Restart(nil); !errors.Is(err, ErrCAKMissing) {
		t.Fatalf("restart without CAK error = %v, want ErrCAKMissing", err)
	}
	if _, err := g.ActiveSAK(); !errors.Is(err, ErrCAKMissing) {
		t.Fatalf("SAK usable after CAK loss: %v", err)
	}
	if err := g.Restart(make([]byte, 32)); err != nil {
		t.Fatalf("restart with CAK: %v", err)
	}
	if _, err := g.ActiveSAK(); err != nil {
		t.Fatalf("active SAK after restart with CAK: %v", err)
	}
}

func TestLeaveParticipant(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newMacsec(t)
	joinPeer(t, g, "00:11:22:33:44:55:01", 100, now)
	joinPeer(t, g, "00:11:22:33:44:55:02", 10, now)
	if err := g.Leave("00:11:22:33:44:55:01"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	winner, err := g.ElectKeyServer()
	if err != nil {
		t.Fatalf("election: %v", err)
	}
	if winner != "00:11:22:33:44:55:02" {
		t.Fatalf("left participant still elected: %q", winner)
	}
	if err := g.Leave("00:11:22:33:44:55:99"); err == nil {
		t.Fatalf("leaving an unknown participant reported success")
	}
}

func TestInstallRejectsInvalidAN(t *testing.T) {
	g := newMacsec(t)
	if err := g.InstallSAK("k4", make([]byte, 16), 4); !errors.Is(err, ErrUnknownAN) {
		t.Fatalf("AN 4 install error = %v, want ErrUnknownAN", err)
	}
	if err := g.InstallSAK("k0", nil, 0); err == nil {
		t.Fatalf("empty SAK material was accepted")
	}
	if err := g.InstallSAK("k0", make([]byte, 16), 3); err != nil {
		t.Fatalf("AN 3 install: %v", err)
	}
}

func TestElectionStableWhenLowPriorityJoins(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := newMacsec(t)
	joinPeer(t, g, "00:11:22:33:44:55:01", 100, now)
	joinPeer(t, g, "00:11:22:33:44:55:02", 200, now)
	first, err := g.ElectKeyServer()
	if err != nil {
		t.Fatalf("first election: %v", err)
	}
	joinPeer(t, g, "00:11:22:33:44:55:03", 1, now)
	second, err := g.ElectKeyServer()
	if err != nil {
		t.Fatalf("second election: %v", err)
	}
	if first != second || second != "00:11:22:33:44:55:02" {
		t.Fatalf("election changed from %q to %q after a low priority join", first, second)
	}
}
