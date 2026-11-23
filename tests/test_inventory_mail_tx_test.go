package task

import (
	"testing"

	impl "gsb/batch45-game-a-08"
)

func TestClaimDeletesMailOnlyOnSuccess(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.Deposit("mail-1", []impl.Attachment{{ItemID: "sword", Count: 1}})
	res, err := m.Claim("mail-1", "char-1", 3)
	if err != nil {
		t.Fatalf("claiming a mail with free bag slots failed: %v", err)
	}
	if !res.MailDeleted {
		t.Fatalf("claim result must report the mail as deleted")
	}
	if got := m.MailState("mail-1"); got != "deleted" {
		t.Fatalf("mail state = %q, want deleted", got)
	}
}

func TestClaimKeepsMailWhenBagIsFull(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.Deposit("mail-1", []impl.Attachment{{ItemID: "sword", Count: 1}})
	if _, err := m.Claim("mail-1", "char-1", 0); err == nil {
		t.Fatalf("claiming without free bag slots must fail")
	}
	if got := m.MailState("mail-1"); got != "present" {
		t.Fatalf("mail state after failed claim = %q, want present", got)
	}
}

func TestConcurrentClaimGrantsOnce(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.Deposit("mail-1", []impl.Attachment{{ItemID: "sword", Count: 1}})
	if got := m.ConcurrentClaim("mail-1", 8); got != 1 {
		t.Fatalf("%d concurrent claims succeeded, want exactly 1", got)
	}
}

func TestBoundAttachmentIsNotTradable(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	if m.Tradable(impl.Attachment{ItemID: "sword", Count: 1, Bound: true}) {
		t.Fatalf("bound attachment must not be tradable")
	}
	if !m.Tradable(impl.Attachment{ItemID: "sword", Count: 1, Bound: false}) {
		t.Fatalf("unbound attachment must stay tradable")
	}
}

func TestMergeStackRejectsOverflow(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	got, err := m.MergeStack(9, 5, 10)
	if got != 10 {
		t.Fatalf("merged stack size = %d, want the limit 10", got)
	}
	if err == nil {
		t.Fatalf("merging past the stack limit must report the overflow")
	}
}

func TestExpiredMailReturnsAttachments(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.Deposit("mail-1", []impl.Attachment{{ItemID: "sword", Count: 1}, {ItemID: "potion", Count: 3}})
	returned, err := m.ExpireReturn("mail-1")
	if err != nil {
		t.Fatalf("expiring a mail failed: %v", err)
	}
	if returned != 2 {
		t.Fatalf("returned %d attachments, want 2", returned)
	}
}

func TestCrossServerClaimLocked(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	if !m.CrossServerLock("char-1") {
		t.Fatalf("first cross server lock must succeed")
	}
	if m.CrossServerLock("char-1") {
		t.Fatalf("the same character must not hold two cross server claim locks")
	}
}

func TestStackLimitValidated(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	if m.StackValid(20, 25) {
		t.Fatalf("a stack of 25 over the limit of 20 must be invalid")
	}
	if !m.StackValid(20, 20) {
		t.Fatalf("a stack exactly at the limit must be valid")
	}
}

func TestReceiptPersisted(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.MarkReceipt("mail-1")
	if got := m.PersistReceipts(); got != 1 {
		t.Fatalf("persisted %d receipts, want 1", got)
	}
}

func TestBagExpansionDuringClaim(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	slots, err := m.EnsureSlots(3)
	if err != nil {
		t.Fatalf("ensuring 3 bag slots failed: %v", err)
	}
	if slots < 3 {
		t.Fatalf("free slots after expansion = %d, want at least 3", slots)
	}
}

func TestExpiredItemNotClaimable(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	if m.ExpiredItemClaimable(100, 200) {
		t.Fatalf("an attachment expired before now must not be claimable")
	}
	if !m.ExpiredItemClaimable(300, 200) {
		t.Fatalf("an attachment that expires in the future must be claimable")
	}
}

func TestRollbackRestoresMailState(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.Deposit("mail-1", []impl.Attachment{{ItemID: "sword", Count: 1}})
	if _, err := m.Claim("mail-1", "char-1", 0); err == nil {
		t.Fatalf("claim without slots must fail")
	}
	if got := m.RollbackState("mail-1"); got != "present" {
		t.Fatalf("mail state after rollback = %q, want present", got)
	}
}

func TestInventoryCountAfterClaim(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.Deposit("mail-1", []impl.Attachment{{ItemID: "sword", Count: 1}})
	if _, err := m.Claim("mail-1", "char-1", 3); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if got := m.InventoryCount("sword"); got != 1 {
		t.Fatalf("inventory count = %d, want 1 claimed sword", got)
	}
}

func TestMailCountAfterClaim(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	m.Deposit("mail-1", []impl.Attachment{{ItemID: "sword", Count: 1}})
	if got := m.MailCount(); got != 1 {
		t.Fatalf("stored mails = %d, want 1 after deposit", got)
	}
	if _, err := m.Claim("mail-1", "char-1", 3); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if got := m.MailCount(); got != 0 {
		t.Fatalf("stored mails = %d, want 0 after the claim", got)
	}
}

func TestApplyUsesOneSlotPerStack(t *testing.T) {
	m := impl.NewMailInventoryTx(20)
	slots, err := m.Apply(impl.Attachment{ItemID: "potion", Count: 3, Bound: false})
	if err != nil {
		t.Fatalf("applying an attachment failed: %v", err)
	}
	if slots != 1 {
		t.Fatalf("attachment used %d slots, want a single stack slot", slots)
	}
}
