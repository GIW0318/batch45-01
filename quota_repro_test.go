package task

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReproConcurrentReservationsNeverOversell(t *testing.T) {
	e := NewProjectQuotaEnforcer()
	if err := e.AddProject(ProjectDesc{ID: 1, Limits: Limits{Bytes: 1000, Inodes: 1000}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	now := time.Unix(2_000_000, 0)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
		holds   []*Reservation
	)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.ReserveAt(1, "worker", Usage{Bytes: 10, Inodes: 1}, now, time.Minute)
			if err != nil {
				if !errors.Is(err, ErrQuotaExceeded) {
					t.Errorf("ReserveAt: %v", err)
				}
				return
			}
			mu.Lock()
			allowed++
			holds = append(holds, r)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if allowed != 100 {
		t.Fatalf("exactly 100 holds of 10 bytes fit into 1000 bytes, got %d", allowed)
	}
	used, err := e.UsageOf(1)
	if err != nil {
		t.Fatalf("UsageOf: %v", err)
	}
	if used.Bytes != 1000 || used.Inodes != 100 {
		t.Fatalf("ledger must stop exactly at the limit, got %+v", used)
	}
	for _, r := range holds {
		if err := e.CommitReservation(r); err != nil {
			t.Fatalf("CommitReservation: %v", err)
		}
	}
}

func TestReproCrashedWriterReclaimedByTimeout(t *testing.T) {
	e := NewProjectQuotaEnforcer()
	if err := e.AddProject(ProjectDesc{ID: 1, Limits: Limits{Bytes: 1000, Inodes: 100}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	base := time.Unix(2_000_000, 0)
	alive, err := e.ReserveAt(1, "alive", Usage{Bytes: 300, Inodes: 3}, base, 30*time.Second)
	if err != nil {
		t.Fatalf("ReserveAt(alive): %v", err)
	}
	crashed, err := e.ReserveAt(1, "crashed", Usage{Bytes: 400, Inodes: 4}, base, time.Minute)
	if err != nil {
		t.Fatalf("ReserveAt(crashed): %v", err)
	}
	if err := e.CommitReservation(alive); err != nil {
		t.Fatalf("CommitReservation: %v", err)
	}

	before, err := e.UsageOf(1)
	if err != nil {
		t.Fatalf("UsageOf before reap: %v", err)
	}
	if before.Bytes != 700 {
		t.Fatalf("UsageOf before crash reap = %+v, want 700 bytes", before)
	}
	if n, err := e.ExpiredReservations(crashed.ExpiresAt.Add(-time.Nanosecond)); err != nil || n != 0 {
		t.Fatalf("no reservation may be reaped before its deadline: n=%d err=%v", n, err)
	}
	if n, err := e.ExpiredReservations(crashed.ExpiresAt); err != nil || n != 1 {
		t.Fatalf("the crashed reservation must be reaped at its deadline: n=%d err=%v", n, err)
	}
	after, err := e.UsageOf(1)
	if err != nil {
		t.Fatalf("UsageOf after reap: %v", err)
	}
	if after.Bytes != 300 || after.Inodes != 3 {
		t.Fatalf("crashed writer quota must be returned, got %+v", after)
	}
	if err := e.CommitReservation(crashed); !errors.Is(err, ErrReservationGone) {
		t.Fatalf("double settle must fail with ErrReservationGone, got %v", err)
	}
	if err := e.CommitReservation(alive); !errors.Is(err, ErrReservationGone) {
		t.Fatalf("second settle must fail with ErrReservationGone, got %v", err)
	}
}

func TestReproLedgerSplitByProjectAndWriter(t *testing.T) {
	e := NewProjectQuotaEnforcer()
	if err := e.AddProject(ProjectDesc{ID: 1, Limits: Limits{Bytes: 1000, Inodes: 10}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := e.AddProject(ProjectDesc{ID: 2, Limits: Limits{Bytes: 1000, Inodes: 10}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	if _, err := e.Charge(1, "alice", Usage{Bytes: 100, Inodes: 1}); err != nil {
		t.Fatalf("Charge alice/p1: %v", err)
	}
	if _, err := e.Charge(1, "bob", Usage{Bytes: 200, Inodes: 2}); err != nil {
		t.Fatalf("Charge bob/p1: %v", err)
	}
	if _, err := e.Charge(2, "alice", Usage{Bytes: 300, Inodes: 3}); err != nil {
		t.Fatalf("Charge alice/p2: %v", err)
	}

	alice1, err := e.UserUsageOf(1, "alice")
	if err != nil {
		t.Fatalf("UserUsageOf: %v", err)
	}
	bob1, err := e.UserUsageOf(1, "bob")
	if err != nil {
		t.Fatalf("UserUsageOf: %v", err)
	}
	alice2, err := e.UserUsageOf(2, "alice")
	if err != nil {
		t.Fatalf("UserUsageOf: %v", err)
	}
	if alice1 != (Usage{Bytes: 100, Inodes: 1}) || bob1 != (Usage{Bytes: 200, Inodes: 2}) || alice2 != (Usage{Bytes: 300, Inodes: 3}) {
		t.Fatalf("per-writer ledger split is wrong: alice/p1=%+v bob/p1=%+v alice/p2=%+v", alice1, bob1, alice2)
	}
	p1, _ := e.UsageOf(1)
	p2, _ := e.UsageOf(2)
	if p1 != (Usage{Bytes: 300, Inodes: 3}) || p2 != (Usage{Bytes: 300, Inodes: 3}) {
		t.Fatalf("project ledgers leaked into each other: p1=%+v p2=%+v", p1, p2)
	}
}
