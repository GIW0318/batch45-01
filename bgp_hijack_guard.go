// Package task implements the inbound BGP admission control of a border
// router that speaks RPKI.
//
// A HijackGuard validates every announcement before it may reach the
// forwarding table. The guard is deliberately stateful: it keeps the local
// RPKI cache, the installed RIB, a per-prefix flap penalty and the newest
// sequence number per prefix, so that session resets, replays and process
// restarts cannot resurrect routes that were never authorised.
package task

import "errors"

// RPKIState is the origin validation state produced by the RPKI validator.
type RPKIState int

const (
	// RPKIUnknown means no validation was performed.
	RPKIUnknown RPKIState = iota
	// RPKIValid means a ROA covers the prefix and authorises the origin AS.
	RPKIValid
	// RPKIInvalid means a ROA covers the prefix but not the origin AS.
	RPKIInvalid
	// RPKINotFound means no ROA covers the prefix.
	RPKINotFound
)

// RejectReason is the stable code recorded for every announcement the guard
// refuses to install.
type RejectReason string

const (
	// ReasonRPKIInvalid records an announcement whose validator verdict is
	// invalid, or whose origin AS is not authorised by the local RPKI cache.
	ReasonRPKIInvalid RejectReason = "HIJACK_RPKI_INVALID"
	// ReasonMOASConflict records a second origin for a prefix that already
	// has a primary route with at least the same priority.
	ReasonMOASConflict RejectReason = "HIJACK_MOAS_CONFLICT"
	// ReasonDefaultDenied records a default route that was not explicitly
	// allowed by the configuration.
	ReasonDefaultDenied RejectReason = "HIJACK_DEFAULT_DENIED"
	// ReasonPrivateAS records a path that contains a private ASN. Private
	// ASNs are 64512-65534 and 4200000000-4294967294.
	ReasonPrivateAS RejectReason = "HIJACK_PRIVATE_AS"
	// ReasonASSet records a path built from an AS_SET instead of a sequence.
	ReasonASSet RejectReason = "HIJACK_AS_SET"
	// ReasonCommunity records a community that is not on the whitelist.
	ReasonCommunity RejectReason = "HIJACK_COMMUNITY"
	// ReasonPathTooLong records a path whose length, prepends included, is
	// larger than GuardConfig.MaxASPathLen.
	ReasonPathTooLong RejectReason = "HIJACK_AS_PATH_TOO_LONG"
	// ReasonDamped records an announcement for a flap-damped prefix.
	ReasonDamped RejectReason = "HIJACK_DAMPED"
	// ReasonStaleUpdate records an announcement older than the installed one.
	ReasonStaleUpdate RejectReason = "HIJACK_STALE_UPDATE"
)

// ROA is one Route Origin Authorisation taken from the local RPKI cache.
type ROA struct {
	// Prefix is the authorised prefix in CIDR notation.
	Prefix string
	// MaxLen is the longest prefix length the ROA authorises.
	MaxLen int
	// OriginAS is the only origin AS authorised for Prefix up to MaxLen.
	OriginAS uint32
}

// GuardConfig is the static policy applied to every announcement.
type GuardConfig struct {
	// ROAs is the local RPKI cache used to re-validate the origin AS of
	// every announcement, independently of the validator verdict.
	ROAs []ROA
	// CommunityWhitelist lists the communities a route may carry. A route
	// carrying any community outside the list is rejected. An empty
	// whitelist disables the check.
	CommunityWhitelist []uint32
	// AllowDefaultRoute must be true before 0.0.0.0/0 may be installed.
	AllowDefaultRoute bool
	// MaxASPathLen bounds len(ASPath)+Prepend. Zero disables the check.
	MaxASPathLen int
	// FlapThreshold is the number of withdraws after which a prefix is
	// damped. Zero disables damping.
	FlapThreshold int
}

// Announcement is one route advertisement received from a peer.
type Announcement struct {
	// Peer identifies the BGP session the announcement arrived on.
	Peer string
	// Prefix is the announced prefix in CIDR notation.
	Prefix string
	// NextHop is the next hop carried by the announcement.
	NextHop string
	// OriginAS is the origin ASN carried in the AS_PATH.
	OriginAS uint32
	// ASPath is the AS sequence from the peer to the origin, origin last.
	ASPath []uint32
	// ASSet marks an announcement whose path is an AS_SET, not a sequence.
	ASSet bool
	// Prepend is the number of AS prepends applied on top of ASPath.
	Prepend int
	// Communities are the communities carried by the announcement.
	Communities []uint32
	// RPKI is the verdict produced by the RPKI validator.
	RPKI RPKIState
	// Priority ranks competing origins of the same prefix; larger wins.
	// A conflicting announcement from another origin replaces the installed
	// route only when its Priority is strictly larger than the installed
	// route's priority; otherwise it is rejected as a MOAS conflict.
	Priority int
	// Seq orders announcements for the same prefix; larger is newer. An
	// announcement whose Seq is not newer than the installed one for the
	// same prefix is rejected and never overwrites the newer route.
	Seq uint64
}

