package task

import (
	"errors"
	"sort"
	"sync"
)

// Driver names used by the passthrough manager.
const (
	DriverVFIO = "vfio-pci"
)

// Hotplug actions.
const (
	HotplugAdd    = "add"
	HotplugRemove = "remove"
)

var (
	ErrDeviceExists    = errors.New("passthrough: PCI address already registered")
	ErrDeviceBusy      = errors.New("passthrough: device already allocated")
	ErrGroupShared     = errors.New("passthrough: IOMMU group already owned by another tenant")
	ErrGroupIncomplete = errors.New("passthrough: not every IOMMU group member can be bound")
	ErrResetFailed     = errors.New("passthrough: device reset failed")
	ErrTenantMismatch  = errors.New("passthrough: device reserved for another tenant")
	ErrUnknownDevice   = errors.New("passthrough: unknown PCI address")

	errUnknownHotplug = errors.New("passthrough: unknown hotplug action")
)

// Device is one PCI function known to the manager.
type Device struct {
	PCI         string
	IOMMUGroup  int
	Vendor      string
	Product     string
	Node        string
	Driver      string
	HostDriver  string
	Bound       bool
	Healthy     bool
	Tenant      string
	Container   string
	ReservedFor string
}

// Hotplug is a device add or remove notification.
type Hotplug struct {
	Action string
	Device Device
}

// DevicePassthroughGuard owns the device allocation table.
type DevicePassthroughGuard struct {
	mu            sync.Mutex
	devices       map[string]Device
	allocations   map[string]string
	failReset     map[string]bool
	pendingRepair map[string]bool
}

// NewDevicePassthroughGuard returns an empty manager.
func NewDevicePassthroughGuard() *DevicePassthroughGuard {
	return &DevicePassthroughGuard{
		devices:       make(map[string]Device),
		allocations:   make(map[string]string),
		failReset:     make(map[string]bool),
		pendingRepair: make(map[string]bool),
	}
}

func (g *DevicePassthroughGuard) ensureLocked() {
	if g.devices == nil {
		g.devices = make(map[string]Device)
	}
	if g.allocations == nil {
		g.allocations = make(map[string]string)
	}
	if g.failReset == nil {
		g.failReset = make(map[string]bool)
	}
	if g.pendingRepair == nil {
		g.pendingRepair = make(map[string]bool)
	}
}

func normalizeDevice(d Device) Device {
	if d.HostDriver == "" && d.Driver != "" && d.Driver != DriverVFIO {
		d.HostDriver = d.Driver
	}
	if d.Bound && d.Driver == "" {
		d.Driver = DriverVFIO
	}
	return d
}

func hostDriverOf(d Device) string {
	if d.HostDriver != "" {
		return d.HostDriver
	}
	if d.Driver != "" && d.Driver != DriverVFIO {
		return d.Driver
	}
	return ""
}

func (g *DevicePassthroughGuard) ownerOfLocked(d Device) string {
	if tenant := g.allocations[d.PCI]; tenant != "" {
		return tenant
	}
	return d.Tenant
}

func (g *DevicePassthroughGuard) allocatedLocked(d Device) bool {
	if d.Tenant != "" || d.Container != "" {
		return true
	}
	_, ok := g.allocations[d.PCI]
	return ok
}

func (g *DevicePassthroughGuard) groupMembersLocked(group int) []Device {
	members := make([]Device, 0)
	for _, d := range g.devices {
		if d.IOMMUGroup == group {
			members = append(members, d)
		}
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].PCI < members[j].PCI
	})
	return members
}

func (g *DevicePassthroughGuard) groupOwnerLocked(group int, excludePCI string) (owner string, conflict bool) {
	for _, d := range g.devices {
		if d.IOMMUGroup != group || d.PCI == excludePCI {
			continue
		}
		tenant := g.ownerOfLocked(d)
		if tenant == "" {
			continue
		}
		if owner == "" {
			owner = tenant
			continue
		}
		if owner != tenant {
			return owner, true
		}
	}
	return owner, false
}

