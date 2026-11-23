package task

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Opcodes understood by the simulated io_uring ring.
const (
	OpNop   uint8 = 0
	OpRead  uint8 = 1
	OpWrite uint8 = 2
)

var (
	// ErrQueueFull is returned by Submit when the submission queue is full.
	ErrQueueFull = errors.New("io_uring: submit queue is full")
	// ErrUnknownRequest is returned when a user_data value is not in flight.
	ErrUnknownRequest = errors.New("io_uring: unknown request")
	// ErrDuplicateFD is returned when the same fd is registered twice.
	ErrDuplicateFD = errors.New("io_uring: fixed file already registered")
	// ErrBufferBusy is returned when a registered buffer is still owned by
	// an in-flight request.
	ErrBufferBusy = errors.New("io_uring: registered buffer still in use")
	// ErrSQPOLLDenied is returned when SQPOLL is requested without privilege.
	ErrSQPOLLDenied = errors.New("io_uring: SQPOLL requires privilege")
	// ErrStaleFD is returned when a child process uses the parent's fixed
	// file table.
	ErrStaleFD = errors.New("io_uring: fixed file table is not inherited")
	// ErrQuotaExceeded is returned when a tenant exceeds its ring quota.
	ErrQuotaExceeded = errors.New("io_uring: tenant quota exceeded")
	// ErrClosed is returned when an operation is attempted after Destroy.
	ErrClosed = errors.New("io_uring: ring is closed")
	// ErrTimeout is carried by a completion for an expired timeout request.
	ErrTimeout = errors.New("io_uring: request timed out")
	// ErrCanceled is carried by a completion for a cancelled request.
	ErrCanceled = errors.New("io_uring: request cancelled")
)

const (
	// DefaultMaxBufferBytes bounds the total registered-buffer bytes in one
	// ring when RingConfig.MaxBufferBytes is not set.
	DefaultMaxBufferBytes = 64 << 20

	userDataSeqMask = uint64(1<<32 - 1)
)

// SQE is a submission queue entry. Chain marks the entry as part of a chain:
// the next entry only runs when this one completes successfully.
type SQE struct {
	Opcode   uint8
	TenantID string
	FD       int
	BufID    int
	Offset   uint64
	Length   uint32
	Chain    bool
}

// CQE is a completion queue entry. UserData is the value returned by Submit;
// TenantID must be the tenant that owns the original request.
type CQE struct {
	UserData uint64
	TenantID string
	Seq      uint32
	Result   int32
	Err      error
}

// RingConfig configures one guard ring.
type RingConfig struct {
	Entries     int
	Buffers     int
	SQPOLL      bool
	Privileged  bool
	FailOpcodes []uint8
	TenantQuota map[string]int

	// MaxBufferBytes optionally bounds the sum of all buffer lengths
	// registered on this ring. Zero selects DefaultMaxBufferBytes.
	MaxBufferBytes int
}

type request struct {
	ud       uint64
	tenant   string
	seq      uint32
	cqe      CQE
	canceled bool
	bufID    int
}

type registeredBuffer struct {
	data []byte
	refs int
}

// IoUringGuard wraps a per-process ring and keeps tenant state separated.
type IoUringGuard struct {
	mu sync.Mutex

	cfg RingConfig

	tenantIDs   map[string]uint32
	tenantsByID map[uint32]string
	nextTenant  uint32

	// nextSeq is deliberately per tenant. The encoded user_data combines the
	// tenant id with this sequence, so equal sequences in different tenants
	// cannot alias each other.
	nextSeq map[string]uint32

	// pending contains submitted requests that have not been reaped yet.
	// Keeping them here until Reap also models CQ/SQ ownership: a completed
	// request still occupies a queue slot until its CQE is consumed.
	pending map[uint64]*request

	completions []*request
	compHead    int

	tenantInflight map[string]int

	fixedFDs    map[int]struct{}
	inheritedFD map[int]struct{}

	buffers     map[int]*registeredBuffer
	nextBufID   int
	bufferBytes int

	closed   bool
	draining bool
}

