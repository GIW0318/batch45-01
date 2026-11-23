package task

import (
	"errors"
	"testing"
	"time"

	. "gsb/batch45-safety-b-11"
)

func newGuard(t *testing.T, cfg RingConfig) *IoUringGuard {
	t.Helper()
	g, err := NewIoUringGuard(cfg)
	if err != nil {
		t.Fatalf("NewIoUringGuard(%+v): %v", cfg, err)
	}
	return g
}

func TestUserDataEncodesTenantAndSeq(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	ud := g.EncodeUserData("tenant-a", 7)
	tenant, seq, err := g.DecodeUserData(ud)
	if err != nil {
		t.Fatalf("DecodeUserData(%d): %v", ud, err)
	}
	if tenant != "tenant-a" || seq != 7 {
		t.Fatalf("decoded tenant=%q seq=%d, want tenant-a/7", tenant, seq)
	}
	if other := g.EncodeUserData("tenant-b", 7); other == ud {
		t.Fatalf("user_data collision across tenants: %d", ud)
	}
}

func TestCompletionCarriesOwningTenant(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	udA, err := g.Submit(SQE{TenantID: "tenant-a", Opcode: OpRead, FD: 3})
	if err != nil {
		t.Fatalf("submit tenant-a: %v", err)
	}
	udB, err := g.Submit(SQE{TenantID: "tenant-b", Opcode: OpWrite, FD: 4})
	if err != nil {
		t.Fatalf("submit tenant-b: %v", err)
	}
	cqes, err := g.Reap(0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(cqes) != 2 {
		t.Fatalf("reaped %d completions, want 2", len(cqes))
	}
	for _, c := range cqes {
		switch c.UserData {
		case udA:
			if c.TenantID != "tenant-a" {
				t.Fatalf("tenant-a request delivered to %q", c.TenantID)
			}
		case udB:
			if c.TenantID != "tenant-b" {
				t.Fatalf("tenant-b request delivered to %q", c.TenantID)
			}
		default:
			t.Fatalf("unexpected user_data %d", c.UserData)
		}
	}
}

func TestSubmitQueueFullReturnsError(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 1})
	if _, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 3}); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 3}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("second submit error = %v, want ErrQueueFull", err)
	}
	if _, err := g.Reap(1); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if _, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 3}); err != nil {
		t.Fatalf("submit after space freed: %v", err)
	}
}

func TestCancelMatchesOriginalRequest(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	ud1, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 3})
	if err != nil {
		t.Fatalf("submit first: %v", err)
	}
	ud2, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 3})
	if err != nil {
		t.Fatalf("submit second: %v", err)
	}
	if err := g.Cancel(ud1); err != nil {
		t.Fatalf("cancel %d: %v", ud1, err)
	}
	cqes, err := g.Reap(0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	var sawCanceled, sawLive bool
	for _, c := range cqes {
		switch c.UserData {
		case ud1:
			sawCanceled = c.Err != nil
		case ud2:
			sawLive = c.Err == nil
		}
	}
	if !sawCanceled {
		t.Fatalf("cancelled request %d completed without error", ud1)
	}
	if !sawLive {
		t.Fatalf("unrelated request %d was affected by the cancel", ud2)
	}
	if err := g.Cancel(1 << 40); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("cancel unknown error = %v, want ErrUnknownRequest", err)
	}
}

