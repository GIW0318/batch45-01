package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Verdict is the TC action returned for a packet.
type Verdict int

const (
	VerdictDrop     Verdict = 0
	VerdictOK       Verdict = 1
	VerdictRedirect Verdict = 2
)

// Packet is the parsed packet metadata seen by the TC hook.
type Packet struct {
	SrcIP   string
	DstIP   string
	SrcPort uint16
	DstPort uint16
	Proto   uint8
	IfIndex int
}

// Policy is one redirect policy installed by the control plane.
type Policy struct {
	ID              string
	SrcIP           string
	DstIP           string
	SrcPort         uint16
	DstPort         uint16
	Proto           uint8
	Action          Verdict
	RedirectIfIndex int
}

// MapKey is the lookup key of the redirect map.
type MapKey struct {
	SrcIP   string
	DstIP   string
	SrcPort uint16
	DstPort uint16
	Proto   uint8
}

// TcProgram is the compiled TC program description.
type TcProgram struct {
	Policies []Policy
	Ifaces   []int
	Helpers  []string
}

// GuardOptions configures the redirect guard.
type GuardOptions struct {
	AllowedIfIndexes []int
	MapCapacity      int
	ConntrackSync    bool
}

// Loader installs a compiled TC program.
type Loader func(*TcProgram) error

// TcRedirectGuard owns the TC redirect policies and map state.
type TcRedirectGuard struct{}

// NewTcRedirectGuard returns a guard configured by opts.
func NewTcRedirectGuard(opts GuardOptions) *TcRedirectGuard {
	return &TcRedirectGuard{}
}

// AddPolicy installs a policy.
func (g *TcRedirectGuard) AddPolicy(p Policy) error {
	return errNotImplemented
}

// DeletePolicy removes a policy and its map entries.
func (g *TcRedirectGuard) DeletePolicy(id string) error {
	return errNotImplemented
}

// SetAllowedIfIndexes replaces the set of interfaces redirects may target.
func (g *TcRedirectGuard) SetAllowedIfIndexes(idxs []int) {}

// HookVerdict evaluates a packet at the TC hook.
func (g *TcRedirectGuard) HookVerdict(pkt Packet) (Verdict, int, error) {
	return VerdictDrop, 0, errNotImplemented
}

// KeyFor returns the map key of a packet.
func (g *TcRedirectGuard) KeyFor(pkt Packet) MapKey {
	return MapKey{}
}

// UpdateMap atomically replaces the redirect map content.
func (g *TcRedirectGuard) UpdateMap(entries map[MapKey]int) error {
	return errNotImplemented
}

// MapSnapshot returns a copy of the redirect map.
func (g *TcRedirectGuard) MapSnapshot() map[MapKey]int {
	return nil
}

// Attach attaches the program to the given interfaces.
func (g *TcRedirectGuard) Attach(ifaces []int) error {
	return errNotImplemented
}

// Attached lists the interfaces the program is attached to.
func (g *TcRedirectGuard) Attached() []int {
	return nil
}

// Compile builds the TC program description.
func (g *TcRedirectGuard) Compile() (*TcProgram, error) {
	return nil, errNotImplemented
}

// LoadWith installs the program with the supplied loader.
func (g *TcRedirectGuard) LoadWith(l Loader) error {
	return errNotImplemented
}

// Loaded reports whether a program is installed.
func (g *TcRedirectGuard) Loaded() bool {
	return false
}

// Blocking reports whether all traffic is dropped because no policy is loaded.
func (g *TcRedirectGuard) Blocking() bool {
	return false
}

// ActivePrograms reports how many programs are attached.
func (g *TcRedirectGuard) ActivePrograms() int {
	return 0
}

// ChecksumRequired reports whether the packet needs checksum recomputation.
func (g *TcRedirectGuard) ChecksumRequired(pkt Packet) bool {
	return false
}