// Register adds one device, rejecting duplicate PCI addresses.
func (g *DevicePassthroughGuard) Register(d Device) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	d = normalizeDevice(d)
	if d.PCI == "" {
		return ErrUnknownDevice
	}
	if _, ok := g.devices[d.PCI]; ok {
		return ErrDeviceExists
	}

	g.devices[d.PCI] = d
	if d.Tenant != "" {
		g.allocations[d.PCI] = d.Tenant
	}
	return nil
}

// RegisterGroup registers all functions of one IOMMU group.
func (g *DevicePassthroughGuard) RegisterGroup(devices []Device) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	if len(devices) == 0 {
		return nil
	}

	prepared := make([]Device, 0, len(devices))
	seen := make(map[string]struct{}, len(devices))
	group := devices[0].IOMMUGroup
	for _, d := range devices {
		d = normalizeDevice(d)
		if d.PCI == "" {
			return ErrUnknownDevice
		}
		if d.IOMMUGroup != group {
			return ErrGroupIncomplete
		}
		if _, ok := seen[d.PCI]; ok {
			return ErrDeviceExists
		}
		if _, ok := g.devices[d.PCI]; ok {
			return ErrDeviceExists
		}
		seen[d.PCI] = struct{}{}
		prepared = append(prepared, d)
	}

	for _, d := range prepared {
		g.devices[d.PCI] = d
		if d.Tenant != "" {
			g.allocations[d.PCI] = d.Tenant
		}
	}
	return nil
}

// Allocate assigns pci to tenant/container. The whole IOMMU group is bound to
// vfio-pci atomically; a group may only ever serve one tenant.
func (g *DevicePassthroughGuard) Allocate(pci, tenant, container string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	target, ok := g.devices[pci]
	if !ok {
		return ErrUnknownDevice
	}
	if g.allocatedLocked(target) {
		return ErrDeviceBusy
	}

	members := g.groupMembersLocked(target.IOMMUGroup)
	owner := ""
	for _, d := range members {
		if reserved := d.ReservedFor; reserved != "" && reserved != tenant {
			return ErrTenantMismatch
		}
		current := g.ownerOfLocked(d)
		if current == "" {
			continue
		}
		if owner == "" {
			owner = current
		} else if owner != current {
			return ErrGroupShared
		}
	}
	if owner != "" {
		if owner != tenant {
			return ErrGroupShared
		}
		return ErrDeviceBusy
	}

	for _, d := range members {
		if !d.Healthy {
			return ErrGroupIncomplete
		}
	}
	for _, d := range members {
		if g.failReset[d.PCI] {
			delete(g.failReset, d.PCI)
			return ErrResetFailed
		}
	}

	snapshot := make(map[string]Device, len(members))
	for _, d := range members {
		snapshot[d.PCI] = d
	}
	for _, d := range members {
		if !d.Healthy {
			g.restoreSnapshotLocked(snapshot)
			return ErrGroupIncomplete
		}
		d.Bound = true
		d.Driver = DriverVFIO
		g.devices[d.PCI] = d
		delete(g.pendingRepair, d.PCI)
	}

	target = g.devices[pci]
	target.Tenant = tenant
	target.Container = container
	g.devices[pci] = target
	g.allocations[pci] = tenant
	return nil
}

func (g *DevicePassthroughGuard) restoreSnapshotLocked(snapshot map[string]Device) {
	for pci, d := range snapshot {
		g.devices[pci] = d
	}
}

func (g *DevicePassthroughGuard) unbindLocked(d Device) Device {
	d.Driver = hostDriverOf(d)
	d.Bound = false
	return d
}

func (g *DevicePassthroughGuard) releaseGroupLocked(group int) {
	for _, d := range g.groupMembersLocked(group) {
		delete(g.allocations, d.PCI)
		delete(g.pendingRepair, d.PCI)
		d.Tenant = ""
		d.Container = ""
		d = g.unbindLocked(d)
		g.devices[d.PCI] = d
	}
}

// Release frees one device and restores its host driver.
func (g *DevicePassthroughGuard) Release(pci string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	d, ok := g.devices[pci]
	if !ok {
		return ErrUnknownDevice
	}
	g.releaseGroupLocked(d.IOMMUGroup)
	return nil
}

