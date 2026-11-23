package task

import (
	"testing"

	impl "gsb/batch45-game-a-01"
)

func TestSortInputsOrdersByFramePlayerAndSequence(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	in := []impl.Input{
		{PlayerID: 1, Frame: 2, Seq: 0},
		{PlayerID: 0, Frame: 2, Seq: 0},
		{PlayerID: 0, Frame: 1, Seq: 3},
	}
	got := g.SortInputs(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 sorted inputs, got %d", len(got))
	}
	want := [][3]int{{1, 0, 3}, {2, 0, 0}, {2, 1, 0}}
	for i, w := range want {
		if got[i].Frame != w[0] || got[i].PlayerID != w[1] || got[i].Seq != w[2] {
			t.Fatalf("input %d out of order: got frame=%d player=%d seq=%d", i, got[i].Frame, got[i].PlayerID, got[i].Seq)
		}
	}
}

func TestMergeFrameKeepsFirstInputPerPlayer(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	got := g.MergeFrame(5, []impl.Input{
		{PlayerID: 0, Frame: 5, Seq: 1, Payload: "move"},
		{PlayerID: 0, Frame: 5, Seq: 2, Payload: "dash"},
		{PlayerID: 1, Frame: 5, Seq: 0, Payload: "attack"},
	})
	if len(got) != 2 {
		t.Fatalf("expected one merged input for each of 2 players, got %d", len(got))
	}
	if got[0].Seq != 1 {
		t.Fatalf("expected earliest sequence 1 to win for player 0, got %d", got[0].Seq)
	}
}

func TestContiguousFramesWaitsForRetransmission(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	got := g.ContiguousFrames([]int{3, 4, 5, 7, 8})
	if len(got) != 3 {
		t.Fatalf("expected to stop at the first missing frame, got %d playable frames", len(got))
	}
	for i, want := range []int{3, 4, 5} {
		if got[i] != want {
			t.Fatalf("playable frame %d = %d, want %d", i, got[i], want)
		}
	}
}

func TestFixedConversionUsesSharedScale(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	if got := g.Fixed(1.5); got != 1500 {
		t.Fatalf("fixed(1.5) = %d, want 1500 with scale 1000", got)
	}
}

func TestFixedMulKeepsPrecision(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	if got := g.FixedMul(1500, 2000); got != 3000 {
		t.Fatalf("fixed mul of 1.5 and 2.0 = %d, want 3000", got)
	}
}

func TestPrecisionIsSharedByEveryPeer(t *testing.T) {
	g := impl.NewLockstepGuard(4)
	if got := g.Precision(); got != 1000 {
		t.Fatalf("precision = %d, want the shared scale 1000", got)
	}
}

func TestSeedComesFromMatchIDNotClock(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	first := g.Seed("battle-7")
	second := g.Seed("battle-7")
	if first == 0 {
		t.Fatalf("seed must not be zero, got %d", first)
	}
	if first != second {
		t.Fatalf("same match id must produce the same seed, got %d and %d", first, second)
	}
	if first == g.Seed("battle-8") {
		t.Fatalf("different match ids must produce different seeds")
	}
}

func TestCooldownUsesLogicalFrames(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	if got := g.CooldownTicks(2, 60); got != 120 {
		t.Fatalf("2s cooldown at 60 ticks = %d frames, want 120", got)
	}
}

func TestEntityOrderIsDeterministic(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	got := g.EntityOrder([]int{30, 10, 20})
	if len(got) != 3 {
		t.Fatalf("expected 3 ordered entities, got %d", len(got))
	}
	for i, want := range []int{10, 20, 30} {
		if got[i] != want {
			t.Fatalf("entity %d = %d, want %d", i, got[i], want)
		}
	}
}

func TestReplayEntryRecordsFrameNumber(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	rec := g.ReplayEntry(7, 0x51)
	if rec.Frame != 7 {
		t.Fatalf("replay frame = %d, want 7", rec.Frame)
	}
	if rec.Checksum != 0x51 {
		t.Fatalf("replay checksum = %d, want 81", rec.Checksum)
	}
}

func TestResumeFrameIsLastSnapshot(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	if got := g.ResumeFrame(145); got != 120 {
		t.Fatalf("resume after acking frame 145 = %d, want snapshot frame 120", got)
	}
}

func TestPushInputNeverOverwritesFullBuffer(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	buf := []impl.Input{{PlayerID: 0, Frame: 1, Seq: 0, Payload: "old-a"}, {PlayerID: 1, Frame: 1, Seq: 0, Payload: "old-b"}}
	got, ok := g.PushInput(buf, 2, impl.Input{PlayerID: 0, Frame: 1, Seq: 9, Payload: "new"})
	if ok {
		t.Fatalf("pushing into a full buffer must be rejected")
	}
	if len(got) != 2 {
		t.Fatalf("full buffer must keep the 2 old inputs, got %d entries", len(got))
	}
	if got[0].Payload != "old-a" {
		t.Fatalf("old input was overwritten, payload %q", got[0].Payload)
	}
}

func TestRollingChecksumCoversIntermediateFrames(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	all := g.RollingChecksum([]uint64{11, 22, 33})
	last := g.RollingChecksum([]uint64{33})
	if all == 0 {
		t.Fatalf("rolling checksum must not be zero")
	}
	if all == last {
		t.Fatalf("checksum over all frames must differ from the checksum of the final state only")
	}
}

func TestPeerPrecisionClampedToServer(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	if got := g.NormalizePrecision(1 << 20); got != 1<<16 {
		t.Fatalf("peer scale normalised to %d, want server scale %d", got, 1<<16)
	}
}

func TestDesyncFrameFindsFirstMismatch(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	a := []uint64{1, 2, 3, 4}
	b := []uint64{1, 2, 9, 4}
	if got := g.DesyncFrame(a, b); got != 2 {
		t.Fatalf("first desync frame = %d, want 2", got)
	}
	if got := g.DesyncFrame(a, a); got != -1 {
		t.Fatalf("identical checksum streams must report -1, got %d", got)
	}
}
