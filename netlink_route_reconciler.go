package task

import "errors"

// Netlink multicast group ids (RTNLGRP_*).
const (
	GroupIPv4Route = 7
	GroupIPv6Route = 11
)

// Route tables.
const (
	TableMain   = 254
	TableLocal  = 255
	TablePolicy = 100
)

// ErrnoENODEV mirrors the kernel ENODEV errno returned for missing links.
const ErrnoENODEV = 19

// Route is one routing table entry.
type Route struct {
	Table    int
	Family   int
	Dst      string
	Gateway  string
	Nexthops []string
	Protocol string
	Priority int
	Iface    string
	Admin    bool
	Invalid  bool
}

// Event is one rtnetlink notification.
type Event struct {
	Kind  string // add, update or del
	Route Route
	Seq   uint32
}

// Dump is a full routing table snapshot returned by the kernel.
type Dump struct {
	Routes []Route
	Seq    uint32
}

// Stats counters of the reconciler.
type Stats struct {
	Applied    int
	Dropped    int
	Reconnects int
	Resyncs    int
	Errors     int
}

// RouteReconciler keeps kernel routing state aligned with policy state.
type RouteReconciler struct{}

// NewRouteReconciler returns an empty reconciler.
func NewRouteReconciler() (*RouteReconciler, error) {
	return nil, errors.New("not implemented")
}

// Subscribe joins the multicast groups for the given IP families (4 and 6).
func (r *RouteReconciler) Subscribe(families []int) error {
	return errors.New("not implemented")
}

// Groups returns the subscribed netlink groups.
func (r *RouteReconciler) Groups() []int {
	return nil
}

// Reassemble rejoins netlink multipart fragments into complete messages.
func (r *RouteReconciler) Reassemble(fragments [][]byte) ([]Route, error) {
	return nil, errors.New("not implemented")
}

// ApplyDump replaces the local state with a full snapshot.
func (r *RouteReconciler) ApplyDump(d Dump) error {
	return errors.New("not implemented")
}

// ApplyEvent applies one incremental event. Events older than the newest
// applied sequence must be dropped, with uint32 wraparound handled.
func (r *RouteReconciler) ApplyEvent(ev Event) error {
	return errors.New("not implemented")
}

// Routes returns the routes of one table ordered by priority then prefix.
func (r *RouteReconciler) Routes(table int) []Route {
	return nil
}

// Lookup finds an exact prefix in one table.
func (r *RouteReconciler) Lookup(table int, dst string) (Route, bool) {
	return Route{}, false
}

// PolicyRoutes returns the non-main tables ordered by priority.
func (r *RouteReconciler) PolicyRoutes() []Route {
	return nil
}

// Delete removes one prefix from one table only.
func (r *RouteReconciler) Delete(table int, dst string) error {
	return errors.New("not implemented")
}

// MarkLinkDown marks every route that egresses through iface as invalid.
func (r *RouteReconciler) MarkLinkDown(iface string) error {
	return errors.New("not implemented")
}

// HandleErrno records a kernel error for one interface and returns it to the
// caller instead of skipping silently.
func (r *RouteReconciler) HandleErrno(iface string, errno int) error {
	return errors.New("not implemented")
}

// Reconnect re-establishes the netlink socket after it dropped.
func (r *RouteReconciler) Reconnect() error {
	return errors.New("not implemented")
}

// Stats returns the current counters.
func (r *RouteReconciler) Stats() Stats {
	return Stats{}
}

// SnapshotAll returns a deep copy of all tables.
func (r *RouteReconciler) SnapshotAll() map[int][]Route {
	return nil
}
