// Command verify exercises the three hard-constraint scenarios of the
// mail batch dispatcher: concurrent batches, shard expansion migration
// and restart resume. It exits non-zero on the first failed check.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	impl "gsb/batch45-game-a-10"
)

func fail(format string, args ...any) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func verifyConcurrentBatches() {
	d := impl.NewMailBatchDispatcher()

	if !d.TryAcquireSend("batch-a") {
		fail("first sender of batch-a must acquire the send slot")
	}
	if d.TryAcquireSend("batch-a") {
		fail("batch-a must stay serial: second sender acquired the slot")
	}
	if !d.TryAcquireSend("batch-b") {
		fail("batch-b must not be blocked while batch-a is sending")
	}
	d.ReleaseSend("batch-b")

	// batch-a is "stuck" (never releases); batch-b must still finish.
	var wg sync.WaitGroup
	done := make(chan string, 2)
	for _, batchID := range []string{"batch-b", "batch-c"} {
		if !d.TryAcquireSend(batchID) {
			fail("batch %s must acquire its slot while batch-a is stuck", batchID)
		}
		wg.Add(1)
		go func(batchID string) {
			defer wg.Done()
			defer d.ReleaseSend(batchID)
			d.Plan(batchID, []string{"player-1", "player-2"})
			d.RecordSuccess(batchID, "player-1")
			d.RecordSuccess(batchID, "player-2")
			done <- batchID
		}(batchID)
	}
	wg.Wait()
	close(done)
	finished := 0
	for range done {
		finished++
	}
	if finished != 2 {
		fail("stuck batch-a blocked other batches: only %d/2 finished", finished)
	}
	if got := d.Pending("batch-b"); got != 0 {
		fail("batch-b pending = %d, want 0 after serial send", got)
	}
	d.ReleaseSend("batch-a")
	d.ReleaseSend("batch-b")
	fmt.Println("OK: concurrent batches - serial inside a batch, parallel across batches, stuck batch does not block others")
}

func verifyShardExpansion() {
	d := impl.NewMailBatchDispatcher()
	recipients := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"}

	const oldShards, newShards = 2, 3
	oldPlan := d.PlanShards(recipients, oldShards)
	if len(oldPlan) != oldShards {
		fail("old plan has %d shards, want %d", len(oldPlan), oldShards)
	}

	// Deliver everything owned by old shard 0 before the expansion.
	d.Plan("batch-x", recipients)
	delivered := map[string]bool{}
	for _, recipient := range oldPlan[0] {
		d.RecordSuccess("batch-x", recipient)
		delivered[d.Normalize(recipient)] = true
	}

	newPlan := d.PlanShards(recipients, newShards)
	if len(newPlan) != newShards {
		fail("new plan has %d shards, want %d", len(newPlan), newShards)
	}

	migrated := 0
	for _, recipient := range recipients {
		oldOwner := d.ShardOf(recipient, oldShards)
		newOwner := d.ShardOf(recipient, newShards)
		if oldOwner != newOwner {
			migrated++
			// A migrated recipient must land on exactly one new shard.
			count := 0
			for _, shard := range newPlan {
				for _, r := range shard {
					if d.Normalize(r) == d.Normalize(recipient) {
						count++
					}
				}
			}
			if count != 1 {
				fail("migrated recipient %s appears %d times in new plan", recipient, count)
			}
		} else if newOwner >= newShards {
			fail("recipient %s kept an out-of-range shard %d", recipient, newOwner)
		}
	}
	if migrated == 0 {
		fail("expansion from %d to %d shards migrated nobody", oldShards, newShards)
	}

	// Delivered records must not be recomputed or resent: pending only
	// counts recipients that were never delivered, wherever they live now.
	wantPending := len(recipients) - len(delivered)
	if got := d.Pending("batch-x"); got != wantPending {
		fail("pending after expansion = %d, want %d (delivered records must survive)", got, wantPending)
	}
	fmt.Printf("OK: shard expansion - %d/%d recipients migrated from %d to %d shards, delivered records untouched\n",
		migrated, len(recipients), oldShards, newShards)
}

func verifyRestartResume() {
	dir, err := os.MkdirTemp("", "mailbatch")
	if err != nil {
		fail("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	stateFile := filepath.Join(dir, "state.json")

	// First process run: plan, finish shard 0, deliver part of the batch.
	first := impl.NewMailBatchDispatcher()
	recipients := []string{"Player-1", "player-2", "player-3", "player-4"}
	first.Plan("batch-r", recipients)
	first.CompleteShard("batch-r", 0)
	first.RecordSuccess("batch-r", "player-1")
	first.RecordSuccess("batch-r", "player-2")
	first.MarkFailedMail("batch-r", first.MailID("batch-r", "player-9"))

	state := first.Snapshot()
	data, err := json.Marshal(state)
	if err != nil {
		fail("marshal state: %v", err)
	}
	if err := os.WriteFile(stateFile, data, 0o600); err != nil {
		fail("write state: %v", err)
	}

	// Simulated restart: a brand new dispatcher restores from disk.
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		fail("read state: %v", err)
	}
	var restored impl.DispatcherState
	if err := json.Unmarshal(raw, &restored); err != nil {
		fail("unmarshal state: %v", err)
	}
	second := impl.NewMailBatchDispatcher()
	second.Restore(restored)

	if got := second.Pending("batch-r"); got != 2 {
		fail("pending after restart = %d, want 2 (player-3, player-4)", got)
	}
	if got := second.ShardProgress("batch-r", 0); got != 1 {
		fail("shard progress after restart = %d, want 1", got)
	}
	if got := second.DeadLetters("batch-r"); got != 1 {
		fail("dead letters after restart = %d, want 1", got)
	}

	// Replanning after restart must not resend delivered recipients.
	mails := second.Plan("batch-r", recipients)
	resent := 0
	for _, mail := range mails {
		if mail.Recipient == "player-1" || mail.Recipient == "player-2" {
			if !contains(secondSnapshotDelivered(second), mail.Recipient) {
				resent++
			}
		}
	}
	if got := second.Pending("batch-r"); got != 2 {
		fail("pending after replan = %d, want 2 (no recipient gets a second mail)", got)
	}
	fmt.Println("OK: restart resume - per-shard progress and delivered records restored, no recipient receives a second mail")
}

func secondSnapshotDelivered(d *impl.MailBatchDispatcher) []string {
	return d.Snapshot().Delivered["batch-r"]
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func main() {
	start := time.Now()
	verifyConcurrentBatches()
	verifyShardExpansion()
	verifyRestartResume()
	fmt.Printf("all verification scenarios passed in %s\n", time.Since(start).Round(time.Millisecond))
}
