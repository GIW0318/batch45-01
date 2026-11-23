package task

import "errors"

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
type DevicePassthroughGuard struct{}

// NewDevicePassthroughGuard returns an empty manager.
func NewDevicePassthroughGuard() *DevicePassthroughGuard {
	return &DevicePassthroughGuard{}
}

// Register adds one device, rejecting duplicate PCI addresses.
func (g *DevicePassthroughGuard) Register(d Device) error {
	return errors.New("not implemented")
}

// RegisterGroup registers all functions of one IOMMU group.
func (g *DevicePassthroughGuard) RegisterGroup(devices []Device) error {
	return errors.New("not implemented")
}

// Allocate assigns pci to tenant/container. The whole IOMMU group is bound to
// vfio-pci atomically; a group may only ever serve one tenant.
func (g *DevicePassthroughGuard) Allocate(pci, tenant, container string) error {
	return errors.New("not implemented")
}

// Release frees one device and restores its host driver.
func (g *DevicePassthroughGuard) Release(pci string) error {
	return errors.New("not implemented")
}

// ReleaseContainer frees every device held by one container.
func (g *DevicePassthroughGuard) ReleaseContainer(container string) error {
	return errors.New("not implemented")
}

// ReleaseNode frees every device allocated on one node (container migration).
func (g *DevicePassthroughGuard) ReleaseNode(node string) error {
	return errors.New("not implemented")
}

// Reserve pins a device to one tenant.
func (g *DevicePassthroughGuard) Reserve(pci, tenant string) error {
	return errors.New("not implemented")
}

// Device returns one device.
func (g *DevicePassthroughGuard) Device(pci string) (Device, bool) {
	return Device{}, false
}

// Group returns the members of one IOMMU group ordered by PCI address.
func (g *DevicePassthroughGuard) Group(group int) []Device {
	return nil
}

// Reset resets one device.
func (g *DevicePassthroughGuard) Reset(pci string) error {
	return errors.New("not implemented")
}

// FailNextReset makes the next reset of pci fail once.
func (g *DevicePassthroughGuard) FailNextReset(pci string) error {
	return errors.New("not implemented")
}

// Hotplug applies one hotplug notification.
func (g *DevicePassthroughGuard) Hotplug(ev Hotplug) error {
	return errors.New("not implemented")
}

// Restore reloads the device table after a restart.
func (g *DevicePassthroughGuard) Restore(state map[string]Device) error {
	return errors.New("not implemented")
}

// Allocations reports pci to tenant mappings.
func (g *DevicePassthroughGuard) Allocations() map[string]string {
	return nil
}
