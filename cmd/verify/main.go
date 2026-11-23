// Command verify exercises the two scenarios the unit tests cannot cover:
// concurrent programming of the same BSID/SID, and crash-restart recovery of
// the persisted SID allocation table.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	task "gsb/batch45-safety-c-07"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "VERIFY FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	dir, err := os.MkdirTemp("", "srv6-verify")
	if err != nil {
		fail("mktemp: %v", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "sid-alloc.json")

	// --- Scenario 1: concurrent programming -----------------------------
	a, err := task.LoadSIDAllocator(path)
	if err != nil {
		fail("load allocator: %v", err)
	}

	const workers = 64
	var bindOK, allocOK atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// All workers race to bind the same BSID to different policies.
			if err := a.Bind("2001:db8:ffff::1", fmt.Sprintf("policy-%d", i)); err == nil {
				bindOK.Add(1)
			}
			// All workers race to allocate the same SID.
			if err := a.Allocate("2001:db8:1::1"); err == nil {
				allocOK.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if got := bindOK.Load(); got != 1 {
		fail("concurrent BSID bind: want exactly 1 winner, got %d", got)
	}
	if got := allocOK.Load(); got != 1 {
		fail("concurrent SID allocate: want exactly 1 winner, got %d", got)
	}
	owner, ok := a.BoundPolicy("2001:db8:ffff::1")
	if !ok {
		fail("BSID binding lost after concurrent bind")
	}
	// Rebinding the winning policy is idempotent and must succeed.
	if err := a.Bind("2001:db8:ffff::1", owner); err != nil {
		fail("idempotent rebind of %q: %v", owner, err)
	}
	fmt.Printf("concurrent bind: exactly one winner (%s) among %d racers\n", owner, workers)

	// --- Scenario 2: crash / restart recovery ---------------------------
	if err := a.Allocate("2001:db8:1::2"); err != nil {
		fail("allocate sid2: %v", err)
	}
	if err := a.Retain("2001:db8:1::2"); err != nil {
		fail("retain sid2: %v", err)
	}
	if err := a.Retain("2001:db8:1::2"); err != nil {
		fail("retain sid2: %v", err)
	}
	// Simulate a crash: drop the in-memory allocator without any shutdown.
	a = nil

	b, err := task.LoadSIDAllocator(path)
	if err != nil {
		fail("reload allocator after crash: %v", err)
	}
	for _, sid := range []string{"2001:db8:1::1", "2001:db8:1::2"} {
		if !b.IsAllocated(sid) {
			fail("SID %s lost across restart", sid)
		}
		if err := b.Allocate(sid); err == nil {
			fail("SID %s re-allocated after restart", sid)
		}
	}
	if got := b.RefCount("2001:db8:1::2"); got != 3 {
		fail("refcount of sid2 after restart: want 3, got %d", got)
	}
	if _, ok := b.BoundPolicy("2001:db8:ffff::1"); !ok {
		fail("BSID binding lost across restart")
	}
	// Drain the refcount and confirm it never goes negative.
	for i := 0; i < 5; i++ {
		n, err := b.Release("2001:db8:1::2")
		if err != nil {
			fail("release sid2: %v", err)
		}
		if n < 0 {
			fail("refcount went negative: %d", n)
		}
	}
	if got := b.RefCount("2001:db8:1::2"); got != 0 {
		fail("refcount after over-release: want 0, got %d", got)
	}
	// The zero-floor must itself survive a restart.
	c, err := task.LoadSIDAllocator(path)
	if err != nil {
		fail("second reload: %v", err)
	}
	if got := c.RefCount("2001:db8:1::2"); got != 0 {
		fail("refcount after restart: want 0, got %d", got)
	}
	fmt.Println("restart recovery: allocated SIDs, refcounts and BSID bindings survived; refcount floored at 0")

	fmt.Println("VERIFY OK")
}
