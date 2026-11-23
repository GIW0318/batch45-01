package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
type AuditPipeline struct {
	mu          sync.Mutex
	path        string
	mode        os.FileMode
	queueSize   int
	maxBytes    int64
	syslogAddr  string
	file        *os.File
	seq         uint64
	written     int64
	pendingByte int64
	queues      map[string][]queuedEvent
	committed   []queuedEvent
	stored      []Event
	seen        map[string]struct{}
	txs         map[string]*txState
	txSeq       uint64
	syslogUp    bool
	backlog     []Event
	dropped     int
}

type queuedEvent struct {
	ev   Event
	line []byte
}

type txState struct {
	events []Event
}

type jsonEvent struct {
	ID     string            `json:"id"`
	Seq    uint64            `json:"seq"`
	Actor  string            `json:"actor"`
	Action string            `json:"action"`
	Fields map[string]string `json:"fields"`
	At     time.Time         `json:"at"`
	TxID   string            `json:"tx_id,omitempty"`
}

var sensitiveKeys = map[string]bool{
	"token":         true,
	"password":      true,
	"secret":        true,
	"authorization": true,
}

// NewAuditPipeline opens the audit file. FileMode defaults to 0600.
func NewAuditPipeline(o Options) (*AuditPipeline, error) {
	if o.Path == "" {
		return nil, errors.New("audit: path is required")
	}
	mode := o.FileMode
	if mode == 0 {
		mode = 0o600
	}
	queueSize := o.QueueSize
	if queueSize <= 0 {
		queueSize = 1024
	}
	f, err := os.OpenFile(o.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, mode)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return nil, err
	}
	return &AuditPipeline{
		path:       o.Path,
		mode:       mode,
		queueSize:  queueSize,
		maxBytes:   o.MaxBytes,
		syslogAddr: o.SyslogAddr,
		file:       f,
		queues:     make(map[string][]queuedEvent),
		seen:       make(map[string]struct{}),
		txs:        make(map[string]*txState),
		syslogUp:   true,
	}, nil
}

// Append enqueues one event. A full queue must return ErrQueueFull instead of
// silently dropping the event. Sensitive fields are redacted and a repeated
// event ID is suppressed.
func (p *AuditPipeline) Append(ev Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ev.ID != "" {
		if _, dup := p.seen[ev.ID]; dup {
			return nil
		}
	}
	q := p.queues[ev.Actor]
	if len(q) >= p.queueSize {
		return ErrQueueFull
	}
	qe, err := p.prepareLocked(ev, p.seq+1)
	if err != nil {
		return err
	}
	if err := p.checkDiskLocked(int64(len(qe.line))); err != nil {
		return err
	}
	p.seq++
	p.queues[ev.Actor] = append(q, qe)
	p.pendingByte += int64(len(qe.line))
	if ev.ID != "" {
		p.seen[ev.ID] = struct{}{}
	}
	return nil
}

// AppendTx stages an event inside a transaction.
func (p *AuditPipeline) AppendTx(tx *Tx, ev Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if tx == nil {
		return ErrTxClosed
	}
	st, ok := p.txs[tx.ID]
	if !ok {
		return ErrTxClosed
	}
	ev.TxID = tx.ID
	st.events = append(st.events, ev)
	return nil
}

// Begin starts a transaction.
func (p *AuditPipeline) Begin() (*Tx, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.txSeq++
	tx := &Tx{ID: "tx-" + strconv.FormatUint(p.txSeq, 10)}
	p.txs[tx.ID] = &txState{}
	return tx, nil
}

// Commit persists the transaction's audit records.
func (p *AuditPipeline) Commit(tx *Tx) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if tx == nil {
		return ErrTxClosed
	}
	st, ok := p.txs[tx.ID]
	if !ok {
		return ErrTxClosed
	}
	var batch []queuedEvent
	var size int64
	seq := p.seq
	for _, ev := range st.events {
		if ev.ID != "" {
			if _, dup := p.seen[ev.ID]; dup {
				continue
			}
		}
		qe, err := p.prepareLocked(ev, seq+1)
		if err != nil {
			return err
		}
		seq++
		size += int64(len(qe.line))
		batch = append(batch, qe)
	}
	if err := p.checkDiskLocked(size); err != nil {
		return err
	}
	p.seq = seq
	p.committed = append(p.committed, batch...)
	p.pendingByte += size
	for _, qe := range batch {
		if qe.ev.ID != "" {
			p.seen[qe.ev.ID] = struct{}{}
		}
	}
	delete(p.txs, tx.ID)
	return nil
}

// Rollback discards the transaction's audit records.
func (p *AuditPipeline) Rollback(tx *Tx) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if tx == nil {
		return ErrTxClosed
	}
	if _, ok := p.txs[tx.ID]; !ok {
		return ErrTxClosed
	}
	delete(p.txs, tx.ID)
	return nil
}

