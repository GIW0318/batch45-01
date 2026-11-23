package task

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	task "gsb/batch45-safety-b-21"
)

func TestScanCacheConcurrentAccess(t *testing.T) {
	d := task.NewEscapeDetector(nil)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("ctr-%d", w%4)
			for i := 0; i < 500; i++ {
				gen := uint64(i % 3)
				if err := d.PutScan(id, gen, []task.Finding{{RuleID: "r", PID: i}}); err != nil {
					t.Errorf("PutScan: %v", err)
					return
				}
				if got, ok := d.GetScan(id, gen); ok && len(got) != 1 {
					t.Errorf("GetScan returned %d findings, want 1", len(got))
					return
				}
				if i%7 == 0 {
					d.InvalidateScan(id)
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestFullScanFinishesWithinBudget(t *testing.T) {
	digest := strings.Repeat("d", 64)
	d := task.NewEscapeDetector([]task.Rule{
		{ID: "r1", Severity: "critical", ExeSHA256: []string{digest}},
		{ID: "r2", Severity: "high", ExeInodes: []uint64{4242}},
		{ID: "r3", Severity: "medium", CgroupPrefix: "/kubepods/burstable"},
	})
	procs := make([]task.ProcessIdentity, 0, 20000)
	for i := 0; i < 20000; i++ {
		procs = append(procs, task.ProcessIdentity{
			PID:        1000 + i,
			PidNS:      4026532000,
			Name:       fmt.Sprintf("proc-%d", i),
			ExeInode:   uint64(100 + i),
			ExeSHA256:  strings.Repeat("e", 64),
			CgroupPath: fmt.Sprintf("/kubepods/burstable/pod%d/ctr%d", i%50, i),
		})
	}
	procs = append(procs, task.ProcessIdentity{
		PID: 999999, PidNS: 4026532000, Name: "evil",
		ExeSHA256: digest, ExeInode: 4242,
		CgroupPath: "/kubepods/burstable/pod0/ctr-evil",
	})
	start := time.Now()
	findings, err := d.Scan(procs, start)
	if err != nil {
		t.Fatalf("Scan returned unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("full scan of %d processes took %v, over the 30s budget", len(procs), elapsed)
	}
	if len(findings) == 0 {
		t.Fatalf("scan must flag the planted malicious process")
	}
}

func TestContainerChurnAlertsOncePerWindow(t *testing.T) {
	digest := strings.Repeat("f", 64)
	d := task.NewEscapeDetector([]task.Rule{{
		ID: "escape.digest", Severity: "critical", ExeSHA256: []string{digest},
	}})
	d.AlertWindow = time.Minute
	evil := task.ProcessIdentity{
		PID: 555, PidNS: 4026532000, Name: "evil",
		ExeSHA256: digest, ExeInode: 8888,
	}
	t0 := time.Unix(1700000000, 0)
	first, err := d.Scan([]task.ProcessIdentity{evil}, t0)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first sighting must alert once, got %+v", first)
	}
	// Container exits and re-enters inside the window: no duplicate alert.
	second, err := d.Scan([]task.ProcessIdentity{evil}, t0.Add(10*time.Second))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("same alert inside the window must be suppressed, got %+v", second)
	}
	// After the window expires the alert fires again.
	third, err := d.Scan([]task.ProcessIdentity{evil}, t0.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(third) != 1 {
		t.Fatalf("alert must fire again after the window, got %+v", third)
	}
}

func TestBaselineRestartDetectsNewAndMissing(t *testing.T) {
	baseline := []task.ProcessIdentity{
		{PID: 1, PidNS: 1, Root: "/", Name: "init", ExeInode: 10},
		{PID: 42, PidNS: 1, Root: "/", Name: "cron", ExeInode: 20},
	}
	d := task.NewEscapeDetector(nil)
	data, err := d.SaveBaseline(baseline)
	if err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}
	restarted := task.NewEscapeDetector(nil)
	if err := restarted.LoadBaseline(data); err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	drift, err := restarted.BaselineDrift([]task.ProcessIdentity{
		{PID: 1, PidNS: 1, Root: "/", Name: "init", ExeInode: 10},
		{PID: 43, PidNS: 1, Root: "/", Name: "kworker", ExeInode: 30},
	})
	if err != nil {
		t.Fatalf("BaselineDrift: %v", err)
	}
	var sawNew, sawMissing bool
	for _, f := range drift {
		switch f.RuleID {
		case "baseline.new-process":
			sawNew = true
		case "baseline.missing-process":
			sawMissing = true
		}
	}
	if !sawNew || !sawMissing {
		t.Fatalf("restart drift must report new and missing processes, got %+v", drift)
	}
	// A clean re-scan against the same baseline shows no drift.
	clean, err := restarted.BaselineDrift(baseline)
	if err != nil {
		t.Fatalf("BaselineDrift: %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("unchanged process set must not drift, got %+v", clean)
	}
}
