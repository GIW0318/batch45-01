package task

import (
	"math"
	"sync"
	"testing"

	task "gsb/batch45-game-b-19"
)

func testSignal(frames, channels int) []float32 {
	out := make([]float32, frames*channels)
	for f := 0; f < frames; f++ {
		for c := 0; c < channels; c++ {
			v := float64((f*37+c*101)%1009)/1009.0*2 - 1
			out[f*channels+c] = float32(v)
		}
	}
	return out
}

func runChunked(t *testing.T, r *task.AudioResampler, in []float32, frames, channels int, sizes []int) []float32 {
	t.Helper()
	out := make([]float32, 0, frames*2*channels)
	frame := 0
	chunkIndex := 0
	for frame < frames {
		n := sizes[chunkIndex%len(sizes)]
		if frame+n > frames {
			n = frames - frame
		}
		got, err := r.Resample(in[frame*channels : (frame+n)*channels])
		if err != nil {
			t.Fatalf("Resample chunk at frame %d size %d: %v", frame, n, err)
		}
		out = append(out, got...)
		frame += n
		chunkIndex++
	}
	return out
}

func requireSameSamples(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: output length %d, one-shot length %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s: sample %d differs: got %v (%08x), want %v (%08x)",
				label, i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
		}
	}
}

func TestSelfCheckChunkSeamMatchesOneShot(t *testing.T) {
	configs := []struct {
		inRate, outRate, channels int
	}{
		{44100, 48000, 1},
		{44100, 48000, 2},
		{48000, 44100, 1},
		{48000, 44100, 2},
		{16000, 48000, 1},
		{48000, 16000, 2},
		{44100, 44100, 2},
		{96000, 44100, 1},
	}
	sizes := []int{64, 441, 1024}

	const frames = 5003
	for _, cfg := range configs {
		in := testSignal(frames, cfg.channels)
		oneShot, err := task.NewAudioResampler(cfg.inRate, cfg.outRate, cfg.channels)
		if err != nil {
			t.Fatal(err)
		}
		want, err := oneShot.Resample(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range sizes {
			chunked, err := task.NewAudioResampler(cfg.inRate, cfg.outRate, cfg.channels)
			if err != nil {
				t.Fatal(err)
			}
			got := runChunked(t, chunked, in, frames, cfg.channels, []int{size})
			requireSameSamples(t, fmtConfig(cfg.inRate, cfg.outRate, cfg.channels, size), got, want)
		}
	}
}

func TestSelfCheckMixedChunkSizesMatchOneShot(t *testing.T) {
	configs := []struct {
		inRate, outRate, channels int
	}{
		{44100, 48000, 1},
		{44100, 48000, 2},
		{48000, 44100, 1},
		{48000, 44100, 2},
		{16000, 48000, 1},
		{48000, 16000, 2},
		{44100, 44100, 2},
		{96000, 44100, 1},
	}
	sizes := [][]int{
		{64, 441, 1024},
		{1024, 64, 441, 64},
		{441, 1024, 64, 1},
	}

	const frames = 12347
	for _, cfg := range configs {
		in := testSignal(frames, cfg.channels)
		oneShot, err := task.NewAudioResampler(cfg.inRate, cfg.outRate, cfg.channels)
		if err != nil {
			t.Fatal(err)
		}
		want, err := oneShot.Resample(in)
		if err != nil {
			t.Fatal(err)
		}
		for i, pattern := range sizes {
			chunked, err := task.NewAudioResampler(cfg.inRate, cfg.outRate, cfg.channels)
			if err != nil {
				t.Fatal(err)
			}
			got := runChunked(t, chunked, in, frames, cfg.channels, pattern)
			requireSameSamples(t, fmtConfig(cfg.inRate, cfg.outRate, cfg.channels, i), got, want)
		}
	}
}

func fmtConfig(inRate, outRate, channels, variant int) string {
	return "in=" + itoa(inRate) + " out=" + itoa(outRate) + " ch=" + itoa(channels) + " variant=" + itoa(variant)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestSelfCheckLongStreamNoAccumulatedDrift(t *testing.T) {
	const (
		inRate   = 44100
		outRate  = 48000
		channels = 1
		seconds  = 2 * 60 * 60
	)
	r, err := task.NewAudioResampler(inRate, outRate, channels)
	if err != nil {
		t.Fatal(err)
	}

	const chunkFrames = 1024
	totalFrames := inRate * seconds
	chunk := make([]float32, chunkFrames*channels)
	var outputFrames int64
	for frame := 0; frame < totalFrames; frame += chunkFrames {
		n := chunkFrames
		if frame+n > totalFrames {
			n = totalFrames - frame
		}
		out, err := r.Resample(chunk[:n*channels])
		if err != nil {
			t.Fatal(err)
		}
		outputFrames += int64(len(out) / channels)
	}

	wantFrames := int64(totalFrames) * outRate / inRate
	if outputFrames != wantFrames {
		t.Fatalf("after %d input frames: output frames = %d, want %d (drift %d)",
			totalFrames, outputFrames, wantFrames, outputFrames-wantFrames)
	}
	if got := r.TimestampAt(int(wantFrames)); got != int64(seconds)*1_000_000 {
		t.Fatalf("TimestampAt(%d) = %d us, want %d", wantFrames, got, int64(seconds)*1_000_000)
	}
}

func TestSelfCheckConcurrentWriteOutputRace(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.WriteOutput(1)
		}()
	}
	wg.Wait()
	if got := r.OutputLen(); got != 100 {
		t.Fatalf("OutputLen = %d, want 100", got)
	}
}

