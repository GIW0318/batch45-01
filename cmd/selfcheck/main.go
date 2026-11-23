// Command selfcheck verifies the two constraints the unit tests do not
// cover: mip buffers are allocated once per resolution (never per frame),
// and concurrent mip writes are race-free and never publish a torn frame.
// Run with: go run -race ./cmd/selfcheck
package main

import (
	"fmt"
	"os"
	"sync"

	task "gsb/batch45-game-b-09"
)

func fail(format string, args ...any) {
	fmt.Printf("SELF-CHECK FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func uniformFrame(v float64, n int) []task.RGB {
	f := make([]task.RGB, n)
	for i := range f {
		f[i] = task.RGB{R: v, G: v, B: v}
	}
	return f
}

func checkBufferReuse() {
	b := task.NewBloomChain(5)
	for i := 0; i < 4; i++ {
		if err := b.BuildMipChain(uniformFrame(1, 64*64), 64, 64); err != nil {
			fail("BuildMipChain 64x64: %v", err)
		}
	}
	if got := b.BufferAllocations(); got != 1 {
		fail("4 frames at 64x64 allocated buffers %d times, want 1", got)
	}
	if err := b.BuildMipChain(uniformFrame(1, 128*128), 128, 128); err != nil {
		fail("BuildMipChain 128x128: %v", err)
	}
	if got := b.BufferAllocations(); got != 2 {
		fail("resolution switch allocated buffers %d times, want 2", got)
	}
	for i := 0; i < 4; i++ {
		if err := b.BuildMipChain(uniformFrame(1, 128*128), 128, 128); err != nil {
			fail("BuildMipChain 128x128: %v", err)
		}
	}
	if got := b.BufferAllocations(); got != 2 {
		fail("per-frame texture allocation after scene switch: %d allocations", got)
	}
	fmt.Println("ok: buffers allocated once per resolution, reused every frame")
}

func checkConcurrentMipWrites() {
	b := task.NewBloomChain(4)
	const writers = 8
	const framesPerWriter = 200
	stop := make(chan struct{})
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			mip, err := b.ReadMip(0)
			if err != nil || len(mip) == 0 {
				continue
			}
			first := mip[0]
			for _, c := range mip {
				if c != first {
					fail("torn frame: mip holds both %v and %v", first, c)
				}
			}
		}
	}()
	var writersWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(id int) {
			defer writersWG.Done()
			v := float64(id+1) * 100
			frame := uniformFrame(v, 32*32)
			for f := 0; f < framesPerWriter; f++ {
				if err := b.BuildMipChain(frame, 32, 32); err != nil {
					fail("concurrent BuildMipChain: %v", err)
				}
			}
		}(w)
	}
	writersWG.Wait()
	close(stop)
	reader.Wait()
	fmt.Println("ok: 8 writers x 200 frames, no torn frame observed")
}

func checkConcurrentStateAccess() {
	b := task.NewBloomChain(4)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				b.AccumulateIntensity(1.0/60.0, 1)
				if err := b.AdvanceFrame(); err != nil {
					fail("AdvanceFrame: %v", err)
				}
				_ = b.AccumulatedFrame()
				_ = b.BuffersClean()
				if err := b.ClearBuffers(); err != nil {
					fail("ClearBuffers: %v", err)
				}
				if i%100 == 0 {
					if err := b.SetCamera(id); err != nil {
						fail("SetCamera: %v", err)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	fmt.Println("ok: concurrent intensity/camera/buffer access, race detector clean")
}

func main() {
	checkBufferReuse()
	checkConcurrentMipWrites()
	checkConcurrentStateAccess()
	fmt.Println("SELF-CHECK OK")
}