// ReleaseContainer frees every device held by one container.
func (g *DevicePassthroughGuard) ReleaseContainer(container string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	groups := make(map[int]struct{})
	for _, d := range g.devices {
		if d.Container == container && g.allocatedLocked(d) {
			groups[d.IOMMUGroup] = struct{}{}
		}
	}
	for group := range groups {
		g.releaseGroupLocked(group)
	}
	return nil
}

// ReleaseNode frees every device allocated on one node (container migration).
func (g *DevicePassthroughGuard) ReleaseNode(node string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	groups := make(map[int]struct{})
	for _, d := range g.devices {
		if d.Node == node && g.allocatedLocked(d) {
			groups[d.IOMMUGroup] = struct{}{}
		}
	}
	for group := range groups {
		g.releaseGroupLocked(group)
	}
	return nil
}

// Reserve pins a device to one tenant.
func (g *DevicePassthroughGuard) Reserve(pci, tenant string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	d, ok := g.devices[pci]
	if !ok {
		return ErrUnknownDevice
	}
	if tenant == "" {
		d.ReservedFor = ""
		g.devices[pci] = d
		return nil
	}
	if d.ReservedFor != "" && d.ReservedFor != tenant {
		return ErrTenantMismatch
	}
	if current := g.ownerOfLocked(d); current != "" && current != tenant {
		return ErrTenantMismatch
	}
	if owner, conflict := g.groupOwnerLocked(d.IOMMUGroup, pci); conflict {
		return ErrGroupShared
	} else if owner != "" && owner != tenant {
		return ErrGroupShared
	}

	d.ReservedFor = tenant
	g.devices[pci] = d
	return nil
}

// Device returns one device.
func (g *DevicePassthroughGuard) Device(pci string) (Device, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	d, ok := g.devices[pci]
	return d, ok
}

// Group returns the members of one IOMMU group ordered by PCI address.
func (g *DevicePassthroughGuard) Group(group int) []Device {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	members := g.groupMembersLocked(group)
	if len(members) == 0 {
		return nil
	}
	return members
}

// Reset resets one device.
func (g *DevicePassthroughGuard) Reset(pci string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	if _, ok := g.devices[pci]; !ok {
		return ErrUnknownDevice
	}
	if g.failReset[pci] {
		delete(g.failReset, pci)
		return ErrResetFailed
	}
	return nil
}

// FailNextReset makes the next reset of pci fail once.
func (g *DevicePassthroughGuard) FailNextReset(pci string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	if _, ok := g.devices[pci]; !ok {
		return ErrUnknownDevice
	}
	g.failReset[pci] = true
	return nil
}

// Hotplug applies one hotplug notification.
func (g *DevicePassthroughGuard) Hotplug(ev Hotplug) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	switch ev.Action {
	case HotplugAdd:
		return g.hotplugAddLocked(ev.Device)
	case HotplugRemove:
		return g.hotplugRemoveLocked(ev.Device.PCI)
	default:
		return errUnknownHotplug
	}
}

func (g *DevicePassthroughGuard) hotplugAddLocked(raw Device) error {
	d := normalizeDevice(raw)
	if d.PCI == "" {
		return ErrUnknownDevice
	}

	cur, exists := g.devices[d.PCI]
	if !exists {
		owner, conflict := g.groupOwnerLocked(d.IOMMUGroup, "")
		if conflict {
			return ErrGroupShared
		}
		if d.Tenant != "" {
			if owner != "" && owner != d.Tenant {
				return ErrGroupShared
			}
		} else if owner != "" {
			if !d.Healthy {
				return ErrGroupIncomplete
			}
			d.Driver = DriverVFIO
			d.Bound = true
		}
		g.devices[d.PCI] = d
		if d.Tenant != "" {
			g.allocations[d.PCI] = d.Tenant
		}
		return nil
	}

	owner, conflict := g.groupOwnerLocked(d.IOMMUGroup, d.PCI)
	if conflict {
		return ErrGroupShared
	}

	d.Tenant = cur.Tenant
	if d.Tenant == "" {
		d.Tenant = g.allocations[d.PCI]
	}
	d.Container = cur.Container
	d.ReservedFor = cur.ReservedFor
	if d.Tenant != "" && owner != "" && owner != d.Tenant {
		return ErrGroupShared
	}

	if d.Tenant != "" || owner != "" {
		if d.Healthy {
			d.Driver = DriverVFIO
			d.Bound = true
			delete(g.pendingRepair, d.PCI)
		} else {
			d.Driver = hostDriverOf(d)
			d.Bound = false
			if d.Tenant != "" {
				d.Healthy = false
				g.pendingRepair[d.PCI] = true
			}
		}
	}

	g.devices[d.PCI] = d
	if d.Tenant != "" {
		g.allocations[d.PCI] = d.Tenant
	} else {
		delete(g.allocations, d.PCI)
	}
	return nil
}

