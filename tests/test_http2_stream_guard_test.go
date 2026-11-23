package task

import (
	"errors"
	"testing"

	task "gsb/batch45-safety-c-01"
)

func newGuard(max uint32) *task.StreamGuard {
	return task.NewStreamGuard(task.GuardConfig{
		MaxConcurrentStreams: max,
		MaxHeaderListSize:    4096,
		InitialStreamWindow:  65535,
		InitialConnWindow:    1 << 20,
	})
}

func TestHeadersBeyondMaxConcurrentRejected(t *testing.T) {
	g := newGuard(2)
	for _, id := range []uint32{1, 3} {
		if _, err := g.HandleHeaders(id, 100, true); err != nil {
			t.Fatalf("HandleHeaders(%d) returned unexpected error: %v", id, err)
		}
	}
	if _, err := g.HandleHeaders(5, 100, true); !errors.Is(err, task.ErrMaxConcurrentStreams) {
		t.Fatalf("the third concurrent stream must be rejected, got %v", err)
	}
}

func TestSettingsApplyToExistingConn(t *testing.T) {
	g := newGuard(1)
	if _, err := g.HandleHeaders(1, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(3, 10, true); !errors.Is(err, task.ErrMaxConcurrentStreams) {
		t.Fatalf("stream 3 must be rejected while the limit is 1, got %v", err)
	}
	if err := g.HandleSettings(task.SettingsValues{MaxConcurrentStreams: 4}); err != nil {
		t.Fatalf("HandleSettings returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(3, 10, true); err != nil {
		t.Fatalf("a raised limit must apply to the existing connection, got %v", err)
	}
	if err := g.HandleSettings(task.SettingsValues{MaxConcurrentStreams: 1}); err != nil {
		t.Fatalf("HandleSettings returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(5, 10, true); !errors.Is(err, task.ErrMaxConcurrentStreams) {
		t.Fatalf("a lowered limit must apply to the existing connection, got %v", err)
	}
}

func TestRstStreamReclaimsSlotImmediately(t *testing.T) {
	g := newGuard(1)
	if _, err := g.HandleHeaders(1, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if err := g.HandleRstStream(1); err != nil {
		t.Fatalf("HandleRstStream returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(3, 10, true); err != nil {
		t.Fatalf("RST_STREAM must free the slot at once, got %v", err)
	}
}

func TestStreamIDWrapRejected(t *testing.T) {
	g := newGuard(10)
	if _, err := g.HandleHeaders(5, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(7, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(3, 10, true); !errors.Is(err, task.ErrStreamIDWrapped) {
		t.Fatalf("a lower stream id must be rejected as wrapped, got %v", err)
	}
	if _, err := g.HandleHeaders(7, 10, true); !errors.Is(err, task.ErrStreamIDWrapped) {
		t.Fatalf("reusing a stream id must be rejected, got %v", err)
	}
}

func TestAfterGoAwayNewStreamRejected(t *testing.T) {
	g := newGuard(10)
	if _, err := g.HandleHeaders(3, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if err := g.HandleGoAway(3); err != nil {
		t.Fatalf("HandleGoAway returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(5, 10, true); !errors.Is(err, task.ErrConnDraining) {
		t.Fatalf("a new stream after GOAWAY must be rejected, got %v", err)
	}
	if _, err := g.HandleData(3, 16); err != nil {
		t.Fatalf("streams below the GOAWAY id must stay usable, got %v", err)
	}
}

func TestContinuationBlockIsBounded(t *testing.T) {
	g := task.NewStreamGuard(task.GuardConfig{MaxConcurrentStreams: 4, MaxHeaderListSize: 100, InitialStreamWindow: 65535})
	if _, err := g.HandleHeaders(1, 60, false); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if err := g.HandleContinuation(1, 60); !errors.Is(err, task.ErrContinuationOverflow) {
		t.Fatalf("an unbounded header block must be rejected, got %v", err)
	}
}

func TestHeaderListSizeLimit(t *testing.T) {
	g := newGuard(4)
	if _, err := g.HandleHeaders(1, 4097, true); !errors.Is(err, task.ErrHeaderListTooLarge) {
		t.Fatalf("a header list above the limit must be rejected, got %v", err)
	}
}

func TestPriorityCycleRejected(t *testing.T) {
	g := newGuard(4)
	if err := g.HandlePriority(task.PriorityFrame{StreamID: 1, DependsOn: 3, Weight: 16}); err != nil {
		t.Fatalf("HandlePriority returned unexpected error: %v", err)
	}
	if err := g.HandlePriority(task.PriorityFrame{StreamID: 3, DependsOn: 1, Weight: 16}); !errors.Is(err, task.ErrPriorityCycle) {
		t.Fatalf("a priority dependency cycle must be rejected, got %v", err)
	}
}

func TestPerStreamFlowWindow(t *testing.T) {
	g := task.NewStreamGuard(task.GuardConfig{
		MaxConcurrentStreams: 4,
		MaxHeaderListSize:    4096,
		InitialStreamWindow:  100,
		InitialConnWindow:    10000,
	})
	if _, err := g.HandleHeaders(1, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if _, err := g.HandleData(1, 60); err != nil {
		t.Fatalf("HandleData returned unexpected error: %v", err)
	}
	if _, err := g.HandleData(1, 60); !errors.Is(err, task.ErrFlowControl) {
		t.Fatalf("the per stream window must be charged, got %v", err)
	}
}

func TestUnknownFrameNotIgnored(t *testing.T) {
	g := newGuard(4)
	if _, err := g.HandleUnknownFrame(task.RawFrame{Type: 0x0f, StreamID: 1, Length: 8}); !errors.Is(err, task.ErrUnknownFrame) {
		t.Fatalf("an unknown frame must be rejected instead of ignored, got %v", err)
	}
}

func TestPushPromiseUnlinkedStreamRejected(t *testing.T) {
	g := newGuard(4)
	if _, err := g.HandleHeaders(1, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if err := g.HandlePushPromise(1, 5); !errors.Is(err, task.ErrPushPromiseInvalid) {
		t.Fatalf("a push promise must carry an even promised id, got %v", err)
	}
	if err := g.HandlePushPromise(99, 4); !errors.Is(err, task.ErrPushPromiseInvalid) {
		t.Fatalf("a push promise must reference an open stream, got %v", err)
	}
}

func TestMigratedConnRejectsOldStreamWrites(t *testing.T) {
	g := newGuard(4)
	if _, err := g.HandleHeaders(1, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if err := g.Migrate("10.0.0.9:8443"); err != nil {
		t.Fatalf("Migrate returned unexpected error: %v", err)
	}
	if _, err := g.HandleData(1, 8); !errors.Is(err, task.ErrConnMigrated) {
		t.Fatalf("old streams must not be writable after migration, got %v", err)
	}
}

func TestDataOnClosedStreamRejected(t *testing.T) {
	g := newGuard(4)
	if _, err := g.HandleHeaders(1, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if err := g.HandleRstStream(1); err != nil {
		t.Fatalf("HandleRstStream returned unexpected error: %v", err)
	}
	if _, err := g.HandleData(1, 8); !errors.Is(err, task.ErrStreamClosed) {
		t.Fatalf("DATA on a reset stream must be rejected, got %v", err)
	}
}

func TestOpenStreamsCountAccurate(t *testing.T) {
	g := newGuard(4)
	for _, id := range []uint32{1, 3, 5} {
		if _, err := g.HandleHeaders(id, 10, true); err != nil {
			t.Fatalf("HandleHeaders(%d) returned unexpected error: %v", id, err)
		}
	}
	if err := g.HandleRstStream(3); err != nil {
		t.Fatalf("HandleRstStream returned unexpected error: %v", err)
	}
	if got := g.OpenStreams(); got != 2 {
		t.Fatalf("two streams remain open, got %d", got)
	}
}

func TestSettingsWindowAppliesToNewStreams(t *testing.T) {
	g := task.NewStreamGuard(task.GuardConfig{
		MaxConcurrentStreams: 4,
		MaxHeaderListSize:    4096,
		InitialStreamWindow:  1000,
		InitialConnWindow:    10000,
	})
	if err := g.HandleSettings(task.SettingsValues{InitialWindow: 50}); err != nil {
		t.Fatalf("HandleSettings returned unexpected error: %v", err)
	}
	if _, err := g.HandleHeaders(1, 10, true); err != nil {
		t.Fatalf("HandleHeaders returned unexpected error: %v", err)
	}
	if _, err := g.HandleData(1, 60); !errors.Is(err, task.ErrFlowControl) {
		t.Fatalf("new streams must use the settings window, got %v", err)
	}
}