// NewIoUringGuard creates a guard ring. SQPOLL without Privileged must be
// rejected with ErrSQPOLLDenied.
func NewIoUringGuard(cfg RingConfig) (*IoUringGuard, error) {
	if cfg.SQPOLL && !cfg.Privileged {
		return nil, ErrSQPOLLDenied
	}
	if cfg.Entries <= 0 {
		cfg.Entries = 1
	}
	if cfg.MaxBufferBytes <= 0 {
		cfg.MaxBufferBytes = DefaultMaxBufferBytes
	}
	cfg.FailOpcodes = append([]uint8(nil), cfg.FailOpcodes...)
	cfg.TenantQuota = cloneQuota(cfg.TenantQuota)

	return &IoUringGuard{
		cfg:            cfg,
		tenantIDs:      make(map[string]uint32),
		tenantsByID:    make(map[uint32]string),
		nextTenant:     1,
		nextSeq:        make(map[string]uint32),
		pending:        make(map[uint64]*request),
		tenantInflight: make(map[string]int),
		fixedFDs:       make(map[int]struct{}),
		inheritedFD:    make(map[int]struct{}),
		buffers:        make(map[int]*registeredBuffer),
	}, nil
}

// EncodeUserData packs the tenant identity and a per-tenant sequence number
// into one uint64. Values must never collide across tenants.
func (g *IoUringGuard) EncodeUserData(tenant string, seq uint32) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	id := g.tenantIDLocked(tenant)
	return uint64(id)<<32 | uint64(seq)
}

// DecodeUserData is the inverse of EncodeUserData.
func (g *IoUringGuard) DecodeUserData(ud uint64) (tenant string, seq uint32, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	id := uint32(ud >> 32)
	seq = uint32(ud & userDataSeqMask)
	tenant, ok := g.tenantsByID[id]
	if id == 0 || !ok {
		return "", 0, fmt.Errorf("%w: user_data %d has no tenant owner", ErrUnknownRequest, ud)
	}
	return tenant, seq, nil
}

// Submit enqueues one SQE and returns its user_data. It must return
// ErrQueueFull instead of blocking when the ring is full.
func (g *IoUringGuard) Submit(sqe SQE) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.submitPrecheckLocked(sqe); err != nil {
		return 0, err
	}
	ud, seq := g.allocateUDLocked(sqe.TenantID)
	req := g.newRequestLocked(ud, seq, sqe)
	g.enqueueLocked(req)
	return ud, nil
}

// SubmitChain enqueues a chain. Execution stops at the first failing link and
// the links after the failure must never run.
func (g *IoUringGuard) SubmitChain(sqes []SQE) ([]uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(sqes) == 0 {
		return []uint64{}, nil
	}
	if g.closed || g.draining {
		return nil, ErrClosed
	}

	// A chain's execution is deterministic in this ring model. Find the
	// prefix that will actually run, and use it for all capacity/quota
	// accounting; the remaining links only receive user_data for correlation
	// and are never placed on the queue.
	run := g.chainRunLengthLocked(sqes)
	if len(g.pending)+run > g.cfg.Entries {
		return nil, ErrQueueFull
	}

	counts := make(map[string]int)
	for _, sqe := range sqes[:run] {
		if err := g.validateSQELocked(sqe); err != nil {
			return nil, err
		}
		counts[sqe.TenantID]++
	}
	for tenant, n := range counts {
		if g.quotaExceededLocked(tenant, n) {
			return nil, ErrQuotaExceeded
		}
	}

	uds := make([]uint64, len(sqes))
	seqs := make([]uint32, len(sqes))
	for i, sqe := range sqes {
		uds[i], seqs[i] = g.allocateUDLocked(sqe.TenantID)
	}

	for i, sqe := range sqes[:run] {
		req := g.newRequestLocked(uds[i], seqs[i], sqe)
		g.enqueueLocked(req)
	}
	return uds, nil
}