func TestCancelDoesNotCrossTenantBoundary(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	udA, err := g.Submit(SQE{TenantID: "tenant-a", Opcode: OpRead, FD: 3})
	if err != nil {
		t.Fatalf("submit tenant-a: %v", err)
	}
	udB, err := g.Submit(SQE{TenantID: "tenant-b", Opcode: OpRead, FD: 3})
	if err != nil {
		t.Fatalf("submit tenant-b: %v", err)
	}
	if err := g.Cancel(udA); err != nil {
		t.Fatalf("cancel tenant-a: %v", err)
	}
	cqes, err := g.Reap(0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	for _, c := range cqes {
		if c.UserData == udB && c.Err != nil {
			t.Fatalf("tenant-b request cancelled through tenant-a user_data: %v", c.Err)
		}
	}
	if g.TenantInFlight("tenant-b") != 0 {
		t.Fatalf("tenant-b still has in-flight requests after cancel of tenant-a")
	}
}

func TestFixedFilesNotInheritedAfterFork(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	if err := g.RegisterFiles([]int{10, 11}); err != nil {
		t.Fatalf("register files: %v", err)
	}
	child, err := g.AfterFork()
	if err != nil {
		t.Fatalf("after fork: %v", err)
	}
	if _, err := child.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 10}); !errors.Is(err, ErrStaleFD) {
		t.Fatalf("child submit error = %v, want ErrStaleFD", err)
	}
	if _, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 10}); err != nil {
		t.Fatalf("parent submit after fork: %v", err)
	}
}

func TestRegisteredBufferNotReusedBeforeCompletion(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8, Buffers: 1})
	ids, err := g.RegisterBuffers([][]byte{make([]byte, 32)})
	if err != nil {
		t.Fatalf("register buffers: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("got %d buffer ids, want 1", len(ids))
	}
	if _, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 3, BufID: ids[0]}); err != nil {
		t.Fatalf("submit with buffer: %v", err)
	}
	if err := g.ReleaseBuffer(ids[0]); !errors.Is(err, ErrBufferBusy) {
		t.Fatalf("release while in use = %v, want ErrBufferBusy", err)
	}
	if _, err := g.Reap(0); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if err := g.ReleaseBuffer(ids[0]); err != nil {
		t.Fatalf("release after completion: %v", err)
	}
}

func TestTimeoutCompletionDrainedNoLeak(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	ud, err := g.SubmitTimeout("tenant-a", time.Millisecond)
	if err != nil {
		t.Fatalf("submit timeout: %v", err)
	}
	cqes, err := g.Reap(0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(cqes) != 1 || cqes[0].UserData != ud {
		t.Fatalf("timeout completion = %+v, want one CQE for %d", cqes, ud)
	}
	if cqes[0].Err == nil {
		t.Fatalf("timeout completion carries no error")
	}
	if n := g.InFlight(); n != 0 {
		t.Fatalf("in-flight = %d after timeout completion, want 0", n)
	}
	if n := g.TenantInFlight("tenant-a"); n != 0 {
		t.Fatalf("tenant in-flight = %d, want 0", n)
	}
}

func TestChainStopsAfterIntermediateFailure(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8, FailOpcodes: []uint8{9}})
	uds, err := g.SubmitChain([]SQE{
		{TenantID: "t", Opcode: OpNop, Chain: true},
		{TenantID: "t", Opcode: 9, Chain: true},
		{TenantID: "t", Opcode: OpNop},
	})
	if err != nil {
		t.Fatalf("submit chain: %v", err)
	}
	if len(uds) != 3 {
		t.Fatalf("got %d user_data values, want 3", len(uds))
	}
	cqes, err := g.Reap(0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(cqes) != 2 {
		t.Fatalf("reaped %d completions, want 2 (chain must stop at the failure)", len(cqes))
	}
	for _, c := range cqes {
		if c.UserData == uds[2] {
			t.Fatalf("request after failed chain link was executed")
		}
	}
	if cqes[1].Err == nil {
		t.Fatalf("failing chain link reported no error")
	}
}

func TestDuplicateFDRegistrationRejected(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	if err := g.RegisterFiles([]int{5}); err != nil {
		t.Fatalf("register fd 5: %v", err)
	}
	if err := g.RegisterFiles([]int{5}); !errors.Is(err, ErrDuplicateFD) {
		t.Fatalf("duplicate register error = %v, want ErrDuplicateFD", err)
	}
	if err := g.RegisterFiles([]int{6}); err != nil {
		t.Fatalf("register fd 6: %v", err)
	}
}

