package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Message is one chat message as stored in a channel log.
type Message struct {
	Seq       int64
	ServerID  string
	ChannelID string
	From      string
	Body      string
	SentAt    time.Time
}

// ChatFanoutGuard fans messages out to every connection of a chat server.
type ChatFanoutGuard struct {
}

// NewChatFanoutGuard builds a guard for the given server identity.
func NewChatFanoutGuard(serverID string) *ChatFanoutGuard {
	return &ChatFanoutGuard{}
}

// SetSeqStart configures the first channel sequence number of this server.
func (g *ChatFanoutGuard) SetSeqStart(start int64) error {
	return errNotImplemented
}

// ServerID returns the identity used in every stored message.
func (g *ChatFanoutGuard) ServerID() string {
	return ""
}

// RecordKey returns the globally unique key of a message.
func (g *ChatFanoutGuard) RecordKey(m Message) string {
	return ""
}

// Subscribe attaches a connection to a channel.
func (g *ChatFanoutGuard) Subscribe(connID, playerID, channelID string) error {
	return errNotImplemented
}

// Unsubscribe detaches a connection from one channel.
func (g *ChatFanoutGuard) Unsubscribe(connID, channelID string) error {
	return errNotImplemented
}

// SwitchChannel moves a connection to a new channel and drops the old one.
func (g *ChatFanoutGuard) SwitchChannel(connID, channelID string) error {
	return errNotImplemented
}

// ChannelsOf lists the channels a connection currently receives.
func (g *ChatFanoutGuard) ChannelsOf(connID string) []string {
	return nil
}

// CloseConn removes every subscription and queue of a connection.
func (g *ChatFanoutGuard) CloseConn(connID string) error {
	return errNotImplemented
}

// Publish accepts a message, assigns it a channel sequence and stores it.
func (g *ChatFanoutGuard) Publish(channelID, from, body string, at time.Time) (Message, error) {
	return Message{}, errNotImplemented
}

// Forward stores a message replicated from another server, keeping its sequence.
func (g *ChatFanoutGuard) Forward(msg Message, requestID string) error {
	return errNotImplemented
}

// History returns the stored messages of a channel ordered by sequence.
func (g *ChatFanoutGuard) History(channelID string) []Message {
	return nil
}

// Deliver returns the messages a connection still needs after afterSeq.
func (g *ChatFanoutGuard) Deliver(connID string, afterSeq int64) []Message {
	return nil
}

// Mute prevents a player from publishing until the given instant.
func (g *ChatFanoutGuard) Mute(playerID string, until time.Time) error {
	return errNotImplemented
}

// IsMuted reports whether a player is muted at the given instant.
func (g *ChatFanoutGuard) IsMuted(playerID string, at time.Time) bool {
	return false
}

// SetSensitiveWords installs the words rejected at message ingress.
func (g *ChatFanoutGuard) SetSensitiveWords(words []string) error {
	return errNotImplemented
}

// SetMaxBodyLen configures the maximum accepted message length.
func (g *ChatFanoutGuard) SetMaxBodyLen(n int) error {
	return errNotImplemented
}

// SendPrivate appends a private message to a connection private queue.
func (g *ChatFanoutGuard) SendPrivate(connID, from, body string) error {
	return errNotImplemented
}

// SendTeam appends a team message to a connection team queue.
func (g *ChatFanoutGuard) SendTeam(connID, teamID, body string) error {
	return errNotImplemented
}

// PollPrivate drains the private queue of a connection.
func (g *ChatFanoutGuard) PollPrivate(connID string) []string {
	return nil
}

// PollTeam drains the team queue of a connection.
func (g *ChatFanoutGuard) PollTeam(connID string) []string {
	return nil
}
