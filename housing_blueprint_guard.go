package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Placement is one furniture item placed in a home.
type Placement struct {
	FurnitureID string
	OwnerID     string
	X           float64
	Y           float64
	Rotation    int
	Durability  int
	Terrain     string
}

// Blueprint is a saved home layout.
type Blueprint struct {
	ID         string
	OwnerID    string
	Version    int
	Placements []Placement
	Shared     bool
	At         time.Time
}

// BlueprintGuard applies and deletes blueprints without duplicating furniture.
type BlueprintGuard struct {
}

// NewBlueprintGuard builds an empty guard with the current blueprint version.
func NewBlueprintGuard() *BlueprintGuard {
	return &BlueprintGuard{}
}

// CurrentVersion returns the blueprint format version written by this guard.
func (g *BlueprintGuard) CurrentVersion() int {
	return 0
}

// SetFurnitureCap configures how many furniture items a home may hold.
func (g *BlueprintGuard) SetFurnitureCap(n int) error {
	return errNotImplemented
}

// SetLimitedFurniture marks the furniture ids that may exist only once per home.
func (g *BlueprintGuard) SetLimitedFurniture(ids []string) error {
	return errNotImplemented
}

// CreateBlueprint stores a blueprint, keeping the ownership of every placement.
func (g *BlueprintGuard) CreateBlueprint(id, ownerID string, placements []Placement, at time.Time) error {
	return errNotImplemented
}

// Blueprint returns a stored blueprint.
func (g *BlueprintGuard) Blueprint(id string) (Blueprint, bool) {
	return Blueprint{}, false
}

// GrantAccess lets another player apply a shared blueprint.
func (g *BlueprintGuard) GrantAccess(blueprintID, playerID string) error {
	return errNotImplemented
}

// SetShared marks a blueprint as shareable with other players.
func (g *BlueprintGuard) SetShared(blueprintID string, shared bool) error {
	return errNotImplemented
}

// Apply places every furniture of a blueprint in a home.
func (g *BlueprintGuard) Apply(blueprintID, playerID, terrain string, at time.Time) ([]Placement, error) {
	return nil, errNotImplemented
}

// ApplyCount reports how many times a blueprint was applied.
func (g *BlueprintGuard) ApplyCount(blueprintID string) int {
	return 0
}

// Placed lists the furniture currently placed in a player home.
func (g *BlueprintGuard) Placed(playerID string) []Placement {
	return nil
}

// Delete removes a blueprint and reclaims the furniture it placed.
func (g *BlueprintGuard) Delete(blueprintID string) ([]Placement, error) {
	return nil, errNotImplemented
}

// Import stores a blueprint from another server, remapping conflicting ids.
func (g *BlueprintGuard) Import(bp Blueprint, at time.Time) (string, error) {
	return "", errNotImplemented
}

// Export returns a stored blueprint for transfer to another server.
func (g *BlueprintGuard) Export(blueprintID string) (Blueprint, error) {
	return Blueprint{}, errNotImplemented
}
