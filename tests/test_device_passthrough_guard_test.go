package task

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	. "gsb/batch45-safety-b-18"
)

func newPassthrough(t *testing.T) *DevicePassthroughGuard {
	t.Helper()
	return NewDevicePassthroughGuard()
}

func dev(pci, node string, group int) Device {
	return Device{
		PCI:        pci,
		IOMMUGroup: group,
		Vendor:     "10de",
		Product:    "2204",
		Node:       node,
		Driver:     "nvidia",
		HostDriver: "nvidia",
		Healthy:    true,
	}
}

func TestSharedGroupRejected(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	b := dev("0000:01:00.1", "node-1", 1)
	if err := g.RegisterGroup([]Device{a, b}); err != nil {
		t.Fatalf("register group: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate a: %v", err)
	}
	if err := g.Allocate(b.PCI, "t2", "c2"); !errors.Is(err, ErrGroupShared) {
		t.Fatalf("second tenant in the same IOMMU group error = %v, want ErrGroupShared", err)
	}
	if got, _ := g.Device(b.PCI); got.Tenant != "" {
		t.Fatalf("device b was allocated anyway: %+v", got)
	}
	if err := g.Release(a.PCI); err != nil {
		t.Fatalf("release a: %v", err)
	}
	if err := g.Allocate(b.PCI, "t2", "c2"); err != nil {
		t.Fatalf("allocate b after group freed: %v", err)
	}
}

func TestContainerExitReleasesDevice(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:02:00.0", "node-1", 2)
	c := dev("0000:03:00.0", "node-1", 3)
	if err := g.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := g.Register(c); err != nil {
		t.Fatalf("register c: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate a: %v", err)
	}
	if err := g.Allocate(c.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate c: %v", err)
	}
	if err := g.ReleaseContainer("c1"); err != nil {
		t.Fatalf("release container: %v", err)
	}
	if got, _ := g.Device(a.PCI); got.Container != "" || got.Tenant != "" || got.Bound {
		t.Fatalf("device a after container exit = %+v, want free", got)
	}
	if got := len(g.Allocations()); got != 0 {
		t.Fatalf("allocations = %d after container exit, want 0", got)
	}
	if err := g.Allocate(a.PCI, "t2", "c2"); err != nil {
		t.Fatalf("allocate a to another tenant after release: %v", err)
	}
}

func TestDuplicatePCIRegistrationRejected(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	dup := a
	dup.Node = "node-9"
	dup.Vendor = "ffff"
	if err := g.Register(dup); !errors.Is(err, ErrDeviceExists) {
		t.Fatalf("duplicate registration error = %v, want ErrDeviceExists", err)
	}
	got, ok := g.Device(a.PCI)
	if !ok {
		t.Fatalf("device disappeared after duplicate registration")
	}
	if got.Node != "node-1" || got.Vendor != "10de" {
		t.Fatalf("device overwritten by duplicate: %+v", got)
	}
}

