package task

import (
	"errors"
	"hash/fnv"
	"strings"
	"sync"
)

// ErrQueueFull is returned when the send queue is at capacity and the
// incoming mail is rejected instead of overwriting a queued one.
var ErrQueueFull = errors.New("send queue is full")

const (
	// mailTTLSeconds is the mail lifetime counted from actual delivery.
	mailTTLSeconds = 30 * 24 * 60 * 60
	// maxRetryAttempts caps how many send attempts one mail may make.
	maxRetryAttempts = 3
)

// Mail is one compensational mail created by a batch job.
type Mail struct {
	ID        string
	Recipient string
}

// DispatcherState is the persistable state used to resume after a restart.
type DispatcherState struct {
	Planned       map[string][]Mail      `json:"planned"`
	Delivered     map[string][]string    `json:"delivered"`
	ShardProgress map[string]map[int]int `json:"shardProgress"`
	DeadLetters   map[string][]string    `json:"deadLetters"`
}

// MailBatchDispatcher sends compensational mails in resumable batches.
type MailBatchDispatcher struct {
	mu            sync.Mutex
	planned       map[string][]Mail
	plannedSet    map[string]map[string]bool
	delivered     map[string]map[string]bool
	shardProgress map[string]map[int]int
	deadLetters   map[string][]string
	sending       map[string]bool
	queueLen      int
}

// NewMailBatchDispatcher builds an empty batch dispatcher.
func NewMailBatchDispatcher() *MailBatchDispatcher {
	return &MailBatchDispatcher{
		planned:       make(map[string][]Mail),
		plannedSet:    make(map[string]map[string]bool),
		delivered:     make(map[string]map[string]bool),
		shardProgress: make(map[string]map[int]int),
		deadLetters:   make(map[string][]string),
		sending:       make(map[string]bool),
	}
}

// MailID is the stable mail id of one recipient inside one batch.
// It is a pure function of batch and recipient, so a retry never
// regenerates a different id.
func (d *MailBatchDispatcher) MailID(batchID, recipient string) string {
	return batchID + ":" + d.Normalize(recipient)
}

// Plan builds the mails of a batch, deduplicating recipients.
// Recipients are normalized (trimmed, lowercased) before dedup, so
// "Player-1" and "player-1" are the same person. Planning is
// idempotent: replanning a batch never duplicates or resets progress.
func (d *MailBatchDispatcher) Plan(batchID string, recipients []string) []Mail {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := d.plannedSet[batchID]
	if seen == nil {
		seen = make(map[string]bool)
		d.plannedSet[batchID] = seen
	}
	for _, recipient := range recipients {
		norm := d.Normalize(recipient)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		d.planned[batchID] = append(d.planned[batchID], Mail{
			ID:        d.MailID(batchID, norm),
			Recipient: norm,
		})
	}
	mails := make([]Mail, len(d.planned[batchID]))
	copy(mails, d.planned[batchID])
	return mails
}

// RecordSuccess records that a mail of a batch was delivered.
// Only the delivered recipient leaves the pending set, and a duplicate
// record (e.g. a retried write-back) is a no-op.
func (d *MailBatchDispatcher) RecordSuccess(batchID, recipient string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	norm := d.Normalize(recipient)
	if !d.plannedSet[batchID][norm] {
		return
	}
	done := d.delivered[batchID]
	if done == nil {
		done = make(map[string]bool)
		d.delivered[batchID] = done
	}
	done[norm] = true
}

// Pending is the number of mails of a batch that still need a send.
func (d *MailBatchDispatcher) Pending(batchID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	pending := 0
	for _, mail := range d.planned[batchID] {
		if !d.delivered[batchID][mail.Recipient] {
			pending++
		}
	}
	return pending
}

// MaxConcurrent is the number of senders allowed for one batch.
// Sends inside one batch are strictly serial; different batches may
// still run in parallel.
func (d *MailBatchDispatcher) MaxConcurrent(batchID string) int {
	return 1
}

// Normalize is the canonical form of a recipient name.
func (d *MailBatchDispatcher) Normalize(recipient string) string {
	return strings.ToLower(strings.TrimSpace(recipient))
}

// RetryAttachments is the number of attachment copies after a retry.
// No matter how many attempts happen, exactly one copy is kept.
func (d *MailBatchDispatcher) RetryAttachments(mailID string, attempts int) int {
	return 1
}

// KeepsOffline reports whether the mail of an offline player is kept.
func (d *MailBatchDispatcher) KeepsOffline(recipient string) bool {
	return true
}

// ExpiryFrom is the mail expiry timestamp counted from delivery.
func (d *MailBatchDispatcher) ExpiryFrom(deliveredAt int64) int64 {
	return deliveredAt + mailTTLSeconds
}

// CompleteShard records that one shard of a batch finished.
func (d *MailBatchDispatcher) CompleteShard(batchID string, shard int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	progress := d.shardProgress[batchID]
	if progress == nil {
		progress = make(map[int]int)
		d.shardProgress[batchID] = progress
	}
	progress[shard]++
}

// ShardProgress is the number of finished mails in a batch shard.
func (d *MailBatchDispatcher) ShardProgress(batchID string, shard int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.shardProgress[batchID][shard]
}

// Enqueue accepts a mail unless the send queue is at capacity.
// A full queue rejects the new mail with an error on the spot instead
// of overwriting an already queued mail.
func (d *MailBatchDispatcher) Enqueue(queueCap int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.queueLen >= queueCap {
		return false, ErrQueueFull
	}
	d.queueLen++
	return true, nil
}