func TestSelfCheckAppendClampedCapacityGuard(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	dst := []float32{1, 2, 3, 4}
	got, err := r.AppendClamped(dst, []float32{5, 6, 7, 8}, 8)
	if err != nil {
		t.Fatalf("within capacity: %v", err)
	}
	if len(got) != 8 || cap(got) > 8 {
		t.Fatalf("got len/cap %d/%d, want len 8 and cap <= 8", len(got), cap(got))
	}
	for i, want := range []float32{1, 2, 3, 4, 5, 6, 7, 8} {
		if got[i] != want {
			t.Fatalf("got[%d] = %v, want %v", i, got[i], want)
		}
	}

	before := append([]float32(nil), dst...)
	overflowDst := make([]float32, 4, 4)
	copy(overflowDst, dst)
	if _, err := r.AppendClamped(overflowDst, []float32{5, 6, 7}, 6); err == nil {
		t.Fatal("overflow append did not return an error")
	}
	if len(overflowDst) != 4 {
		t.Fatalf("overflow changed dst length to %d", len(overflowDst))
	}
	for i := range before {
		if overflowDst[i] != before[i] {
			t.Fatalf("overflow modified dst[%d] = %v, want %v", i, overflowDst[i], before[i])
		}
	}
}

func TestSelfCheckConstructorRejectsNonPositive(t *testing.T) {
	cases := [][3]int{
		{0, 48000, 1},
		{-1, 48000, 1},
		{44100, 0, 1},
		{44100, -1, 1},
		{44100, 48000, 0},
		{44100, 48000, -1},
	}
	for _, tc := range cases {
		if _, err := task.NewAudioResampler(tc[0], tc[1], tc[2]); err == nil {
			t.Fatalf("NewAudioResampler(%d, %d, %d) did not return an error", tc[0], tc[1], tc[2])
		}
	}
}

func TestSelfCheckRebuildResetsAllState(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resample(make([]float32, 441)); err != nil {
		t.Fatal(err)
	}
	r.WriteOutput(9)
	if r.Phase() == 0 {
		t.Fatal("expected non-zero phase before rebuild")
	}

	if err := r.Rebuild(48000, 44100, 2); err != nil {
		t.Fatal(err)
	}
	if got := r.Ratio(); !near(got, 44100.0/48000.0, 1e-9) {
		t.Fatalf("Ratio after rebuild = %v", got)
	}
	if got := r.Phase(); got != 0 {
		t.Fatalf("Phase after rebuild = %d, want 0", got)
	}
	if got := r.OutputLen(); got != 0 {
		t.Fatalf("OutputLen after rebuild = %d, want 0", got)
	}

	in := testSignal(997, 2)
	rebuilt, err := r.Resample(in)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := task.NewAudioResampler(48000, 44100, 2)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fresh.Resample(in)
	if err != nil {
		t.Fatal(err)
	}
	requireSameSamples(t, "after rebuild", rebuilt, want)

	if err := r.Rebuild(0, 48000, 1); err == nil {
		t.Fatal("invalid rebuild did not return an error")
	}
	if got := r.Ratio(); !near(got, 44100.0/48000.0, 1e-9) {
		t.Fatalf("invalid rebuild changed ratio to %v", got)
	}
}

func TestSelfCheckResetBufferKeepsStreamPosition(t *testing.T) {
	r, err := task.NewAudioResampler(44100, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Resample(make([]float32, 441))
	if err != nil {
		t.Fatal(err)
	}
	phase := r.Phase()
	next := r.NextFrameIndex(480)

	if err := r.ResetBuffer(); err != nil {
		t.Fatal(err)
	}
	if got := r.Phase(); got != phase {
		t.Fatalf("Phase after ResetBuffer = %d, want %d", got, phase)
	}
	if got := r.NextFrameIndex(480); got != next {
		t.Fatalf("NextFrameIndex after ResetBuffer = %d, want %d", got, next)
	}

	second, err := r.Resample(make([]float32, 441))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 480 || len(second) != 480 {
		t.Fatalf("chunk output lengths = %d then %d, want 480 then 480", len(first), len(second))
	}
	if got := r.TimestampAt(960); got != 20_000 {
		t.Fatalf("TimestampAt(960) = %d, want 20000", got)
	}
}
