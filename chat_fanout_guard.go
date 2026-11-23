package task

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxHistoryPerChannel = 4096
	maxQueuePerConn      = 1024
)

var (
	errInvalidInput  = errors.New("invalid input")
	errMuted         = errors.New("player is muted")
	errSensitiveWord = errors.New("message contains a sensitive word")
	errBodyTooLong   = errors.New("message body is too long")
	errQueueFull     = errors.New("connection queue is full")
)

// Message is one chat message as stored in a channel log.
type Message struct {
	Seq       int64
	ServerID  string
	ChannelID string
	From      string
	Body      string
	SentAt    time.Time
}

type channelState struct {
	history []Message
	keys    map[string]struct{}
	nextSeq int64
}

type connState struct {
	playerID string

	channels  map[string]struct{}
	queue     []Message
	queueKeys map[string]struct{}

	private []string
	team    []string
}

// ChatFanoutGuard fans messages out to every connection of a chat server.
type ChatFanoutGuard struct {
	mu sync.RWMutex

	serverID  string
	seqStart  int64
	maxBody   int
	sensitive []string
	mutes     map[string]time.Time

	channels  map[string]*channelState
	conns     map[string]*connState
	forwarded map[string]struct{}
}

// NewChatFanoutGuard builds a guard for the given server identity.
func NewChatFanoutGuard(serverID string) *ChatFanoutGuard {
	return &ChatFanoutGuard{
		serverID:  serverID,
		seqStart:  1,
		mutes:     make(map[string]time.Time),
		channels:  make(map[string]*channelState),
		conns:     make(map[string]*connState),
		forwarded: make(map[string]struct{}),
	}
}

// SetSeqStart configures the first channel sequence number of this server.
func (g *ChatFanoutGuard) SetSeqStart(start int64) error {
	if start < 0 {
		return fmt.Errorf("%w: sequence start must not be negative", errInvalidInput)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.seqStart = start
	for _, ch := range g.channels {
		if ch.nextSeq < start {
			ch.nextSeq = start
		}
	}
	return nil
}

// ServerID returns the identity used in every stored message.
func (g *ChatFanoutGuard) ServerID() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.serverID
}

// RecordKey returns the globally unique key of a message.
func (g *ChatFanoutGuard) RecordKey(m Message) string {
	return g.recordKey(m)
}

func (g *ChatFanoutGuard) recordKey(m Message) string {
	serverID := m.ServerID
	if serverID == "" {
		serverID = g.serverID
	}
	return fmt.Sprintf("%s/%s/%d", serverID, m.ChannelID, m.Seq)
}

