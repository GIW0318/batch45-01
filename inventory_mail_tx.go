package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Attachment is one item stack attached to a mail.
type Attachment struct {
	ItemID string
	Count  int
	Bound  bool
}

// ClaimResult is the outcome of claiming a mail attachment.
type ClaimResult struct {
	MailDeleted bool
	Granted     []Attachment
}

// MailInventoryTx claims mail attachments inside one inventory transaction.
type MailInventoryTx struct {
	slots int
}

// NewMailInventoryTx builds a mail inventory transaction with the given slot count.
func NewMailInventoryTx(slots int) *MailInventoryTx {
	return &MailInventoryTx{slots: slots}
}

// Deposit stores a mail with its attachments.
func (m *MailInventoryTx) Deposit(mailID string, atts []Attachment) {
}

// Claim moves the attachments into the bag and deletes the mail atomically.
func (m *MailInventoryTx) Claim(mailID, charID string, bagFree int) (ClaimResult, error) {
	return ClaimResult{}, errNotImplemented
}

// MailState is the lifecycle state of a mail: present or deleted.
func (m *MailInventoryTx) MailState(mailID string) string {
	return ""
}

// ConcurrentClaim runs several claim attempts and reports how many succeeded.
func (m *MailInventoryTx) ConcurrentClaim(mailID string, workers int) int {
	return 0
}

// Tradable reports whether an attachment may be traded.
func (m *MailInventoryTx) Tradable(att Attachment) bool {
	return false
}

// MergeStack merges a stack without overflowing the stack limit.
func (m *MailInventoryTx) MergeStack(existing, incoming, limit int) (int, error) {
	return 0, errNotImplemented
}

// ExpireReturn returns the attachments of an expired mail to the sender.
func (m *MailInventoryTx) ExpireReturn(mailID string) (int, error) {
	return 0, errNotImplemented
}

// CrossServerLock takes the cross server claim lock for a character.
func (m *MailInventoryTx) CrossServerLock(charID string) bool {
	return false
}

// StackValid reports whether a stack count respects the stack limit.
func (m *MailInventoryTx) StackValid(limit, count int) bool {
	return false
}

// MarkReceipt records that a claim receipt was written.
func (m *MailInventoryTx) MarkReceipt(mailID string) {
}

// PersistReceipts returns how many receipts were persisted.
func (m *MailInventoryTx) PersistReceipts() int {
	return 0
}

// EnsureSlots makes sure the bag can hold the needed slots.
func (m *MailInventoryTx) EnsureSlots(needed int) (int, error) {
	return 0, errNotImplemented
}

// ExpiredItemClaimable reports whether an attachment is still claimable.
func (m *MailInventoryTx) ExpiredItemClaimable(expiredAt, now int64) bool {
	return false
}

// RollbackState is the mail state after a rolled back claim transaction.
func (m *MailInventoryTx) RollbackState(mailID string) string {
	return ""
}

// InventoryCount is the number of an item currently in the bag.
func (m *MailInventoryTx) InventoryCount(itemID string) int {
	return 0
}

// MailCount is the number of mails currently stored.
func (m *MailInventoryTx) MailCount() int {
	return 0
}

// Apply writes one attachment into the bag and returns the slots used.
func (m *MailInventoryTx) Apply(att Attachment) (int, error) {
	return 0, errNotImplemented
}