func TestGroupMembersBoundTogether(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	b := dev("0000:01:00.1", "node-1", 1)
	if err := g.RegisterGroup([]Device{a, b}); err != nil {
		t.Fatalf("register group: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	for _, pci := range []string{a.PCI, b.PCI} {
		got, ok := g.Device(pci)
		if !ok {
			t.Fatalf("device %s missing", pci)
		}
		if !got.Bound || got.Driver != DriverVFIO {
			t.Fatalf("group member %s = %+v, want bound to vfio-pci", pci, got)
		}
	}
}

func TestResetFailureBlocksAllocation(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := g.FailNextReset(a.PCI); err != nil {
		t.Fatalf("arm reset failure: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); !errors.Is(err, ErrResetFailed) {
		t.Fatalf("allocation after reset failure error = %v, want ErrResetFailed", err)
	}
	if got, _ := g.Device(a.PCI); got.Tenant != "" {
		t.Fatalf("device allocated despite reset failure: %+v", got)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocation after a successful reset: %v", err)
	}
}

func TestHotplugRemoveClearsDevice(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if err := g.Hotplug(Hotplug{Action: HotplugRemove, Device: a}); err != nil {
		t.Fatalf("hotplug remove: %v", err)
	}
	if _, ok := g.Device(a.PCI); ok {
		t.Fatalf("removed device still present")
	}
	if got := len(g.Allocations()); got != 0 {
		t.Fatalf("allocations = %d after removal, want 0", got)
	}
	added := dev("0000:0a:00.0", "node-1", 10)
	if err := g.Hotplug(Hotplug{Action: HotplugAdd, Device: added}); err != nil {
		t.Fatalf("hotplug add: %v", err)
	}
	if _, ok := g.Device(added.PCI); !ok {
		t.Fatalf("hotplugged device not tracked")
	}
}

func TestAllocateUnknownDeviceRejected(t *testing.T) {
	g := newPassthrough(t)
	if err := g.Allocate("0000:99:00.0", "t1", "c1"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("unknown device error = %v, want ErrUnknownDevice", err)
	}
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate registered device: %v", err)
	}
}

func TestTenantReservationEnforced(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := g.Reserve(a.PCI, "t1"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := g.Allocate(a.PCI, "t2", "c2"); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("reserved device allocated to another tenant: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("reserved device allocated to its tenant: %v", err)
	}
}

func TestConcurrentAllocateSameDeviceOneWinner(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := g.Allocate(a.PCI, "t"+strconv.Itoa(i), "c"+strconv.Itoa(i))
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful allocations = %d, want exactly 1", successes)
	}
	if got := len(g.Allocations()); got != 1 {
		t.Fatalf("allocations = %d, want 1", got)
	}
}

func TestHostDriverUnboundAfterPassthrough(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got, _ := g.Device(a.PCI); got.Driver != "nvidia" {
		t.Fatalf("initial driver = %q, want nvidia", got.Driver)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	got, _ := g.Device(a.PCI)
	if got.Driver != DriverVFIO {
		t.Fatalf("driver after passthrough = %q, want %q", got.Driver, DriverVFIO)
	}
	if !got.Bound {
		t.Fatalf("device not bound after passthrough")
	}
	if err := g.Release(a.PCI); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, _ = g.Device(a.PCI)
	if got.Driver != "nvidia" || got.Bound {
		t.Fatalf("device after release = %+v, want host driver restored", got)
	}
}

func TestMigrationReleasesNode(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	b := dev("0000:02:00.0", "node-2", 2)
	if err := g.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := g.Register(b); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate a: %v", err)
	}
	if err := g.Allocate(b.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocate b: %v", err)
	}
	if err := g.ReleaseNode("node-1"); err != nil {
		t.Fatalf("release node: %v", err)
	}
	if got, _ := g.Device(a.PCI); got.Tenant != "" || got.Bound {
		t.Fatalf("device on migrated node still held: %+v", got)
	}
	if got, _ := g.Device(b.PCI); got.Tenant != "t1" {
		t.Fatalf("device on the other node was released: %+v", got)
	}
}

func TestRestoreAllocationsAfterRestart(t *testing.T) {
	g := newPassthrough(t)
	pci := "0000:01:00.0"
	state := map[string]Device{
		pci: {
			PCI:        pci,
			IOMMUGroup: 1,
			Node:       "node-1",
			Driver:     DriverVFIO,
			HostDriver: "nvidia",
			Bound:      true,
			Healthy:    true,
			Tenant:     "t1",
			Container:  "c1",
		},
	}
	if err := g.Restore(state); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, ok := g.Device(pci)
	if !ok || got.Tenant != "t1" || got.Container != "c1" {
		t.Fatalf("restored device = %+v, want tenant t1/container c1", got)
	}
	allocations := g.Allocations()
	if allocations[pci] != "t1" {
		t.Fatalf("allocation table = %v, want %s->t1", allocations, pci)
	}
	if err := g.Allocate(pci, "t2", "c2"); !errors.Is(err, ErrDeviceBusy) {
		t.Fatalf("restored allocation not enforced: %v", err)
	}
}

func TestGroupAllocationIsAtomic(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	broken := dev("0000:01:00.1", "node-1", 1)
	broken.Healthy = false
	if err := g.RegisterGroup([]Device{a, broken}); err != nil {
		t.Fatalf("register group: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); !errors.Is(err, ErrGroupIncomplete) {
		t.Fatalf("allocation with unhealthy group member error = %v, want ErrGroupIncomplete", err)
	}
	if got, _ := g.Device(a.PCI); got.Tenant != "" || got.Bound {
		t.Fatalf("device partially allocated: %+v", got)
	}
	broken.Healthy = true
	if err := g.Hotplug(Hotplug{Action: HotplugAdd, Device: broken}); err != nil {
		t.Fatalf("hotplug healthy update: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("allocation after the group became healthy: %v", err)
	}
}

func TestDoubleAllocateSameTenantRejected(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	if err := g.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c1"); err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	if err := g.Allocate(a.PCI, "t1", "c2"); !errors.Is(err, ErrDeviceBusy) {
		t.Fatalf("second allocation of the same device error = %v, want ErrDeviceBusy", err)
	}
	if got, _ := g.Device(a.PCI); got.Container != "c1" {
		t.Fatalf("container changed by the rejected allocation: %+v", got)
	}
}

func TestConcurrentGroupAllocationOneWinner(t *testing.T) {
	g := newPassthrough(t)
	a := dev("0000:01:00.0", "node-1", 1)
	b := dev("0000:01:00.1", "node-1", 1)
	if err := g.RegisterGroup([]Device{a, b}); err != nil {
		t.Fatalf("register group: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := a.PCI
			if i%2 == 1 {
				target = b.PCI
			}
			err := g.Allocate(target, "t"+strconv.Itoa(i), "c"+strconv.Itoa(i))
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful group allocations = %d, want exactly 1", successes)
	}
	if got := len(g.Allocations()); got != 1 {
		t.Fatalf("allocations = %d, want 1", got)
	}
}