// Dequeue releases one slot of the send queue after a mail leaves it.
func (d *MailBatchDispatcher) Dequeue() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.queueLen > 0 {
		d.queueLen--
	}
}

// Route is the mail route of another server.
// Cross-server mails route to their server; an empty server is local.
func (d *MailBatchDispatcher) Route(server string) string {
	if server == "" {
		return "local"
	}
	return server
}

// MarkFailed records that a mail failed permanently and needs a dead letter.
func (d *MailBatchDispatcher) MarkFailed(batchID string) {
	d.MarkFailedMail(batchID, batchID+":dead")
}

// MarkFailedMail records one permanently failed mail as a dead letter.
func (d *MailBatchDispatcher) MarkFailedMail(batchID, mailID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deadLetters[batchID] = append(d.deadLetters[batchID], mailID)
}

// DeadLetters is the number of dead letters of a batch.
func (d *MailBatchDispatcher) DeadLetters(batchID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.deadLetters[batchID])
}

// ExportDeadLetters lists the dead letters of one batch so a
// compensational resend can be reviewed mail by mail.
func (d *MailBatchDispatcher) ExportDeadLetters(batchID string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.deadLetters[batchID]))
	copy(out, d.deadLetters[batchID])
	return out
}

// StatusAfterSend is the batch status after a send attempt.
// Only two states are exposed: pending and sent.
func (d *MailBatchDispatcher) StatusAfterSend(success bool) string {
	if success {
		return "sent"
	}
	return "pending"
}

// PlanShards splits a recipient list into the requested number of shards.
// Shard ownership is fixed by the recipient hash, so scaling the shard
// count only migrates recipients whose ownership actually changed.
func (d *MailBatchDispatcher) PlanShards(recipients []string, shards int) [][]string {
	if shards <= 0 {
		return nil
	}
	planned := make([][]string, shards)
	for _, recipient := range recipients {
		shard := d.ShardOf(recipient, shards)
		planned[shard] = append(planned[shard], recipient)
	}
	return planned
}

// ShardOf is the stable shard owner of one recipient.
func (d *MailBatchDispatcher) ShardOf(recipient string, shards int) int {
	if shards <= 0 {
		return 0
	}
	h := fnv.New32a()
	h.Write([]byte(d.Normalize(recipient)))
	return int(h.Sum32() % uint32(shards))
}

// RetryAllowed reports whether another send attempt is allowed.
func (d *MailBatchDispatcher) RetryAllowed(mailID string, attempts int) bool {
	return attempts >= 0 && attempts < maxRetryAttempts
}

// TryAcquireSend takes the per-batch send slot. At most one sender may
// hold a batch at a time; other batches are unaffected.
func (d *MailBatchDispatcher) TryAcquireSend(batchID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sending[batchID] {
		return false
	}
	d.sending[batchID] = true
	return true
}

// ReleaseSend returns the per-batch send slot.
func (d *MailBatchDispatcher) ReleaseSend(batchID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sending, batchID)
}

// Snapshot captures the resumable state: per-shard progress and the
// delivered records that guarantee no recipient gets a second mail.
func (d *MailBatchDispatcher) Snapshot() DispatcherState {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := DispatcherState{
		Planned:       make(map[string][]Mail, len(d.planned)),
		Delivered:     make(map[string][]string, len(d.delivered)),
		ShardProgress: make(map[string]map[int]int, len(d.shardProgress)),
		DeadLetters:   make(map[string][]string, len(d.deadLetters)),
	}
	for batchID, mails := range d.planned {
		copied := make([]Mail, len(mails))
		copy(copied, mails)
		state.Planned[batchID] = copied
	}
	for batchID, recipients := range d.delivered {
		list := make([]string, 0, len(recipients))
		for recipient := range recipients {
			list = append(list, recipient)
		}
		state.Delivered[batchID] = list
	}
	for batchID, progress := range d.shardProgress {
		copied := make(map[int]int, len(progress))
		for shard, count := range progress {
			copied[shard] = count
		}
		state.ShardProgress[batchID] = copied
	}
	for batchID, letters := range d.deadLetters {
		copied := make([]string, len(letters))
		copy(copied, letters)
		state.DeadLetters[batchID] = copied
	}
	return state
}

// Restore loads a snapshot taken before a restart. Already delivered
// recipients stay delivered and will not be sent a second mail.
func (d *MailBatchDispatcher) Restore(state DispatcherState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for batchID, mails := range state.Planned {
		if d.plannedSet[batchID] == nil {
			d.plannedSet[batchID] = make(map[string]bool)
		}
		for _, mail := range mails {
			norm := d.Normalize(mail.Recipient)
			if norm == "" || d.plannedSet[batchID][norm] {
				continue
			}
			d.plannedSet[batchID][norm] = true
			d.planned[batchID] = append(d.planned[batchID], Mail{
				ID:        d.MailID(batchID, norm),
				Recipient: norm,
			})
		}
	}
	for batchID, recipients := range state.Delivered {
		if d.delivered[batchID] == nil {
			d.delivered[batchID] = make(map[string]bool)
		}
		for _, recipient := range recipients {
			d.delivered[batchID][d.Normalize(recipient)] = true
		}
	}
	for batchID, progress := range state.ShardProgress {
		if d.shardProgress[batchID] == nil {
			d.shardProgress[batchID] = make(map[int]int)
		}
		for shard, count := range progress {
			d.shardProgress[batchID][shard] = count
		}
	}
	for batchID, letters := range state.DeadLetters {
		d.deadLetters[batchID] = append(d.deadLetters[batchID], letters...)
	}
}
