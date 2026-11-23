// Command bgpverify is a local demonstration of the route guard safety fixes:
// it runs one concurrent-update convergence pass, one restart recovery pass,
// and replays the per-prefix/per-peer/per-reason audit trail of a leaked
// default route. It performs no network I/O.
package main

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"gsb/batch45-safety-c-06"
)

func main() {
	fmt.Println("== bgp route guard verification ==")

	audit := &task.AuditLog{}
	limits := map[string]int{"peer-a": 100, "peer-stale": 100}
	table := task.NewRouteTable(limits, audit)
	prefix := netip.MustParsePrefix("172.16.0.0/24")

	fmt.Println("-- concurrent update run: 16 goroutines x 100 Seq values on one prefix --")
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seq := uint64(1); seq <= 100; seq++ {
				_ = table.Update(task.Route{
					Prefix:    prefix,
					Peer:      "peer-a",
					LocalPref: 100,
					Seq:       seq,
					Attrs:     task.PathAttrs{ASPath: []uint32{64500, 174}, RPKIState: "valid"},
				})
			}
		}()
	}
	wg.Wait()

	current, ok := table.Get(prefix.String())
	if !ok {
		panic("concurrent updates produced an empty table")
	}
	fmt.Printf("converged route: prefix=%s peer=%s seq=%d table_len=%d\n",
		current.Prefix, current.Peer, current.Seq, table.Len())

	stale := task.Route{Prefix: prefix, Peer: "peer-stale", Seq: 1}
	if err := table.Update(stale); !errors.Is(err, task.ErrStaleSeq) {
		panic(fmt.Sprintf("stale Seq update was not rejected: %v", err))
	}
	fmt.Println("stale Seq 1 update after convergence: rejected and audited")

	fmt.Println("-- hard prefix quota run: limit 2, third prefix must never install --")
	quotaAudit := &task.AuditLog{}
	quotaTable := task.NewRouteTable(map[string]int{"quota-peer": 2}, quotaAudit)
	for _, text := range []string{"192.0.2.0/24", "192.0.2.1/24", "192.0.2.2/24"} {
		err := quotaTable.Update(task.Route{
			Prefix: netip.MustParsePrefix(text),
			Peer:   "quota-peer",
			Seq:    1,
		})
		if err != nil {
			fmt.Printf("prefix %s rejected: %v (table_len=%d)\n", text, err, quotaTable.Len())
		}
	}
	if quotaTable.Len() != 2 {
		panic("hard quota window observed an over-limit install")
	}

	fmt.Println("-- restart run: snapshot -> empty process state -> RestoreTable --")
	snapshot := table.Persist()
	fmt.Printf("persisted snapshot contains %d route(s)\n", len(snapshot))
	restored, err := task.RestoreTable(snapshot, limits, audit)
	if err != nil {
		panic(err)
	}
	restoredRoute, ok := restored.Get(prefix.String())
	if !ok {
		panic("restart lost the converged route")
	}
	fmt.Printf("restored table: prefixes=%d prefix=%s peer=%s seq=%d\n",
		restored.Len(), restoredRoute.Prefix, restoredRoute.Peer, restoredRoute.Seq)

	fmt.Println("-- audit replay --")
	staleRows := audit.ForPrefix(prefix.String(), "peer-stale")
	fmt.Printf("stale-seq audit rows for %s / peer-stale: %d\n", prefix, len(staleRows))
	for _, row := range staleRows {
		fmt.Printf("  reject prefix=%s peer=%s reason=%s seq=%d\n",
			row.Prefix, row.Peer, row.Reason, row.Seq)
	}

	guard := &task.RouteGuard{}
	flaps := &task.FlapHistory{}
	policy := task.Policy{
		PeerAS:             64500,
		CommunityAllowList: []uint32{0x000a0001},
	}
	leak := task.Route{
		Prefix: netip.MustParsePrefix("0.0.0.0/0"),
		Peer:   "upstream-leak",
		Seq:    9,
		Attrs: task.PathAttrs{
			ASPath:       []uint32{64501, 65001, 174},
			Communities:  []uint32{0x000b0009},
			PrependCount: 0,
			RPKIState:    "invalid",
			HasASSet:     true,
		},
	}
	verdict, reasons, err := guard.Admit(leak, policy, 1_000_000, 60, flaps, audit)
	if err != nil {
		panic(err)
	}
	fmt.Printf("leaked default route verdict=%d with %d per-reason audit rows:\n",
		verdict, len(reasons))
	for _, reason := range reasons {
		fmt.Printf("  reject prefix=%s peer=%s reason=%s seq=%d\n",
			leak.Prefix, leak.Peer, reason, leak.Seq)
	}

	fmt.Println("-- flap damping run across updates --")
	now := int64(2_000_000)
	for offset := int64(1); offset <= 8; offset++ {
		flaps.Append("203.0.113.0/24", now-offset)
	}
	flapRoute := task.Route{
		Prefix: netip.MustParsePrefix("203.0.113.0/24"),
		Peer:   "peer-a",
		Seq:    3,
		Attrs: task.PathAttrs{
			ASPath:       []uint32{64500, 174},
			PrependCount: 1,
			RPKIState:    "valid",
		},
	}
	verdict, reasons, err = guard.Admit(flapRoute,
		task.Policy{PeerAS: 64500, CommunityAllowList: nil}, now, 60, flaps, audit)
	if err != nil {
		panic(err)
	}
	fmt.Printf("flapping route verdict=%d reasons=%v\n", verdict, reasons)

	fmt.Println("== verification OK ==")
}