// SubmitTimeout enqueues a request that expires after d.
func (g *IoUringGuard) SubmitTimeout(tenant string, d time.Duration) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed || g.draining {
		return 0, ErrClosed
	}
	if len(g.pending) >= g.cfg.Entries {
		return 0, ErrQueueFull
	}
	if g.quotaExceededLocked(tenant, 1) {
		return 0, ErrQuotaExceeded
	}

	ud, seq := g.allocateUDLocked(tenant)
	req := &request{
		ud:     ud,
		tenant: tenant,
		seq:    seq,
		cqe: CQE{
			UserData: ud,
			TenantID: tenant,
			Seq:      seq,
			Result:   -int32(110), // -ETIMEDOUT
			Err:      ErrTimeout,
		},
	}
	// The simulated ring has no kernel timer to advance independently. A
	// timeout therefore becomes a completed CQE at submission time, ensuring
	// Reap(0) observes it and no completion or counter is ever leaked.
	_ = d
	g.enqueueLocked(req)
	return ud, nil
}

// Reap returns up to n completions. n <= 0 drains everything that is
// currently pending, including timeout completions.
func (g *IoUringGuard) Reap(n int) ([]CQE, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	available := len(g.completions) - g.compHead
	if n <= 0 || n > available {
		n = available
	}
	if n <= 0 {
		return []CQE{}, nil
	}

	out := make([]CQE, 0, n)
	for i := 0; i < n; i++ {
		req := g.completions[g.compHead]
		g.completions[g.compHead] = nil
		g.compHead++
		g.finishRequestLocked(req)
		out = append(out, req.cqe)
	}
	g.compactCompletionsLocked()
	return out, nil
}

// Cancel cancels the request identified by user_data and must match exactly
// that request; the completion of a cancelled request carries Err.
func (g *IoUringGuard) Cancel(ud uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	req, ok := g.pending[ud]
	if !ok {
		return ErrUnknownRequest
	}
	if req.canceled {
		return fmt.Errorf("%w: request already cancelled", ErrUnknownRequest)
	}
	req.canceled = true
	req.cqe.Err = ErrCanceled
	req.cqe.Result = -int32(125) // -ECANCELED
	return nil
}

// RegisterFiles installs the fixed file table for this process only.
func (g *IoUringGuard) RegisterFiles(fds []int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed || g.draining {
		return ErrClosed
	}

	seen := make(map[int]struct{}, len(fds))
	for _, fd := range fds {
		if _, exists := g.fixedFDs[fd]; exists {
			return ErrDuplicateFD
		}
		if _, dup := seen[fd]; dup {
			return ErrDuplicateFD
		}
		seen[fd] = struct{}{}
	}
	for _, fd := range fds {
		g.fixedFDs[fd] = struct{}{}
		// An explicit registration in this process supersedes the stale,
		// inherited view of the same descriptor number.
		delete(g.inheritedFD, fd)
	}
	return nil
}

// RegisterBuffers registers buffers and returns their ids.
func (g *IoUringGuard) RegisterBuffers(bufs [][]byte) ([]int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed || g.draining {
		return nil, ErrClosed
	}
	if g.cfg.Buffers > 0 && len(g.buffers)+len(bufs) > g.cfg.Buffers {
		return nil, fmt.Errorf("%w: ring buffer-slot limit %d exceeded", ErrQuotaExceeded, g.cfg.Buffers)
	}

	ids := make([]int, len(bufs))
	for i, buf := range bufs {
		g.nextBufID++
		id := g.nextBufID
		g.buffers[id] = &registeredBuffer{data: buf}
		g.bufferBytes += len(buf)
		ids[i] = id
	}
	return ids, nil
}

// ReleaseBuffer marks a buffer reusable. A buffer still referenced by an
// in-flight request must be refused with ErrBufferBusy.
func (g *IoUringGuard) ReleaseBuffer(id int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	buf, ok := g.buffers[id]
	if !ok {
		return fmt.Errorf("%w: buffer id %d", ErrUnknownRequest, id)
	}
	if buf.refs > 0 {
		return ErrBufferBusy
	}
	return nil
}

