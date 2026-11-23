package task

import (
	"errors"
	"os"
	"time"
)

var (
	ErrQueueFull = errors.New("audit: queue is full")
	ErrDiskFull  = errors.New("audit: disk full")
	ErrTxClosed  = errors.New("audit: transaction already finished")
)

// Event is one audit record.
type Event struct {
	ID     string
	Seq    uint64
	Actor  string
	Action string
	Fields map[string]string
	At     time.Time
	TxID   string
}

// Options configures the pipeline.
type Options struct {
	Path       string
	QueueSize  int
	RotateSize int64
	MaxBytes   int64
	SyslogAddr string
	FileMode   os.FileMode
}

// Filter selects events in Query.
type Filter struct {
	Actor  string
	Action string
	Since  time.Time
	Until  time.Time
}

// Tx is one business transaction whose audit record must commit with it.
type Tx struct {
	ID string
}

// AuditPipeline writes ordered, redacted audit events.
type AuditPipeline struct{}

// NewAuditPipeline opens the audit file. FileMode defaults to 0600.
func NewAuditPipeline(o Options) (*AuditPipeline, error) {
	return nil, errors.New("not implemented")
}

// Append enqueues one event. A full queue must return ErrQueueFull instead of
// silently dropping the event. Sensitive fields are redacted and a repeated
// event ID is suppressed.
func (p *AuditPipeline) Append(ev Event) error {
	return errors.New("not implemented")
}

// AppendTx stages an event inside a transaction.
func (p *AuditPipeline) AppendTx(tx *Tx, ev Event) error {
	return errors.New("not implemented")
}

// Begin starts a transaction.
func (p *AuditPipeline) Begin() (*Tx, error) {
	return nil, errors.New("not implemented")
}

// Commit persists the transaction's audit records.
func (p *AuditPipeline) Commit(tx *Tx) error {
	return errors.New("not implemented")
}

// Rollback discards the transaction's audit records.
func (p *AuditPipeline) Rollback(tx *Tx) error {
	return errors.New("not implemented")
}

// ReadAll flushes pending events and returns every stored event ordered by
// sequence.
func (p *AuditPipeline) ReadAll() ([]Event, error) {
	return nil, errors.New("not implemented")
}

// Query returns the events matching the filter, ordered by sequence.
func (p *AuditPipeline) Query(f Filter) ([]Event, error) {
	return nil, errors.New("not implemented")
}

// Seq returns the last assigned sequence number.
func (p *AuditPipeline) Seq() uint64 {
	return 0
}

// Redact masks the values of sensitive keys in a key=value string.
func (p *AuditPipeline) Redact(s string) string {
	return ""
}

// Rotate renames the current file to Path+".1" and reopens Path, so later
// writes never land in the rotated inode.
func (p *AuditPipeline) Rotate() error {
	return errors.New("not implemented")
}

// FileMode returns the permission bits used for the audit file.
func (p *AuditPipeline) FileMode() os.FileMode {
	return 0
}

// Flush writes buffered events, draining the syslog backlog when the remote
// endpoint is reachable.
func (p *AuditPipeline) Flush() error {
	return errors.New("not implemented")
}

// Dropped returns the number of silently lost events, which must stay zero.
func (p *AuditPipeline) Dropped() int {
	return 0
}

// Restore loads the sequence counter from existing events after a restart.
func (p *AuditPipeline) Restore(events []Event) error {
	return errors.New("not implemented")
}

// SetSyslogUp toggles the simulated remote syslog availability.
func (p *AuditPipeline) SetSyslogUp(up bool) {
}

// SyslogBacklog returns the number of events buffered for the remote syslog.
func (p *AuditPipeline) SyslogBacklog() int {
	return 0
}

// FilePath returns the current audit file path.
func (p *AuditPipeline) FilePath() string {
	return ""
}