func TestSQPOLLRequiresPrivilege(t *testing.T) {
	if _, err := NewIoUringGuard(RingConfig{Entries: 8, SQPOLL: true}); !errors.Is(err, ErrSQPOLLDenied) {
		t.Fatalf("unprivileged SQPOLL error = %v, want ErrSQPOLLDenied", err)
	}
	g, err := NewIoUringGuard(RingConfig{Entries: 8, SQPOLL: true, Privileged: true})
	if err != nil {
		t.Fatalf("privileged SQPOLL rejected: %v", err)
	}
	if g == nil {
		t.Fatalf("privileged SQPOLL returned nil guard")
	}
}

func TestCompletionsCarrySubmitSequence(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	var uds []uint64
	for i := 0; i < 3; i++ {
		ud, err := g.Submit(SQE{TenantID: "t", Opcode: OpRead, FD: 3, Offset: uint64(i)})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		uds = append(uds, ud)
		_, seq, err := g.DecodeUserData(ud)
		if err != nil {
			t.Fatalf("decode user_data %d: %v", ud, err)
		}
		if seq != uint32(i+1) {
			t.Fatalf("submit %d got seq %d, want %d", i, seq, i+1)
		}
	}
	cqes, err := g.Reap(0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	seen := map[uint64]bool{}
	for _, c := range cqes {
		seen[c.UserData] = true
	}
	for _, ud := range uds {
		if !seen[ud] {
			t.Fatalf("completion for user_data %d missing", ud)
		}
	}
}

func TestDestroyWaitsForInFlight(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	for i := 0; i < 2; i++ {
		if _, err := g.Submit(SQE{TenantID: "t", Opcode: OpWrite, FD: 3}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	if err := g.Destroy(); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if n := g.InFlight(); n != 0 {
		t.Fatalf("in-flight = %d after destroy, want 0", n)
	}
	if !g.Closed() {
		t.Fatalf("guard not marked closed after destroy")
	}
}

func TestTenantQuotaCountedPerRing(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8, TenantQuota: map[string]int{"tenant-a": 1}})
	if _, err := g.Submit(SQE{TenantID: "tenant-a", Opcode: OpRead, FD: 3}); err != nil {
		t.Fatalf("first tenant-a submit: %v", err)
	}
	if _, err := g.Submit(SQE{TenantID: "tenant-a", Opcode: OpRead, FD: 3}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("over-quota submit error = %v, want ErrQuotaExceeded", err)
	}
	if _, err := g.Submit(SQE{TenantID: "tenant-b", Opcode: OpRead, FD: 3}); err != nil {
		t.Fatalf("tenant-b submit: %v", err)
	}
	if got := g.TenantUsage("tenant-a"); got != 1 {
		t.Fatalf("tenant-a usage = %d, want 1", got)
	}
}

func TestPerTenantSequenceIsolation(t *testing.T) {
	g := newGuard(t, RingConfig{Entries: 8})
	udA, err := g.Submit(SQE{TenantID: "tenant-a", Opcode: OpRead, FD: 3})
	if err != nil {
		t.Fatalf("submit tenant-a: %v", err)
	}
	udB, err := g.Submit(SQE{TenantID: "tenant-b", Opcode: OpRead, FD: 3})
	if err != nil {
		t.Fatalf("submit tenant-b: %v", err)
	}
	tenantA, seqA, err := g.DecodeUserData(udA)
	if err != nil {
		t.Fatalf("decode tenant-a: %v", err)
	}
	tenantB, seqB, err := g.DecodeUserData(udB)
	if err != nil {
		t.Fatalf("decode tenant-b: %v", err)
	}
	if tenantA != "tenant-a" || tenantB != "tenant-b" {
		t.Fatalf("tenants decoded as %q/%q", tenantA, tenantB)
	}
	if seqA != 1 || seqB != 1 {
		t.Fatalf("first sequence per tenant = %d/%d, want 1/1", seqA, seqB)
	}
	if udA == udB {
		t.Fatalf("user_data collision between tenants: %d", udA)
	}
}