// Route is the forwarding entry installed for a prefix.
type Route struct {
	// Prefix is the installed prefix in CIDR notation.
	Prefix string
	// NextHop is the installed next hop.
	NextHop string
	// OriginAS is the origin ASN that was validated at install time.
	OriginAS uint32
	// Peer is the session the route was learned from.
	Peer string
	// Priority is the priority carried by the winning announcement.
	Priority int
	// Seq is the sequence number of the winning announcement.
	Seq uint64
}

// Reject records one admission decision that did not result in an install.
type Reject struct {
	// Prefix is the prefix the rejected announcement was for.
	Prefix string
	// Peer is the session the announcement arrived on.
	Peer string
	// OriginAS is the origin ASN the announcement claimed.
	OriginAS uint32
	// Code is the stable rejection code.
	Code RejectReason
	// Seq is the sequence number of the rejected announcement.
	Seq uint64
}

// Stats summarises guard activity since the last restart.
type Stats struct {
	// Received counts announcements handed to Announce.
	Received int
	// Installed counts announcements that reached the forwarding table.
	Installed int
	// Rejected counts announcements refused by the guard.
	Rejected int
	// Withdrawn counts routes removed by Withdraw or SessionDown.
	Withdrawn int
}

// Snapshot is the state persisted across a process restart.
type Snapshot struct {
	// Routes is the installed RIB.
	Routes []Route
	// Rejects is the rejection history.
	Rejects []Reject
}

// HijackGuard is the inbound admission controller. All methods are safe for
// concurrent use: updates for the same prefix are serialised, and a guard
// rebuilt from a Snapshot keeps the newest sequence number of every prefix.
type HijackGuard struct{}

// NewGuard returns a guard with an empty RIB and the given policy.
func NewGuard(cfg GuardConfig) *HijackGuard {
	return &HijackGuard{}
}

// NewGuardFromSnapshot rebuilds a guard after a restart. The restored guard
// must expose the same installed routes through Lookup and Routes and must
// keep rejecting stale updates for those prefixes.
func NewGuardFromSnapshot(cfg GuardConfig, snap Snapshot) (*HijackGuard, error) {
	return nil, errors.New("not implemented")
}

// Announce validates a against the policy and installs it when every gate
// passes. Validation runs in this order:
//
//  1. flap damping (ReasonDamped),
//  2. RPKI origin validation against GuardConfig.ROAs and a.RPKI (ReasonRPKIInvalid),
//  3. private ASN in ASPath or OriginAS (ReasonPrivateAS),
//  4. AS_SET paths (ReasonASSet),
//  5. community whitelist (ReasonCommunity),
//  6. default route without explicit permission (ReasonDefaultDenied),
//  7. AS path length including Prepend (ReasonPathTooLong),
//  8. stale sequence number (ReasonStaleUpdate),
//  9. MOAS conflict with a different origin (ReasonMOASConflict).
//
// Every rejection returns a non-nil error, records a Reject with the matching
// RejectReason and leaves the installed route untouched.
func (g *HijackGuard) Announce(a Announcement) error {
	return errors.New("not implemented")
}

// Withdraw removes the route for prefix learned from peer and counts one flap
// for the prefix. Withdrawing a prefix that is not installed is not an error.
func (g *HijackGuard) Withdraw(peer, prefix string) error {
	return errors.New("not implemented")
}

// SessionDown drops every route learned from peer, as a BGP session teardown
// must withdraw all routes the peer announced. Routes from other peers are
// left untouched.
func (g *HijackGuard) SessionDown(peer string) error {
	return errors.New("not implemented")
}

// Lookup returns the installed route for prefix.
func (g *HijackGuard) Lookup(prefix string) (Route, bool) {
	return Route{}, false
}

// Routes returns a copy of the installed RIB.
func (g *HijackGuard) Routes() []Route {
	return nil
}

// Rejects returns a copy of the rejection history, oldest first.
func (g *HijackGuard) Rejects() []Reject {
	return nil
}

// FlapCount returns the current flap penalty of prefix.
func (g *HijackGuard) FlapCount(prefix string) int {
	return 0
}

// Stats returns the counters of the guard.
func (g *HijackGuard) Stats() Stats {
	return Stats{}
}

// Snapshot returns a copy of the state that must survive a restart.
func (g *HijackGuard) Snapshot() Snapshot {
	return Snapshot{}
}