// AfterFork returns the child process view of the ring. The child must not be
// able to reach the parent fixed file table.
func (g *IoUringGuard) AfterFork() (*IoUringGuard, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed || g.draining {
		return nil, ErrClosed
	}

	child := &IoUringGuard{
		cfg:            g.cfg,
		tenantIDs:      cloneTenantIDs(g.tenantIDs),
		tenantsByID:    cloneTenantsByID(g.tenantsByID),
		nextTenant:     g.nextTenant,
		nextSeq:        cloneNextSeq(g.nextSeq),
		pending:        make(map[uint64]*request),
		tenantInflight: make(map[string]int),
		fixedFDs:       make(map[int]struct{}),
		inheritedFD:    make(map[int]struct{}),
		buffers:        make(map[int]*registeredBuffer, len(g.buffers)),
		nextBufID:      g.nextBufID,
		bufferBytes:    g.bufferBytes,
	}
	for fd := range g.fixedFDs {
		child.inheritedFD[fd] = struct{}{}
	}
	for fd := range g.inheritedFD {
		child.inheritedFD[fd] = struct{}{}
	}
	for id, buf := range g.buffers {
		// Registered memory is visible in a forked image, but references
		// owned by the parent's in-flight requests are not.
		child.buffers[id] = &registeredBuffer{data: buf.data}
	}
	return child, nil
}

// Destroy waits for all in-flight requests, drains their completions and
// leaves the ring closed.
func (g *IoUringGuard) Destroy() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed {
		return nil
	}
	g.draining = true

	// Submissions execute synchronously under this mutex, so once Destroy
	// owns the lock every previously accepted request has reached a terminal
	// state. Reaping the completion list both waits for and consumes them.
	for i := g.compHead; i < len(g.completions); i++ {
		req := g.completions[i]
		g.completions[i] = nil
		g.finishRequestLocked(req)
	}
	g.completions = nil
	g.compHead = 0

	// Defensive: a request is only absent from the completion list if the
	// accounting is already inconsistent. Drain it as well so counters cannot
	// remain positive or go negative.
	for ud, req := range g.pending {
		delete(g.pending, ud)
		g.finishRequestLocked(req)
	}

	g.draining = false
	g.closed = true
	return nil
}

// InFlight reports the number of requests not yet completed.
func (g *IoUringGuard) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pending)
}

// TenantInFlight reports the number of in-flight requests of one tenant.
func (g *IoUringGuard) TenantInFlight(tenant string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n := g.tenantInflight[tenant]; n > 0 {
		return n
	}
	return 0
}

// TenantUsage reports the quota usage of one tenant on this ring.
func (g *IoUringGuard) TenantUsage(tenant string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n := g.tenantInflight[tenant]; n > 0 {
		return n
	}
	return 0
}

// Closed reports whether Destroy already ran.
func (g *IoUringGuard) Closed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

func (g *IoUringGuard) submitPrecheckLocked(sqe SQE) error {
	if g.closed || g.draining {
		return ErrClosed
	}
	if len(g.pending) >= g.cfg.Entries {
		return ErrQueueFull
	}
	if err := g.validateSQELocked(sqe); err != nil {
		return err
	}
	if g.quotaExceededLocked(sqe.TenantID, 1) {
		return ErrQuotaExceeded
	}
	return nil
}

func (g *IoUringGuard) validateSQELocked(sqe SQE) error {
	if _, stale := g.inheritedFD[sqe.FD]; stale {
		return ErrStaleFD
	}
	if sqe.BufID > 0 {
		if _, ok := g.buffers[sqe.BufID]; !ok {
			return fmt.Errorf("%w: tenant %q buffer id %d is not registered", ErrUnknownRequest, sqe.TenantID, sqe.BufID)
		}
		if g.bufferBytes > g.cfg.MaxBufferBytes {
			return fmt.Errorf("%w: tenant %q exceeds ring registered-buffer byte budget (%d > %d)",
				ErrQuotaExceeded, sqe.TenantID, g.bufferBytes, g.cfg.MaxBufferBytes)
		}
	}
	return nil
}

func (g *IoUringGuard) quotaExceededLocked(tenant string, additional int) bool {
	limit, configured := g.cfg.TenantQuota[tenant]
	if !configured {
		return false
	}
	return g.tenantInflight[tenant]+additional > limit
}