func (g *DevicePassthroughGuard) hotplugRemoveLocked(pci string) error {
	d, ok := g.devices[pci]
	if !ok {
		return ErrUnknownDevice
	}

	g.releaseGroupLocked(d.IOMMUGroup)
	delete(g.devices, pci)
	delete(g.allocations, pci)
	delete(g.failReset, pci)
	delete(g.pendingRepair, pci)
	return nil
}

// Restore reloads the device table after a restart.
func (g *DevicePassthroughGuard) Restore(state map[string]Device) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	devices := make(map[string]Device, len(state))
	for key, raw := range state {
		d := normalizeDevice(raw)
		pci := key
		if pci == "" {
			pci = d.PCI
		}
		if pci == "" {
			return ErrUnknownDevice
		}
		if _, exists := devices[pci]; exists {
			return ErrDeviceExists
		}
		d.PCI = pci
		devices[pci] = d
	}

	allocations := make(map[string]string)
	groupAllocs := make(map[int][]string)
	for pci, d := range devices {
		if d.Tenant == "" {
			continue
		}
		allocations[pci] = d.Tenant
		groupAllocs[d.IOMMUGroup] = append(groupAllocs[d.IOMMUGroup], pci)
	}

	for _, pcis := range groupAllocs {
		owner := ""
		for _, pci := range pcis {
			tenant := devices[pci].Tenant
			if owner == "" {
				owner = tenant
			} else if owner != tenant {
				return ErrGroupShared
			}
		}
		sort.Strings(pcis)
		for _, pci := range pcis[1:] {
			d := devices[pci]
			d.Tenant = ""
			d.Container = ""
			devices[pci] = d
			delete(allocations, pci)
		}
	}

	allGroups := make(map[int][]string)
	for pci, d := range devices {
		allGroups[d.IOMMUGroup] = append(allGroups[d.IOMMUGroup], pci)
	}

	pending := make(map[string]bool)
	for _, pcis := range allGroups {
		sort.Strings(pcis)
		owner := ""
		allocated := make([]string, 0)
		for _, pci := range pcis {
			if tenant := allocations[pci]; tenant != "" {
				owner = tenant
				allocated = append(allocated, pci)
			}
		}

		if owner == "" {
			for _, pci := range pcis {
				d := devices[pci]
				if d.Bound || d.Driver == DriverVFIO {
					d = g.unbindLocked(d)
				}
				d.Tenant = ""
				d.Container = ""
				devices[pci] = d
			}
			continue
		}

		complete := true
		for _, pci := range pcis {
			d := devices[pci]
			if !d.Healthy || !d.Bound || d.Driver != DriverVFIO {
				complete = false
				break
			}
		}
		if complete {
			continue
		}

		for _, pci := range pcis {
			d := devices[pci]
			d = g.unbindLocked(d)
			devices[pci] = d
		}
		for _, pci := range allocated {
			d := devices[pci]
			d.Healthy = false
			devices[pci] = d
			pending[pci] = true
		}
	}

	g.devices = devices
	g.allocations = allocations
	g.failReset = make(map[string]bool)
	g.pendingRepair = pending
	return nil
}

// Allocations reports pci to tenant mappings.
func (g *DevicePassthroughGuard) Allocations() map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	allocations := make(map[string]string, len(g.allocations))
	for pci, tenant := range g.allocations {
		if _, ok := g.devices[pci]; ok {
			allocations[pci] = tenant
		}
	}
	return allocations
}
