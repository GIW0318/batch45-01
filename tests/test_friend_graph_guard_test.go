package task

import (
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-11"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestAddFriendCreatesBidirectionalRelation(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	if got := g.FriendsOf("alice"); len(got) != 1 || got[0] != "bob" {
		t.Fatalf("alice friends = %v, want [bob]", got)
	}
	if got := g.FriendsOf("bob"); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("bob friends = %v, want [alice] after alice added him", got)
	}
}

func TestFriendCountCountsBothDirections(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	if got := g.FriendCount("bob"); got != 1 {
		t.Fatalf("bob friend count = %d, want 1 counted from the reverse edge", got)
	}
	if got := g.FriendCount("nobody"); got != 0 {
		t.Fatalf("stranger friend count = %d, want 0", got)
	}
}

func TestFriendLimitAppliesToBothSides(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.SetFriendLimit(1))
	must(t, g.AddFriend("alice", "bob"))
	if err := g.AddFriend("bob", "carol"); err == nil {
		t.Fatalf("bob already has 1/1 friends, adding carol must be rejected")
	}
}

func TestDuplicateAddFriendKeepsSingleRelation(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	_ = g.AddFriend("alice", "bob")
	if got := g.FriendCount("alice"); got != 1 {
		t.Fatalf("alice friend count = %d after duplicate add, want 1", got)
	}
	if got := g.FriendsOf("bob"); len(got) != 1 {
		t.Fatalf("bob friends = %v after duplicate add, want one entry", got)
	}
}

func TestConcurrentAddFriendCreatesSingleRecord(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	const workers = 16
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = g.AddFriend("alice", "bob")
		}(i)
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		}
	}
	if success == 0 {
		t.Fatalf("no concurrent add succeeded")
	}
	if got := g.FriendCount("alice"); got != 1 {
		t.Fatalf("concurrent adds produced %d relations, want exactly 1", got)
	}
}

func TestSendRequestDeduplicatesPending(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	_, err := g.SendRequest("alice", "bob", time.Hour, now)
	must(t, err)
	_, err = g.SendRequest("alice", "bob", time.Hour, now)
	must(t, err)
	pending := g.PendingRequests("bob", now)
	if len(pending) != 1 {
		t.Fatalf("pending requests = %d, want 1 after a duplicate application", len(pending))
	}
	if pending[0].From != "alice" {
		t.Fatalf("pending request from = %q, want alice", pending[0].From)
	}
}

func TestExpiredRequestCannotBeAccepted(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	req, err := g.SendRequest("alice", "bob", time.Minute, now)
	must(t, err)
	if err := g.AcceptRequest(req.ID, now.Add(2*time.Minute)); err == nil {
		t.Fatalf("expired request must not be accepted")
	}
	req2, err := g.SendRequest("carol", "bob", time.Minute, now)
	must(t, err)
	must(t, g.AcceptRequest(req2.ID, now))
	if !hasString(g.FriendsOf("bob"), "carol") {
		t.Fatalf("valid request was not accepted, bob friends = %v", g.FriendsOf("bob"))
	}
}

func TestAcceptRequestCreatesFriendship(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	req, err := g.SendRequest("alice", "bob", time.Hour, now)
	must(t, err)
	must(t, g.AcceptRequest(req.ID, now.Add(time.Second)))
	if !hasString(g.FriendsOf("alice"), "bob") || !hasString(g.FriendsOf("bob"), "alice") {
		t.Fatalf("accepted request did not create a symmetric friendship: alice=%v bob=%v",
			g.FriendsOf("alice"), g.FriendsOf("bob"))
	}
	if got := g.FriendCount("bob"); got != 1 {
		t.Fatalf("bob friend count = %d, want 1", got)
	}
}

func TestRemoveFriendRemovesBothSides(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	must(t, g.RemoveFriend("alice", "bob"))
	if got := g.FriendsOf("alice"); len(got) != 0 {
		t.Fatalf("alice still has friends after removal: %v", got)
	}
	if got := g.FriendsOf("bob"); len(got) != 0 {
		t.Fatalf("bob still sees alice after removal: %v", got)
	}
	if got := g.FriendCount("bob"); got != 0 {
		t.Fatalf("bob friend count = %d after removal, want 0", got)
	}
}

func TestBlockedPlayerCannotMessage(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	must(t, g.AddFriend("carol", "dave"))
	must(t, g.Block("alice", "bob"))
	if !g.IsBlocked("alice", "bob") {
		t.Fatalf("block was not recorded")
	}
	if g.CanMessage("bob", "alice") {
		t.Fatalf("blocked player must not reach alice")
	}
	if g.CanMessage("alice", "bob") {
		t.Fatalf("alice must not message the player she blocked")
	}
	if !g.CanMessage("carol", "dave") {
		t.Fatalf("unrelated friends must still be able to message each other")
	}
}

func TestPresenceTargetsOnlyFriends(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	must(t, g.AddFriend("carol", "dave"))
	targets := g.PresenceTargets("alice")
	if len(targets) != 1 || targets[0] != "bob" {
		t.Fatalf("presence targets = %v, want only the friend [bob]", targets)
	}
}

func TestRemarkSyncedBothDirections(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	must(t, g.SetRemark("alice", "bob", "bestie"))
	if got := g.Remark("alice", "bob"); got != "bestie" {
		t.Fatalf("alice remark = %q, want bestie", got)
	}
	if got := g.Remark("bob", "alice"); got != "bestie" {
		t.Fatalf("remark was not replicated to the other side, got %q", got)
	}
}

func TestStaleRemoteSyncDoesNotOverwrite(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	must(t, g.SyncFromRemote(impl.Relation{
		PlayerID: "alice", FriendID: "bob", Remark: "new", UpdatedAt: now, Version: 5,
	}))
	must(t, g.SyncFromRemote(impl.Relation{
		PlayerID: "alice", FriendID: "bob", Remark: "old", UpdatedAt: now.Add(-time.Minute), Version: 3,
	}))
	if got := g.Remark("alice", "bob"); got != "new" {
		t.Fatalf("remark = %q after a stale sync, want new", got)
	}
	if got := g.RelationVersion("alice", "bob"); got != 5 {
		t.Fatalf("relation version = %d after a stale sync, want 5", got)
	}
}

func TestTransferPlayerKeepsRelations(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	must(t, g.AddFriend("alice", "bob"))
	must(t, g.TransferPlayer("alice", "server-2"))
	if !hasString(g.FriendsOf("alice"), "bob") {
		t.Fatalf("alice lost bob after the cross server transfer: %v", g.FriendsOf("alice"))
	}
	if got := g.FriendCount("bob"); got != 1 {
		t.Fatalf("bob friend count = %d after alice moved servers, want 1", got)
	}
}

func TestRequestBetweenExistingFriendsRejected(t *testing.T) {
	g := impl.NewFriendGraphGuard()
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	must(t, g.AddFriend("alice", "bob"))
	if _, err := g.SendRequest("alice", "bob", time.Hour, now); err == nil {
		t.Fatalf("a request between existing friends must be rejected")
	}
	if got := g.FriendCount("alice"); got != 1 {
		t.Fatalf("alice friend count = %d, want the single existing relation", got)
	}
}
