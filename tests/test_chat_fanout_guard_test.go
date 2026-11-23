package task

import (
	"testing"
	"time"

	impl "gsb/batch45-game-a-13"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func bodies(msgs []impl.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Body)
	}
	return out
}

func TestHistoryOrdersBySequenceNotArrival(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	_, err := g.Publish("world", "alice", "first", now.Add(5*time.Second))
	must(t, err)
	_, err = g.Publish("world", "alice", "second", now)
	must(t, err)
	hist := g.History("world")
	if len(hist) != 2 {
		t.Fatalf("history = %d messages, want 2", len(hist))
	}
	if hist[0].Body != "first" || hist[1].Body != "second" {
		t.Fatalf("history order = %v, want the lower sequence first", bodies(hist))
	}
	if hist[0].Seq >= hist[1].Seq {
		t.Fatalf("sequences not increasing: %d then %d", hist[0].Seq, hist[1].Seq)
	}
}

func TestServersUseDistinctSequenceNamespaces(t *testing.T) {
	g1 := impl.NewChatFanoutGuard("s1")
	g2 := impl.NewChatFanoutGuard("s2")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g1.SetSeqStart(1))
	must(t, g2.SetSeqStart(1))
	m1, err := g1.Publish("world", "alice", "hello", now)
	must(t, err)
	m2, err := g2.Publish("world", "alice", "hello", now)
	must(t, err)
	k1 := g1.RecordKey(m1)
	k2 := g2.RecordKey(m2)
	if k1 == "" || k2 == "" {
		t.Fatalf("record keys must not be empty: %q %q", k1, k2)
	}
	if k1 == k2 {
		t.Fatalf("both servers use the same key %q for their first message", k1)
	}
}

func TestForwardPreservesOriginSequence(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	_, err := g.Publish("world", "alice", "local", now)
	must(t, err)
	remote := impl.Message{Seq: 42, ServerID: "s2", ChannelID: "world", From: "bob", Body: "remote", SentAt: now}
	must(t, g.Forward(remote, "req-1"))
	hist := g.History("world")
	if len(hist) != 2 {
		t.Fatalf("history = %d messages, want 2", len(hist))
	}
	if last := hist[len(hist)-1]; last.Seq != 42 || last.Body != "remote" {
		t.Fatalf("forwarded message = %+v, want sequence 42 preserved", last)
	}
}

func TestDuplicateGatewayForwardStoredOnce(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	remote := impl.Message{Seq: 7, ServerID: "s2", ChannelID: "world", From: "bob", Body: "only once", SentAt: now}
	must(t, g.Forward(remote, "gw-request-9"))
	must(t, g.Forward(remote, "gw-request-9"))
	if got := len(g.History("world")); got != 1 {
		t.Fatalf("history = %d messages after a duplicate gateway forward, want 1", got)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.Subscribe("conn-1", "alice", "world"))
	_, err := g.Publish("world", "bob", "before", now)
	must(t, err)
	if got := g.Deliver("conn-1", 0); len(got) != 1 {
		t.Fatalf("delivered = %d before unsubscribe, want 1", len(got))
	}
	must(t, g.Unsubscribe("conn-1", "world"))
	_, err = g.Publish("world", "bob", "after", now.Add(time.Second))
	must(t, err)
	if got := g.Deliver("conn-1", 0); len(got) != 0 {
		t.Fatalf("delivered = %d after unsubscribe, want 0", len(got))
	}
}

func TestMuteAppliesToEveryChannel(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.Mute("troll", now.Add(time.Hour)))
	if !g.IsMuted("troll", now) {
		t.Fatalf("mute was not recorded")
	}
	if _, err := g.Publish("world", "troll", "hi", now); err == nil {
		t.Fatalf("muted player still published to world")
	}
	if _, err := g.Publish("guild", "troll", "hi", now); err == nil {
		t.Fatalf("muted player still published to guild")
	}
	_, err := g.Publish("world", "alice", "hi", now)
	must(t, err)
}

func TestSensitiveFilterRunsBeforeStore(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.SetSensitiveWords([]string{"badword"}))
	if _, err := g.Publish("world", "alice", "a badword here", now); err == nil {
		t.Fatalf("message with a sensitive word must be rejected")
	}
	if got := len(g.History("world")); got != 0 {
		t.Fatalf("rejected message reached the store: %d stored", got)
	}
	_, err := g.Publish("world", "alice", "clean message", now)
	must(t, err)
	if got := len(g.History("world")); got != 1 {
		t.Fatalf("clean message stored = %d, want 1", got)
	}
}

func TestPrivateAndTeamQueuesDoNotEvictEachOther(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	for i := 0; i < 5; i++ {
		must(t, g.SendPrivate("conn-1", "alice", "p"))
	}
	for i := 0; i < 5; i++ {
		must(t, g.SendTeam("conn-1", "team-1", "t"))
	}
	private := g.PollPrivate("conn-1")
	team := g.PollTeam("conn-1")
	if len(private) != 5 {
		t.Fatalf("private queue = %d messages, want 5", len(private))
	}
	if len(team) != 5 {
		t.Fatalf("team queue = %d messages, want 5", len(team))
	}
	if private[0] != "p" || team[0] != "t" {
		t.Fatalf("queues were mixed: private=%v team=%v", private, team)
	}
}