// Subscribe attaches a connection to a channel.
func (g *ChatFanoutGuard) Subscribe(connID, playerID, channelID string) error {
	if !validArg(connID) || !validArg(playerID) || !validArg(channelID) {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	c := g.conns[connID]
	if c == nil {
		c = newConnState(playerID)
		g.conns[connID] = c
	} else if c.playerID == "" {
		c.playerID = playerID
	} else if c.playerID != playerID {
		return fmt.Errorf("%w: connection belongs to another player", errInvalidInput)
	}

	c.channels[channelID] = struct{}{}
	return nil
}

// Unsubscribe detaches a connection from one channel.
func (g *ChatFanoutGuard) Unsubscribe(connID, channelID string) error {
	if !validArg(connID) || !validArg(channelID) {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	c := g.conns[connID]
	if c == nil {
		return nil
	}

	delete(c.channels, channelID)
	g.dropQueuedChannelLocked(c, channelID)
	return nil
}

// SwitchChannel moves a connection to a new channel and drops the old one.
func (g *ChatFanoutGuard) SwitchChannel(connID, channelID string) error {
	if !validArg(connID) || !validArg(channelID) {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	c := g.conns[connID]
	if c == nil {
		return fmt.Errorf("%w: unknown connection", errInvalidInput)
	}

	c.channels = map[string]struct{}{channelID: {}}
	g.retainQueuedChannelLocked(c, channelID)
	return nil
}

// ChannelsOf lists the channels a connection currently receives.
func (g *ChatFanoutGuard) ChannelsOf(connID string) []string {
	if !validArg(connID) {
		return nil
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	c := g.conns[connID]
	if c == nil {
		return nil
	}

	channels := make([]string, 0, len(c.channels))
	for channelID := range c.channels {
		channels = append(channels, channelID)
	}
	sort.Strings(channels)
	return channels
}

// CloseConn removes every subscription and queue of a connection.
func (g *ChatFanoutGuard) CloseConn(connID string) error {
	if !validArg(connID) {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.conns, connID)
	return nil
}

// Publish accepts a message, assigns it a channel sequence and stores it.
func (g *ChatFanoutGuard) Publish(channelID, from, body string, at time.Time) (Message, error) {
	if !validArg(channelID) || !validArg(from) || body == "" {
		return Message{}, errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.isMutedLocked(from, at) {
		return Message{}, errMuted
	}
	if err := g.validateBodyLocked(body); err != nil {
		return Message{}, err
	}

	ch := g.channelLocked(channelID)
	msg := Message{
		Seq:       ch.nextSeq,
		ServerID:  g.serverID,
		ChannelID: channelID,
		From:      from,
		Body:      body,
		SentAt:    at,
	}
	if !g.storeLocked(ch, msg) {
		return Message{}, fmt.Errorf("%w: sequence already exists", errInvalidInput)
	}

	g.fanoutLocked(msg)
	return msg, nil
}

// Forward stores a message replicated from another server, keeping its sequence.
func (g *ChatFanoutGuard) Forward(msg Message, requestID string) error {
	if !validArg(requestID) || !validArg(msg.ServerID) || !validArg(msg.ChannelID) ||
		!validArg(msg.From) || msg.Body == "" || msg.Seq < 0 {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.forwarded[requestID]; ok {
		return nil
	}

	ch := g.channels[msg.ChannelID]
	if ch != nil {
		if _, ok := ch.keys[g.recordKey(msg)]; ok {
			g.forwarded[requestID] = struct{}{}
			return nil
		}
	}

	if g.isMutedLocked(msg.From, msg.SentAt) {
		return errMuted
	}
	if err := g.validateBodyLocked(msg.Body); err != nil {
		return err
	}

	ch = g.channelLocked(msg.ChannelID)
	if _, ok := ch.keys[g.recordKey(msg)]; ok {
		g.forwarded[requestID] = struct{}{}
		return nil
	}

	if !g.storeLocked(ch, msg) {
		return fmt.Errorf("%w: duplicate message key", errInvalidInput)
	}
	g.forwarded[requestID] = struct{}{}
	g.fanoutLocked(msg)
	return nil
}

// History returns the stored messages of a channel ordered by sequence.
func (g *ChatFanoutGuard) History(channelID string) []Message {
	if !validArg(channelID) {
		return nil
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	ch := g.channels[channelID]
	if ch == nil {
		return nil
	}

	history := make([]Message, len(ch.history))
	copy(history, ch.history)
	return history
}

// Deliver returns the messages a connection still needs after afterSeq.
func (g *ChatFanoutGuard) Deliver(connID string, afterSeq int64) []Message {
	if !validArg(connID) || afterSeq < 0 {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	c := g.conns[connID]
	if c == nil {
		return nil
	}

	seen := make(map[string]struct{})
	delivered := make([]Message, 0)

	for channelID := range c.channels {
		ch := g.channels[channelID]
		if ch == nil {
			continue
		}
		for _, msg := range ch.history {
			if msg.Seq <= afterSeq {
				continue
			}
			key := g.recordKey(msg)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			delivered = append(delivered, msg)
		}
	}

	for _, msg := range c.queue {
		if msg.Seq <= afterSeq {
			continue
		}
		if _, ok := c.channels[msg.ChannelID]; !ok {
			continue
		}
		key := g.recordKey(msg)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		delivered = append(delivered, msg)
	}

	sort.Slice(delivered, func(i, j int) bool {
		if delivered[i].Seq != delivered[j].Seq {
			return delivered[i].Seq < delivered[j].Seq
		}
		if delivered[i].ChannelID != delivered[j].ChannelID {
			return delivered[i].ChannelID < delivered[j].ChannelID
		}
		return delivered[i].ServerID < delivered[j].ServerID
	})

	// The returned set acknowledges everything up to afterSeq as well, so the
	// live backlog can be released without affecting history-based backfill.
	c.queue = nil
	c.queueKeys = make(map[string]struct{})
	return delivered
}

// Mute prevents a player from publishing until the given instant.
func (g *ChatFanoutGuard) Mute(playerID string, until time.Time) error {
	if !validArg(playerID) {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if until.IsZero() {
		delete(g.mutes, playerID)
		return nil
	}
	g.mutes[playerID] = until
	return nil
}

// IsMuted reports whether a player is muted at the given instant.
func (g *ChatFanoutGuard) IsMuted(playerID string, at time.Time) bool {
	if !validArg(playerID) {
		return false
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.isMutedLocked(playerID, at)
}

func (g *ChatFanoutGuard) isMutedLocked(playerID string, at time.Time) bool {
	until, ok := g.mutes[playerID]
	return ok && at.Before(until)
}

// SetSensitiveWords installs the words rejected at message ingress.
func (g *ChatFanoutGuard) SetSensitiveWords(words []string) error {
	copied := make([]string, 0, len(words))
	for _, word := range words {
		if word == "" {
			return fmt.Errorf("%w: sensitive word must not be empty", errInvalidInput)
		}
		copied = append(copied, word)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.sensitive = copied
	return nil
}

// SetMaxBodyLen configures the maximum accepted message length.
func (g *ChatFanoutGuard) SetMaxBodyLen(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: max body length must not be negative", errInvalidInput)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.maxBody = n
	return nil
}

// SendPrivate appends a private message to a connection private queue.
func (g *ChatFanoutGuard) SendPrivate(connID, from, body string) error {
	if !validArg(connID) || !validArg(from) || body == "" {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.validateBodyLocked(body); err != nil {
		return err
	}

	c := g.connLocked(connID)
	if len(c.private) >= maxQueuePerConn {
		delete(g.conns, connID)
		return errQueueFull
	}
	c.private = append(c.private, body)
	return nil
}

// SendTeam appends a team message to a connection team queue.
func (g *ChatFanoutGuard) SendTeam(connID, teamID, body string) error {
	if !validArg(connID) || !validArg(teamID) || body == "" {
		return errInvalidInput
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.validateBodyLocked(body); err != nil {
		return err
	}

	c := g.connLocked(connID)
	if len(c.team) >= maxQueuePerConn {
		delete(g.conns, connID)
		return errQueueFull
	}
	c.team = append(c.team, body)
	return nil
}

// PollPrivate drains the private queue of a connection.
func (g *ChatFanoutGuard) PollPrivate(connID string) []string {
	if !validArg(connID) {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	c := g.conns[connID]
	if c == nil || len(c.private) == 0 {
		return nil
	}
	private := c.private
	c.private = nil
	return private
}

// PollTeam drains the team queue of a connection.
func (g *ChatFanoutGuard) PollTeam(connID string) []string {
	if !validArg(connID) {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	c := g.conns[connID]
	if c == nil || len(c.team) == 0 {
		return nil
	}
	team := c.team
	c.team = nil
	return team
}

func newConnState(playerID string) *connState {
	return &connState{
		playerID:  playerID,
		channels:  make(map[string]struct{}),
		queueKeys: make(map[string]struct{}),
	}
}

func (g *ChatFanoutGuard) connLocked(connID string) *connState {
	c := g.conns[connID]
	if c == nil {
		c = newConnState("")
		g.conns[connID] = c
	}
	return c
}

func (g *ChatFanoutGuard) channelLocked(channelID string) *channelState {
	ch := g.channels[channelID]
	if ch == nil {
		ch = &channelState{
			keys:    make(map[string]struct{}),
			nextSeq: g.seqStart,
		}
		g.channels[channelID] = ch
	}
	return ch
}

func (g *ChatFanoutGuard) storeLocked(ch *channelState, msg Message) bool {
	key := g.recordKey(msg)
	if _, ok := ch.keys[key]; ok {
		return false
	}

	index := sort.Search(len(ch.history), func(i int) bool {
		return ch.history[i].Seq > msg.Seq
	})
	ch.history = append(ch.history, Message{})
	copy(ch.history[index+1:], ch.history[index:])
	ch.history[index] = msg
	ch.keys[key] = struct{}{}

	if msg.Seq >= ch.nextSeq {
		ch.nextSeq = msg.Seq + 1
	}

	if len(ch.history) > maxHistoryPerChannel {
		evicted := ch.history[0]
		copy(ch.history, ch.history[1:])
		ch.history[len(ch.history)-1] = Message{}
		ch.history = ch.history[:len(ch.history)-1]
		delete(ch.keys, g.recordKey(evicted))
	}
	return true
}

func (g *ChatFanoutGuard) fanoutLocked(msg Message) {
	key := g.recordKey(msg)
	var kicked []string

	for connID, c := range g.conns {
		if _, subscribed := c.channels[msg.ChannelID]; !subscribed {
			continue
		}
		if _, queued := c.queueKeys[key]; queued {
			continue
		}

		c.queue = append(c.queue, msg)
		c.queueKeys[key] = struct{}{}
		if len(c.queue) > maxQueuePerConn {
			kicked = append(kicked, connID)
		}
	}

	for _, connID := range kicked {
		delete(g.conns, connID)
	}
}

func (g *ChatFanoutGuard) validateBodyLocked(body string) error {
	for _, word := range g.sensitive {
		if strings.Contains(body, word) {
			return errSensitiveWord
		}
	}
	if g.maxBody > 0 && utf8.RuneCountInString(body) > g.maxBody {
		return errBodyTooLong
	}
	return nil
}

func (g *ChatFanoutGuard) dropQueuedChannelLocked(c *connState, channelID string) {
	queue := make([]Message, 0, len(c.queue))
	keys := make(map[string]struct{}, len(c.queueKeys))
	for _, msg := range c.queue {
		if msg.ChannelID == channelID {
			continue
		}
		queue = append(queue, msg)
		keys[g.recordKey(msg)] = struct{}{}
	}
	c.queue = queue
	c.queueKeys = keys
}

func (g *ChatFanoutGuard) retainQueuedChannelLocked(c *connState, channelID string) {
	queue := make([]Message, 0, len(c.queue))
	keys := make(map[string]struct{}, len(c.queueKeys))
	for _, msg := range c.queue {
		if msg.ChannelID != channelID {
			continue
		}
		queue = append(queue, msg)
		keys[g.recordKey(msg)] = struct{}{}
	}
	c.queue = queue
	c.queueKeys = keys
}

func validArg(value string) bool {
	return strings.TrimSpace(value) != ""
}