func (g *IoUringGuard) tenantIDLocked(tenant string) uint32 {
	if id, ok := g.tenantIDs[tenant]; ok {
		return id
	}
	id := g.nextTenant
	if id == 0 {
		// 2^32 tenants cannot fit in the tenant half of user_data. Reserving
		// id zero keeps DecodeUserData able to reject unowned values.
		panic("io_uring: tenant id space exhausted")
	}
	g.nextTenant++
	g.tenantIDs[tenant] = id
	g.tenantsByID[id] = tenant
	return id
}

func (g *IoUringGuard) allocateUDLocked(tenant string) (uint64, uint32) {
	id := g.tenantIDLocked(tenant)
	seq := g.nextSeq[tenant] + 1
	if seq == 0 {
		seq = 1
	}
	ud := uint64(id)<<32 | uint64(seq)
	for {
		if _, exists := g.pending[ud]; !exists {
			break
		}
		seq++
		if seq == 0 {
			seq = 1
		}
		ud = uint64(id)<<32 | uint64(seq)
	}
	g.nextSeq[tenant] = seq
	return ud, seq
}

func (g *IoUringGuard) newRequestLocked(ud uint64, seq uint32, sqe SQE) *request {
	result := int32(0)
	var opErr error
	if g.opcodeFailsLocked(sqe.Opcode) {
		result = -int32(5) // -EIO
		opErr = fmt.Errorf("io_uring: opcode %d failed", sqe.Opcode)
	} else {
		switch sqe.Opcode {
		case OpNop:
			result = 0
		case OpRead, OpWrite:
			result = int32(sqe.Length)
		}
	}

	req := &request{
		ud:     ud,
		tenant: sqe.TenantID,
		seq:    seq,
		cqe: CQE{
			UserData: ud,
			TenantID: sqe.TenantID,
			Seq:      seq,
			Result:   result,
			Err:      opErr,
		},
		bufID: sqe.BufID,
	}
	if sqe.BufID > 0 {
		if buf := g.buffers[sqe.BufID]; buf != nil {
			buf.refs++
		}
	}
	return req
}

func (g *IoUringGuard) enqueueLocked(req *request) {
	g.pending[req.ud] = req
	g.completions = append(g.completions, req)
	g.tenantInflight[req.tenant]++
}

func (g *IoUringGuard) finishRequestLocked(req *request) {
	if current, ok := g.pending[req.ud]; ok && current == req {
		delete(g.pending, req.ud)
	}
	if n := g.tenantInflight[req.tenant]; n > 0 {
		if n == 1 {
			delete(g.tenantInflight, req.tenant)
		} else {
			g.tenantInflight[req.tenant] = n - 1
		}
	}
	if req.bufID > 0 {
		if buf := g.buffers[req.bufID]; buf != nil && buf.refs > 0 {
			buf.refs--
		}
	}
}

func (g *IoUringGuard) chainRunLengthLocked(sqes []SQE) int {
	for i, sqe := range sqes {
		if g.opcodeFailsLocked(sqe.Opcode) {
			return i + 1
		}
	}
	return len(sqes)
}

func (g *IoUringGuard) opcodeFailsLocked(opcode uint8) bool {
	for _, failing := range g.cfg.FailOpcodes {
		if opcode == failing {
			return true
		}
	}
	return false
}

func (g *IoUringGuard) compactCompletionsLocked() {
	if g.compHead == len(g.completions) {
		g.completions = nil
		g.compHead = 0
		return
	}
	if g.compHead >= 1024 && g.compHead*2 >= len(g.completions) {
		n := copy(g.completions, g.completions[g.compHead:])
		for i := n; i < len(g.completions); i++ {
			g.completions[i] = nil
		}
		g.completions = g.completions[:n]
		g.compHead = 0
	}
}

func cloneQuota(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTenantIDs(in map[string]uint32) map[string]uint32 {
	out := make(map[string]uint32, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTenantsByID(in map[uint32]string) map[uint32]string {
	out := make(map[uint32]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneNextSeq(in map[string]uint32) map[string]uint32 {
	out := make(map[string]uint32, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