func TestLargeChannelHistoryNotTruncated(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.Subscribe("conn-1", "alice", "world"))
	for i := 0; i < 200; i++ {
		_, err := g.Publish("world", "alice", "msg", now.Add(time.Duration(i)*time.Millisecond))
		must(t, err)
	}
	if got := len(g.History("world")); got != 200 {
		t.Fatalf("history = %d messages, want all 200", got)
	}
	if got := len(g.Deliver("conn-1", 0)); got != 200 {
		t.Fatalf("delivered = %d messages, want all 200", got)
	}
}

func TestReconnectBackfillDeduplicated(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.Subscribe("conn-1", "alice", "world"))
	for i := 0; i < 5; i++ {
		_, err := g.Publish("world", "alice", "m", now.Add(time.Duration(i)*time.Millisecond))
		must(t, err)
	}
	backfill := g.Deliver("conn-1", 2)
	if len(backfill) != 3 {
		t.Fatalf("backfill = %d messages after sequence 2, want 3", len(backfill))
	}
	for i, msg := range backfill {
		if msg.Seq != int64(i+3) {
			t.Fatalf("backfill[%d] seq = %d, want %d", i, msg.Seq, i+3)
		}
	}
	_, err := g.Publish("world", "alice", "live", now.Add(10*time.Second))
	must(t, err)
	merged := g.Deliver("conn-1", 1)
	if len(merged) != 5 {
		t.Fatalf("merged delivery = %d messages, want 5 unique messages", len(merged))
	}
	for i, msg := range merged {
		if msg.Seq != int64(i+2) {
			t.Fatalf("merged[%d] seq = %d, want %d in order", i, msg.Seq, i+2)
		}
	}
}

func TestSwitchChannelDropsOldSubscription(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.Subscribe("conn-1", "alice", "world"))
	_, err := g.Publish("world", "bob", "world message", now)
	must(t, err)
	must(t, g.SwitchChannel("conn-1", "guild"))
	channels := g.ChannelsOf("conn-1")
	if len(channels) != 1 || channels[0] != "guild" {
		t.Fatalf("channels after switch = %v, want only guild", channels)
	}
	_, err = g.Publish("guild", "bob", "guild message", now.Add(time.Second))
	must(t, err)
	_, err = g.Publish("world", "bob", "more world", now.Add(2*time.Second))
	must(t, err)
	delivered := g.Deliver("conn-1", 0)
	if len(delivered) != 1 || delivered[0].Body != "guild message" {
		t.Fatalf("delivered after switch = %v, want only the guild message", bodies(delivered))
	}
}

func TestOversizedMessageRejected(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.SetMaxBodyLen(8))
	if _, err := g.Publish("world", "alice", "123456789", now); err == nil {
		t.Fatalf("message longer than the cap must be rejected")
	}
	_, err := g.Publish("world", "alice", "12345678", now)
	must(t, err)
	if got := len(g.History("world")); got != 1 {
		t.Fatalf("stored = %d messages, want only the accepted one", got)
	}
}

func TestSequenceStrictlyIncreasingPerChannel(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.SetSeqStart(100))
	for i := 0; i < 4; i++ {
		_, err := g.Publish("world", "alice", "m", now)
		must(t, err)
	}
	hist := g.History("world")
	if len(hist) != 4 {
		t.Fatalf("history = %d messages, want 4", len(hist))
	}
	for i, msg := range hist {
		if msg.Seq != int64(100+i) {
			t.Fatalf("message %d seq = %d, want %d", i, msg.Seq, 100+i)
		}
	}
}

func TestCloseConnCleansSubscriptions(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.Subscribe("conn-1", "alice", "world"))
	must(t, g.Subscribe("conn-1", "alice", "guild"))
	if got := len(g.ChannelsOf("conn-1")); got != 2 {
		t.Fatalf("channels before close = %d, want 2", got)
	}
	must(t, g.CloseConn("conn-1"))
	if got := len(g.ChannelsOf("conn-1")); got != 0 {
		t.Fatalf("channels after close = %d, want 0", got)
	}
	_, err := g.Publish("world", "bob", "after close", now)
	must(t, err)
	if got := g.Deliver("conn-1", 0); len(got) != 0 {
		t.Fatalf("closed connection still receives %d messages", len(got))
	}
}

func TestDuplicateSubscribeDeliversOnce(t *testing.T) {
	g := impl.NewChatFanoutGuard("s1")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	must(t, g.Subscribe("conn-1", "alice", "world"))
	must(t, g.Subscribe("conn-1", "alice", "world"))
	_, err := g.Publish("world", "bob", "once", now)
	must(t, err)
	if got := g.Deliver("conn-1", 0); len(got) != 1 {
		t.Fatalf("delivered = %d, want exactly 1 despite the duplicate subscribe", len(got))
	}
}
