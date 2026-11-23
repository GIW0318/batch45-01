package task

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"sync"
	"testing"

	. "gsb/batch45-safety-b-12"
)

func newReconciler(t *testing.T) *RouteReconciler {
	t.Helper()
	r, err := NewRouteReconciler()
	if err != nil {
		t.Fatalf("NewRouteReconciler: %v", err)
	}
	return r
}

func TestSubscribeRegistersIPv6Group(t *testing.T) {
	r := newReconciler(t)
	if err := r.Subscribe([]int{4, 6}); err != nil {
		t.Fatalf("subscribe families 4,6: %v", err)
	}
	if got, want := r.Groups(), []int{GroupIPv4Route, GroupIPv6Route}; !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	if err := r.Subscribe([]int{4}); err != nil {
		t.Fatalf("subscribe family 4: %v", err)
	}
	if got, want := r.Groups(), []int{GroupIPv4Route}; !reflect.DeepEqual(got, want) {
		t.Fatalf("groups after v4-only subscribe = %v, want %v", got, want)
	}
}

func TestFragmentedMessagesReassembled(t *testing.T) {
	r := newReconciler(t)
	full, err := json.Marshal([]Route{
		{Table: TableMain, Family: 4, Dst: "10.0.0.0/8", Nexthops: []string{"10.1.0.1", "10.1.0.2"}},
		{Table: TablePolicy, Family: 6, Dst: "2001:db8::/32", Nexthops: []string{"fe80::1"}},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	frags := [][]byte{full[:7], full[7 : len(full)/2], full[len(full)/2:]}
	routes, err := r.Reassemble(frags)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("reassembled %d routes, want 2", len(routes))
	}
	if len(routes[0].Nexthops) != 2 {
		t.Fatalf("nexthops truncated to %v", routes[0].Nexthops)
	}
}

func TestEventOlderThanDumpDropped(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyDump(Dump{Seq: 10, Routes: []Route{{Table: TableMain, Dst: "10.0.0.0/8"}}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	old := Event{Kind: "add", Seq: 9, Route: Route{Table: TableMain, Dst: "192.168.0.0/16"}}
	if err := r.ApplyEvent(old); err != nil {
		t.Fatalf("apply stale event: %v", err)
	}
	if _, ok := r.Lookup(TableMain, "192.168.0.0/16"); ok {
		t.Fatalf("stale event overtook the dump")
	}
	if got := r.Stats().Dropped; got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 11, Route: Route{Table: TableMain, Dst: "172.16.0.0/12"}}); err != nil {
		t.Fatalf("apply fresh event: %v", err)
	}
	if _, ok := r.Lookup(TableMain, "172.16.0.0/12"); !ok {
		t.Fatalf("fresh event was not applied")
	}
}

func TestDeleteScopedToTable(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyDump(Dump{Seq: 1, Routes: []Route{
		{Table: TablePolicy, Dst: "10.0.0.0/8"},
		{Table: TableMain, Dst: "10.0.0.0/8"},
	}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	if err := r.Delete(TablePolicy, "10.0.0.0/8"); err != nil {
		t.Fatalf("delete in policy table: %v", err)
	}
	if _, ok := r.Lookup(TablePolicy, "10.0.0.0/8"); ok {
		t.Fatalf("policy entry not deleted")
	}
	if _, ok := r.Lookup(TableMain, "10.0.0.0/8"); !ok {
		t.Fatalf("main table entry deleted through a policy-table delete")
	}
}

func TestNexthopsNotMerged(t *testing.T) {
	r := newReconciler(t)
	route := Route{Table: TableMain, Dst: "10.0.0.0/8", Nexthops: []string{"10.1.0.1", "10.1.0.2"}}
	if err := r.ApplyDump(Dump{Seq: 1, Routes: []Route{route}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	got, ok := r.Lookup(TableMain, "10.0.0.0/8")
	if !ok {
		t.Fatalf("route missing after dump")
	}
	if !reflect.DeepEqual(got.Nexthops, route.Nexthops) {
		t.Fatalf("nexthops = %v, want %v (multipath merged)", got.Nexthops, route.Nexthops)
	}
}

func TestPolicyRoutesSortedByPriority(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyDump(Dump{Seq: 1, Routes: []Route{
		{Table: TablePolicy, Dst: "10.20.0.0/16", Priority: 200},
		{Table: TablePolicy + 1, Dst: "10.10.0.0/16", Priority: 100},
	}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	got := r.PolicyRoutes()
	if len(got) != 2 {
		t.Fatalf("policy routes = %d, want 2", len(got))
	}
	if got[0].Priority != 100 || got[1].Priority != 200 {
		t.Fatalf("policy route order = %d,%d want 100,200", got[0].Priority, got[1].Priority)
	}
}

func TestLinkDownMarksRoutesInvalid(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyDump(Dump{Seq: 1, Routes: []Route{
		{Table: TableMain, Dst: "10.0.0.0/8", Iface: "eth0", Admin: true},
		{Table: TableMain, Dst: "10.1.0.0/16", Iface: "eth1", Admin: true},
	}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	if err := r.MarkLinkDown("eth0"); err != nil {
		t.Fatalf("mark link down: %v", err)
	}
	eth0, ok := r.Lookup(TableMain, "10.0.0.0/8")
	if !ok || !eth0.Invalid || eth0.Admin {
		t.Fatalf("eth0 route = %+v, want invalid and admin down", eth0)
	}
	eth1, ok := r.Lookup(TableMain, "10.1.0.0/16")
	if !ok || eth1.Invalid || !eth1.Admin {
		t.Fatalf("eth1 route = %+v, want untouched", eth1)
	}
}

func TestConcurrentApplyNoLostRoutes(t *testing.T) {
	r := newReconciler(t)
	var wg sync.WaitGroup
	errCh := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dst := "10." + strconv.Itoa(i) + ".0.0/16"
			errCh <- r.ApplyEvent(Event{Kind: "add", Seq: uint32(i + 1), Route: Route{Table: TableMain, Dst: dst}})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent apply: %v", err)
		}
	}
	if got := len(r.Routes(TableMain)); got != 50 {
		t.Fatalf("routes = %d after concurrent adds, want 50", got)
	}
}

func TestReconnectResyncs(t *testing.T) {
	r := newReconciler(t)
	if err := r.Reconnect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if got := r.Stats().Reconnects; got != 1 {
		t.Fatalf("reconnects = %d, want 1", got)
	}
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 1, Route: Route{Table: TableMain, Dst: "10.9.0.0/16"}}); err != nil {
		t.Fatalf("apply event after reconnect: %v", err)
	}
	if _, ok := r.Lookup(TableMain, "10.9.0.0/16"); !ok {
		t.Fatalf("reconciler did not resync after reconnect")
	}
}

func TestSequenceWraparoundAccepted(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyDump(Dump{Seq: math.MaxUint32 - 1, Routes: []Route{{Table: TableMain, Dst: "10.0.0.0/8"}}}); err != nil {
		t.Fatalf("apply dump near wrap: %v", err)
	}
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 2, Route: Route{Table: TableMain, Dst: "10.5.0.0/16"}}); err != nil {
		t.Fatalf("apply event after wrap: %v", err)
	}
	if _, ok := r.Lookup(TableMain, "10.5.0.0/16"); !ok {
		t.Fatalf("event after sequence wraparound was dropped")
	}
}

func TestENODEVNotSilent(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyDump(Dump{Seq: 1, Routes: []Route{{Table: TableMain, Dst: "10.0.0.0/8", Iface: "eth0", Admin: true}}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	if err := r.HandleErrno("eth0", 0); err != nil {
		t.Fatalf("handle zero errno: %v", err)
	}
	if err := r.HandleErrno("eth0", ErrnoENODEV); err == nil {
		t.Fatalf("ENODEV was swallowed")
	}
	got, ok := r.Lookup(TableMain, "10.0.0.0/8")
	if !ok || !got.Invalid {
		t.Fatalf("route after ENODEV = %+v, want marked invalid", got)
	}
	if n := r.Stats().Errors; n != 1 {
		t.Fatalf("errors = %d, want 1", n)
	}
}

func TestRestartWithDumpRebuildsTable(t *testing.T) {
	r := newReconciler(t)
	if got := len(r.Routes(TableMain)); got != 0 {
		t.Fatalf("new reconciler has %d routes, want 0", got)
	}
	if err := r.ApplyDump(Dump{Seq: 100, Routes: []Route{
		{Table: TableMain, Dst: "10.0.0.0/8"},
		{Table: TableMain, Dst: "10.1.0.0/16"},
	}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	if got := len(r.Routes(TableMain)); got != 2 {
		t.Fatalf("routes after restart dump = %d, want 2", got)
	}
	if got := r.Stats().Resyncs; got != 1 {
		t.Fatalf("resyncs = %d, want 1", got)
	}
}

func TestPolicyAndMainTableBothUpdated(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 1, Route: Route{Table: TablePolicy, Dst: "10.0.0.0/8", Priority: 10}}); err != nil {
		t.Fatalf("add policy route: %v", err)
	}
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 2, Route: Route{Table: TableMain, Dst: "10.0.0.0/8"}}); err != nil {
		t.Fatalf("add main route: %v", err)
	}
	if _, ok := r.Lookup(TablePolicy, "10.0.0.0/8"); !ok {
		t.Fatalf("policy table lost the route when the main table changed")
	}
	if _, ok := r.Lookup(TableMain, "10.0.0.0/8"); !ok {
		t.Fatalf("main table route missing")
	}
	if got := len(r.PolicyRoutes()); got != 1 {
		t.Fatalf("policy routes = %d, want 1", got)
	}
}

func TestDuplicateAddIdempotent(t *testing.T) {
	r := newReconciler(t)
	rt := Route{Table: TableMain, Dst: "10.0.0.0/8", Nexthops: []string{"10.1.0.1"}}
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 1, Route: rt}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 2, Route: rt}); err != nil {
		t.Fatalf("second add: %v", err)
	}
	if got := len(r.Routes(TableMain)); got != 1 {
		t.Fatalf("duplicate add produced %d routes, want 1", got)
	}
}

func TestSnapshotDeepCopy(t *testing.T) {
	r := newReconciler(t)
	if err := r.ApplyDump(Dump{Seq: 1, Routes: []Route{{Table: TableMain, Dst: "10.0.0.0/8"}}}); err != nil {
		t.Fatalf("apply dump: %v", err)
	}
	snap := r.SnapshotAll()
	if got := len(snap[TableMain]); got != 1 {
		t.Fatalf("snapshot = %d routes, want 1", got)
	}
	if err := r.ApplyEvent(Event{Kind: "add", Seq: 2, Route: Route{Table: TableMain, Dst: "10.7.0.0/16"}}); err != nil {
		t.Fatalf("apply event: %v", err)
	}
	if got := len(snap[TableMain]); got != 1 {
		t.Fatalf("snapshot mutated by later event: %d routes", got)
	}
	if got := len(r.Routes(TableMain)); got != 2 {
		t.Fatalf("live table = %d routes, want 2", got)
	}
}
