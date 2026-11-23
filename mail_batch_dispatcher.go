package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Mail is one compensational mail created by a batch job.
type Mail struct {
	ID        string
	Recipient string
}

// MailBatchDispatcher sends compensational mails in resumable batches.
type MailBatchDispatcher struct {
}

// NewMailBatchDispatcher builds an empty batch dispatcher.
func NewMailBatchDispatcher() *MailBatchDispatcher {
	return &MailBatchDispatcher{}
}

// MailID is the stable mail id of one recipient inside one batch.
func (d *MailBatchDispatcher) MailID(batchID, recipient string) string {
	return ""
}

// Plan builds the mails of a batch, deduplicating recipients.
func (d *MailBatchDispatcher) Plan(batchID string, recipients []string) []Mail {
	return nil
}

// RecordSuccess records that a mail of a batch was delivered.
func (d *MailBatchDispatcher) RecordSuccess(batchID, recipient string) {
}

// Pending is the number of mails of a batch that still need a send.
func (d *MailBatchDispatcher) Pending(batchID string) int {
	return 0
}

// MaxConcurrent is the number of senders allowed for one batch.
func (d *MailBatchDispatcher) MaxConcurrent(batchID string) int {
	return 0
}

// Normalize is the canonical form of a recipient name.
func (d *MailBatchDispatcher) Normalize(recipient string) string {
	return ""
}

// RetryAttachments is the number of attachment copies after a retry.
func (d *MailBatchDispatcher) RetryAttachments(mailID string, attempts int) int {
	return 0
}

// KeepsOffline reports whether the mail of an offline player is kept.
func (d *MailBatchDispatcher) KeepsOffline(recipient string) bool {
	return false
}

// ExpiryFrom is the mail expiry timestamp counted from delivery.
func (d *MailBatchDispatcher) ExpiryFrom(deliveredAt int64) int64 {
	return 0
}

// CompleteShard records that one shard of a batch finished.
func (d *MailBatchDispatcher) CompleteShard(batchID string, shard int) {
}

// ShardProgress is the number of finished mails in a batch shard.
func (d *MailBatchDispatcher) ShardProgress(batchID string, shard int) int {
	return 0
}

// Enqueue accepts a mail unless the send queue is at capacity.
func (d *MailBatchDispatcher) Enqueue(queueCap int) (bool, error) {
	return false, errNotImplemented
}

// Route is the mail route of another server.
func (d *MailBatchDispatcher) Route(server string) string {
	return ""
}

// MarkFailed records that a mail failed permanently and needs a dead letter.
func (d *MailBatchDispatcher) MarkFailed(batchID string) {
}

// DeadLetters is the number of dead letters of a batch.
func (d *MailBatchDispatcher) DeadLetters(batchID string) int {
	return 0
}

// StatusAfterSend is the batch status after a send attempt.
func (d *MailBatchDispatcher) StatusAfterSend(success bool) string {
	return ""
}

// PlanShards splits a recipient list into the requested number of shards.
func (d *MailBatchDispatcher) PlanShards(recipients []string, shards int) [][]string {
	return nil
}

// RetryAllowed reports whether another send attempt is allowed.
func (d *MailBatchDispatcher) RetryAllowed(mailID string, attempts int) bool {
	return false
}