// ReadAll flushes pending events and returns every stored event ordered by
// sequence.
func (p *AuditPipeline) ReadAll() ([]Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.flushLocked(); err != nil {
		return nil, err
	}
	out := make([]Event, len(p.stored))
	copy(out, p.stored)
	return out, nil
}

// Query returns the events matching the filter, ordered by sequence.
func (p *AuditPipeline) Query(f Filter) ([]Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.flushLocked(); err != nil {
		return nil, err
	}
	var out []Event
	for _, ev := range p.stored {
		if f.Actor != "" && ev.Actor != f.Actor {
			continue
		}
		if f.Action != "" && ev.Action != f.Action {
			continue
		}
		if !f.Since.IsZero() && ev.At.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && ev.At.After(f.Until) {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// Seq returns the last assigned sequence number.
func (p *AuditPipeline) Seq() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seq
}

// Redact masks the values of sensitive keys in a key=value string.
func (p *AuditPipeline) Redact(s string) string {
	parts := strings.Split(s, "&")
	for i, part := range parts {
		key, _, found := strings.Cut(part, "=")
		if found && sensitiveKeys[strings.ToLower(strings.TrimSpace(key))] {
			parts[i] = key + "=***"
		}
	}
	return strings.Join(parts, "&")
}

// Rotate renames the current file to Path+".1" and reopens Path, so later
// writes never land in the rotated inode.
func (p *AuditPipeline) Rotate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.flushLocked(); err != nil {
		return err
	}
	if err := p.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(p.path, p.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, p.mode)
	if err != nil {
		return err
	}
	if err := f.Chmod(p.mode); err != nil {
		f.Close()
		return err
	}
	p.file = f
	p.stored = nil
	p.written = 0
	return nil
}

// FileMode returns the permission bits used for the audit file.
func (p *AuditPipeline) FileMode() os.FileMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

// Flush writes buffered events, draining the syslog backlog when the remote
// endpoint is reachable.
func (p *AuditPipeline) Flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flushLocked()
}

// Dropped returns the number of silently lost events, which must stay zero.
func (p *AuditPipeline) Dropped() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dropped
}

// Restore loads the sequence counter from existing events after a restart.
func (p *AuditPipeline) Restore(events []Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ev := range events {
		if ev.Seq > p.seq {
			p.seq = ev.Seq
		}
		if ev.ID != "" {
			p.seen[ev.ID] = struct{}{}
		}
	}
	return nil
}

// SetSyslogUp toggles the simulated remote syslog availability.
func (p *AuditPipeline) SetSyslogUp(up bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.syslogUp = up
}

// SyslogBacklog returns the number of events buffered for the remote syslog.
func (p *AuditPipeline) SyslogBacklog() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.backlog)
}

// FilePath returns the current audit file path.
func (p *AuditPipeline) FilePath() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.path
}

func (p *AuditPipeline) prepareLocked(ev Event, seq uint64) (queuedEvent, error) {
	ev.Seq = seq
	ev.At = ev.At.UTC()
	fields := make(map[string]string, len(ev.Fields))
	for k, v := range ev.Fields {
		if sensitiveKeys[strings.ToLower(k)] {
			fields[k] = "***"
		} else {
			fields[k] = v
		}
	}
	ev.Fields = fields
	line, err := json.Marshal(jsonEvent{
		ID:     ev.ID,
		Seq:    ev.Seq,
		Actor:  ev.Actor,
		Action: ev.Action,
		Fields: ev.Fields,
		At:     ev.At,
		TxID:   ev.TxID,
	})
	if err != nil {
		return queuedEvent{}, err
	}
	line = append(line, '\n')
	return queuedEvent{ev: ev, line: line}, nil
}

func (p *AuditPipeline) checkDiskLocked(size int64) error {
	if p.maxBytes > 0 && p.written+p.pendingByte+size > p.maxBytes {
		return ErrDiskFull
	}
	return nil
}

func (p *AuditPipeline) flushLocked() error {
	all := make([]queuedEvent, 0, len(p.committed))
	all = append(all, p.committed...)
	for _, q := range p.queues {
		all = append(all, q...)
	}
	if len(all) == 0 {
		p.drainSyslogLocked(nil)
		return nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ev.Seq < all[j].ev.Seq })
	var buf bytes.Buffer
	for _, qe := range all {
		buf.Write(qe.line)
	}
	n, err := p.file.Write(buf.Bytes())
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			return ErrDiskFull
		}
		return err
	}
	p.written += int64(n)
	events := make([]Event, 0, len(all))
	for _, qe := range all {
		p.stored = append(p.stored, qe.ev)
		events = append(events, qe.ev)
	}
	p.committed = nil
	p.queues = make(map[string][]queuedEvent)
	p.pendingByte = 0
	p.drainSyslogLocked(events)
	return nil
}

func (p *AuditPipeline) drainSyslogLocked(events []Event) {
	if p.syslogAddr == "" {
		return
	}
	if p.syslogUp {
		p.backlog = nil
		return
	}
	p.backlog = append(p.backlog, events...)
}
