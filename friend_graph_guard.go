package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Relation is one directed edge of the friend graph as replicated between servers.
type Relation struct {
	PlayerID  string
	FriendID  string
	Remark    string
	UpdatedAt time.Time
	Version   int64
}

// FriendRequest is a pending friend application.
type FriendRequest struct {
	ID        string
	From      string
	To        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// FriendGraphGuard keeps friendships symmetric and consistent across servers.
type FriendGraphGuard struct {
}

// NewFriendGraphGuard builds an empty friend graph with the default friend limit.
func NewFriendGraphGuard() *FriendGraphGuard {
	return &FriendGraphGuard{}
}

// SetFriendLimit configures the maximum number of friends one player may have.
func (g *FriendGraphGuard) SetFriendLimit(limit int) error {
	return errNotImplemented
}

// FriendLimit reports the configured friend limit.
func (g *FriendGraphGuard) FriendLimit() int {
	return 0
}

// AddFriend creates a symmetric friendship between a and b.
func (g *FriendGraphGuard) AddFriend(a, b string) error {
	return errNotImplemented
}

// RemoveFriend deletes the friendship between a and b from both sides.
func (g *FriendGraphGuard) RemoveFriend(a, b string) error {
	return errNotImplemented
}

// FriendsOf lists the friend ids of player.
func (g *FriendGraphGuard) FriendsOf(player string) []string {
	return nil
}

// FriendCount reports how many friends player has, counting both directions.
func (g *FriendGraphGuard) FriendCount(player string) int {
	return 0
}

// SendRequest creates a friend request from from to to that expires after ttl.
func (g *FriendGraphGuard) SendRequest(from, to string, ttl time.Duration, now time.Time) (FriendRequest, error) {
	return FriendRequest{}, errNotImplemented
}

// PendingRequests lists unexpired incoming requests for to.
func (g *FriendGraphGuard) PendingRequests(to string, now time.Time) []FriendRequest {
	return nil
}

// AcceptRequest accepts the request with the given id.
func (g *FriendGraphGuard) AcceptRequest(id string, now time.Time) error {
	return errNotImplemented
}

// Block makes a block b, which also stops message delivery.
func (g *FriendGraphGuard) Block(a, b string) error {
	return errNotImplemented
}

// IsBlocked reports whether a blocked b.
func (g *FriendGraphGuard) IsBlocked(a, b string) bool {
	return false
}

// CanMessage reports whether from may send a message to to.
func (g *FriendGraphGuard) CanMessage(from, to string) bool {
	return false
}

// SetRemark stores the remark owner keeps for friend.
func (g *FriendGraphGuard) SetRemark(owner, friend, remark string) error {
	return errNotImplemented
}

// Remark returns the remark owner keeps for friend.
func (g *FriendGraphGuard) Remark(owner, friend string) string {
	return ""
}

// SyncFromRemote merges one relation replicated from another server.
func (g *FriendGraphGuard) SyncFromRemote(r Relation) error {
	return errNotImplemented
}

// RelationVersion returns the stored replication version for the pair a,b.
func (g *FriendGraphGuard) RelationVersion(a, b string) int64 {
	return 0
}

// TransferPlayer moves player to another server, keeping the graph intact.
func (g *FriendGraphGuard) TransferPlayer(player, newServer string) error {
	return errNotImplemented
}

// PresenceTargets lists the players that must receive player's presence update.
func (g *FriendGraphGuard) PresenceTargets(player string) []string {
	return nil
}
