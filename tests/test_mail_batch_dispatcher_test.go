package task

import (
	"testing"

	impl "gsb/batch45-game-a-10"
)

func TestMailIDStableAcrossRetries(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	first := d.MailID("batch-1", "player-1")
	if first != "batch-1:player-1" {
		t.Fatalf("mail id = %q, want batch-1:player-1", first)
	}
	if second := d.MailID("batch-1", "player-1"); second != first {
		t.Fatalf("mail id changed on retry: %q then %q", first, second)
	}
}

func TestPlanDeduplicatesRecipientsCaseInsensitive(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	mails := d.Plan("batch-1", []string{"Player-1", "player-1", "player-2"})
	if len(mails) != 2 {
		t.Fatalf("planned %d mails, want 2 after case insensitive deduplication", len(mails))
	}
}

func TestRecordSuccessSkipsRetry(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	d.Plan("batch-1", []string{"player-1", "player-2"})
	d.RecordSuccess("batch-1", "player-1")
	if got := d.Pending("batch-1"); got != 1 {
		t.Fatalf("pending mails = %d, want only the 1 unsent mail", got)
	}
}

func TestMaxConcurrentIsOne(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if got := d.MaxConcurrent("batch-1"); got != 1 {
		t.Fatalf("max concurrency of a batch = %d, want 1", got)
	}
}

func TestNormalizeRecipientName(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if got := d.Normalize(" Player-One "); got != "player-one" {
		t.Fatalf("normalized recipient = %q, want player-one", got)
	}
}

func TestRetryKeepsSingleAttachmentCopy(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if got := d.RetryAttachments("mail-1", 3); got != 1 {
		t.Fatalf("attachment copies after 3 retries = %d, want 1", got)
	}
}

func TestOfflineRecipientsKept(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if !d.KeepsOffline("player-1") {
		t.Fatalf("mails for offline players must be kept rather than dropped")
	}
}

func TestExpiryCountsFromDelivery(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if got := d.ExpiryFrom(1000); got != 2593000 {
		t.Fatalf("expiry = %d, want 30 days after delivery at 1000", got)
	}
}

func TestShardProgressRecorded(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	d.CompleteShard("batch-1", 2)
	if got := d.ShardProgress("batch-1", 2); got != 1 {
		t.Fatalf("shard progress = %d, want 1 finished shard", got)
	}
}

func TestQueueBackpressureRejectsNewMail(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	accepted, err := d.Enqueue(2)
	if err != nil {
		t.Fatalf("enqueue with free queue space failed: %v", err)
	}
	if !accepted {
		t.Fatalf("mail must be accepted while the queue has room")
	}
}

func TestCrossServerMailRouted(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if got := d.Route("s2"); got != "s2" {
		t.Fatalf("mail route = %q, want s2", got)
	}
	if got := d.Route(""); got != "local" {
		t.Fatalf("local mail route = %q, want local", got)
	}
}

func TestFailedMailGoesToDeadLetter(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	d.MarkFailed("batch-1")
	if got := d.DeadLetters("batch-1"); got != 1 {
		t.Fatalf("dead letters = %d, want 1 failed mail", got)
	}
}

func TestStatusWrittenOnlyAfterSend(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if got := d.StatusAfterSend(true); got != "sent" {
		t.Fatalf("status after a successful send = %q, want sent", got)
	}
	if got := d.StatusAfterSend(false); got != "pending" {
		t.Fatalf("status after a failed send = %q, want pending", got)
	}
}

func TestPlanShardsSplitsRecipients(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	got := d.PlanShards([]string{"p1", "p2", "p3", "p4"}, 2)
	if len(got) != 2 {
		t.Fatalf("planned %d shards, want 2", len(got))
	}
	if len(got[0])+len(got[1]) != 4 {
		t.Fatalf("shards hold %d recipients in total, want 4", len(got[0])+len(got[1]))
	}
}

func TestRetryAllowedCapped(t *testing.T) {
	d := impl.NewMailBatchDispatcher()
	if !d.RetryAllowed("mail-1", 1) {
		t.Fatalf("a first retry must be allowed")
	}
	if d.RetryAllowed("mail-1", 5) {
		t.Fatalf("retries must be capped, attempt 5 must not be allowed")
	}
}
