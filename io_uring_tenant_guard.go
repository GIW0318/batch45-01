package task

import (
	"errors"
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
}

// IoUringGuard wraps a per-process ring and keeps tenant state separated.
type IoUringGuard struct{}

// NewIoUringGuard creates a guard ring. SQPOLL without Privileged must be
// rejected with ErrSQPOLLDenied.
func NewIoUringGuard(cfg RingConfig) (*IoUringGuard, error) {
	return nil, errors.New("not implemented")
}

// EncodeUserData packs the tenant identity and a per-tenant sequence number
// into one uint64. Values must never collide across tenants.
func (g *IoUringGuard) EncodeUserData(tenant string, seq uint32) uint64 {
	return 0
}

// DecodeUserData is the inverse of EncodeUserData.
func (g *IoUringGuard) DecodeUserData(ud uint64) (tenant string, seq uint32, err error) {
	return "", 0, errors.New("not implemented")
}

// Submit enqueues one SQE and returns its user_data. It must return
// ErrQueueFull instead of blocking when the ring is full.
func (g *IoUringGuard) Submit(sqe SQE) (uint64, error) {
	return 0, errors.New("not implemented")
}

// SubmitChain enqueues a chain. Execution stops at the first failing link and
// the links after the failure must never run.
func (g *IoUringGuard) SubmitChain(sqes []SQE) ([]uint64, error) {
	return nil, errors.New("not implemented")
}

// SubmitTimeout enqueues a request that expires after d.
func (g *IoUringGuard) SubmitTimeout(tenant string, d time.Duration) (uint64, error) {
	return 0, errors.New("not implemented")
}

// Reap returns up to n completions. n <= 0 drains everything that is
// currently pending, including timeout completions.
func (g *IoUringGuard) Reap(n int) ([]CQE, error) {
	return nil, errors.New("not implemented")
}

// Cancel cancels the request identified by user_data and must match exactly
// that request; the completion of a cancelled request carries Err.
func (g *IoUringGuard) Cancel(ud uint64) error {
	return errors.New("not implemented")
}

// RegisterFiles installs the fixed file table for this process only.
func (g *IoUringGuard) RegisterFiles(fds []int) error {
	return errors.New("not implemented")
}

// RegisterBuffers registers buffers and returns their ids.
func (g *IoUringGuard) RegisterBuffers(bufs [][]byte) ([]int, error) {
	return nil, errors.New("not implemented")
}

// ReleaseBuffer marks a buffer reusable. A buffer still referenced by an
// in-flight request must be refused with ErrBufferBusy.
func (g *IoUringGuard) ReleaseBuffer(id int) error {
	return errors.New("not implemented")
}

// AfterFork returns the child process view of the ring. The child must not be
// able to reach the parent fixed file table.
func (g *IoUringGuard) AfterFork() (*IoUringGuard, error) {
	return nil, errors.New("not implemented")
}

// Destroy waits for all in-flight requests, drains their completions and
// leaves the ring closed.
func (g *IoUringGuard) Destroy() error {
	return errors.New("not implemented")
}

// InFlight reports the number of requests not yet completed.
func (g *IoUringGuard) InFlight() int {
	return 0
}

// TenantInFlight reports the number of in-flight requests of one tenant.
func (g *IoUringGuard) TenantInFlight(tenant string) int {
	return 0
}

// TenantUsage reports the quota usage of one tenant on this ring.
func (g *IoUringGuard) TenantUsage(tenant string) int {
	return 0
}

// Closed reports whether Destroy already ran.
func (g *IoUringGuard) Closed() bool {
	return false
}
