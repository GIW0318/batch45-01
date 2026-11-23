package task

import (
	"testing"

	task "gsb/batch45-game-b-21"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestAdaptiveDepthGrowsWithLoss(t *testing.T) {
	j := task.NewJitterGuard(4)
	if got := j.AdaptiveDepth(0.25); got != 6 {
		t.Fatalf("AdaptiveDepth(0.25) with base 4 = %d, want 6", got)
	}
}

func TestAdaptiveDepthNeverBelowBase(t *testing.T) {
	j := task.NewJitterGuard(4)
	if got := j.AdaptiveDepth(0); got != 4 {
		t.Fatalf("AdaptiveDepth(0) with base 4 = %d, want 4", got)
	}
}

func TestConcealRepeatsLastFrame(t *testing.T) {
	j := task.NewJitterGuard(4)
	last := []float32{0.5, 0.5, 0.5, 0.5}
	out, err := j.Conceal(last, 4)
	if err != nil {
		t.Fatalf("Conceal returned error: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("Conceal produced %d samples, want 4", len(out))
	}
	if !near(float64(out[0]), 0.5, 1e-6) {
		t.Fatalf("first lost frame = %v, want the last frame repeated", out[0])
	}
}

func TestConcealFadesOnRepeat(t *testing.T) {
	j := task.NewJitterGuard(4)
	last := []float32{0.5, 0.5, 0.5, 0.5}
	if _, err := j.Conceal(last, 4); err != nil {
		t.Fatalf("first Conceal returned error: %v", err)
	}
	second, err := j.Conceal(last, 4)
	if err != nil {
		t.Fatalf("second Conceal returned error: %v", err)
	}
	if len(second) != 4 {
		t.Fatalf("second Conceal produced %d samples, want 4", len(second))
	}
	if !near(float64(second[0]), 0.25, 1e-6) {
		t.Fatalf("second consecutive lost frame = %v, want a faded 0.25", second[0])
	}
}

func TestProcessOrderDecodeThenPLC(t *testing.T) {
	j := task.NewJitterGuard(4)
	got := j.ProcessOrder()
	if len(got) != 2 || got[0] != "decode" || got[1] != "plc" {
		t.Fatalf("ProcessOrder = %v, want [decode plc]", got)
	}
}

func TestBufferOrdersByTimestamp(t *testing.T) {
	j := task.NewJitterGuard(4)
	for _, p := range []task.Packet{{Seq: 1, Timestamp: 300}, {Seq: 2, Timestamp: 100}, {Seq: 3, Timestamp: 200}} {
		if err := j.Push(p); err != nil {
			t.Fatalf("Push(ts=%d) returned error: %v", p.Timestamp, err)
		}
	}
	out := j.Drain(3)
	if len(out) != 3 {
		t.Fatalf("Drain(3) returned %d packets, want 3", len(out))
	}
	if out[0].Timestamp != 100 || out[1].Timestamp != 200 || out[2].Timestamp != 300 {
		t.Fatalf("Drain order = [%d %d %d], want [100 200 300]", out[0].Timestamp, out[1].Timestamp, out[2].Timestamp)
	}
}

func TestDuplicatePacketRejected(t *testing.T) {
	j := task.NewJitterGuard(4)
	p := task.Packet{Seq: 1, Timestamp: 100}
	if err := j.Push(p); err != nil {
		t.Fatalf("first Push returned error: %v", err)
	}
	if err := j.Push(p); err == nil {
		t.Fatal("duplicate packet was accepted as a new packet")
	}
}

func TestComfortNoiseDuringDTX(t *testing.T) {
	j := task.NewJitterGuard(4)
	out := j.ComfortNoise(64, 0.1)
	if len(out) != 64 {
		t.Fatalf("ComfortNoise produced %d samples, want 64", len(out))
	}
}

func TestAlignReferenceSignalDelay(t *testing.T) {
	j := task.NewJitterGuard(4)
	got := j.AlignReference([]float32{1, 2, 3}, 2)
	if len(got) != 5 {
		t.Fatalf("AlignReference produced %d samples, want 5", len(got))
	}
	if got[0] != 0 || got[1] != 0 || got[2] != 1 {
		t.Fatalf("AlignReference = %v, want two zero samples before 1", got)
	}
}

func TestDriftCorrectionPerChannel(t *testing.T) {
	j := task.NewJitterGuard(4)
	if got := j.DriftCorrection(2, 0.001); !near(got, 0.998, 1e-9) {
		t.Fatalf("DriftCorrection(channel 2, 0.001) = %v, want 0.998", got)
	}
}

func TestUnderrunRepeatsLastFrame(t *testing.T) {
	j := task.NewJitterGuard(4)
	last := []float32{0.2, 0.2, 0.2, 0.2}
	got := j.FillUnderrun(last, 8)
	if len(got) != 8 {
		t.Fatalf("FillUnderrun produced %d samples, want 8", len(got))
	}
	if !near(float64(got[4]), 0.2, 1e-6) {
		t.Fatalf("FillUnderrun went silent at index 4: %v", got[4])
	}
}

func TestOverflowEvictsOldest(t *testing.T) {
	j := task.NewJitterGuard(2)
	for _, ts := range []int64{100, 200} {
		if ev, err := j.PushOverflow(task.Packet{Timestamp: ts}); err != nil {
			t.Fatalf("PushOverflow(ts=%d) returned error: %v", ts, err)
		} else if ev != nil {
			t.Fatalf("PushOverflow evicted %+v while the buffer was not full", ev)
		}
	}
	ev, err := j.PushOverflow(task.Packet{Timestamp: 300})
	if err != nil {
		t.Fatalf("PushOverflow(ts=300) returned error: %v", err)
	}
	if ev == nil {
		t.Fatal("overflowing buffer dropped the newest packet instead of evicting the oldest")
	}
	if ev.Timestamp != 100 {
		t.Fatalf("evicted packet has timestamp %d, want the oldest 100", ev.Timestamp)
	}
}

func TestResetNetworkRebuildsDepth(t *testing.T) {
	j := task.NewJitterGuard(4)
	if err := j.ResetNetwork(6); err != nil {
		t.Fatalf("ResetNetwork returned error: %v", err)
	}
	if got := j.Depth(); got != 6 {
		t.Fatalf("Depth after ResetNetwork(6) = %d, want 6", got)
	}
}

func TestDecoderStateRoundTrip(t *testing.T) {
	j := task.NewJitterGuard(4)
	state := []byte{1, 2, 3}
	if err := j.SetDecoderState(state); err != nil {
		t.Fatalf("SetDecoderState returned error: %v", err)
	}
	got := j.DecoderState()
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("DecoderState = %v, want [1 2 3]", got)
	}
}

func TestPendingCountAfterDrain(t *testing.T) {
	j := task.NewJitterGuard(4)
	for i := 1; i <= 3; i++ {
		if err := j.Push(task.Packet{Seq: i, Timestamp: int64(i * 10)}); err != nil {
			t.Fatalf("Push returned error: %v", err)
		}
	}
	j.Drain(1)
	if got := j.Pending(); got != 2 {
		t.Fatalf("Pending after pushing 3 and draining 1 = %d, want 2", got)
	}
}
