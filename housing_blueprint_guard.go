package task

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"
)

const (
	currentBlueprintVersion = 3
	maxBlueprintsPerOwner   = 20
)

var (
	errNotImplemented = errors.New("not implemented")
	errInvalidRequest = errors.New("invalid request")
)

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

type placedFurniture struct {
	Placement
	BlueprintID string
	Version     int
}

type applyKey struct {
	BlueprintID string
	Version     int
	PlayerID    string
	Terrain     string
}

type applyReceipt struct {
	Placements []Placement
}

// BlueprintGuard applies and deletes blueprints without duplicating furniture.
type BlueprintGuard struct {
	mu sync.Mutex

	furnitureCap int
	limited      map[string]struct{}

	blueprints map[string]Blueprint
	grants     map[string]map[string]struct{}
	placed     map[string][]placedFurniture
	receipts   map[applyKey]applyReceipt

	applyCounts map[string]int
	ownerCounts map[string]int
	importSeq   uint64
}

// NewBlueprintGuard builds an empty guard with the current blueprint version.
func NewBlueprintGuard() *BlueprintGuard {
	return &BlueprintGuard{
		furnitureCap: math.MaxInt,
		limited:      make(map[string]struct{}),
		blueprints:   make(map[string]Blueprint),
		grants:       make(map[string]map[string]struct{}),
		placed:       make(map[string][]placedFurniture),
		receipts:     make(map[applyKey]applyReceipt),
		applyCounts:  make(map[string]int),
		ownerCounts:  make(map[string]int),
	}
}

// CurrentVersion returns the blueprint format version written by this guard.
func (g *BlueprintGuard) CurrentVersion() int {
	return currentBlueprintVersion
}

// SetFurnitureCap configures how many furniture items a home may hold.
func (g *BlueprintGuard) SetFurnitureCap(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: furniture cap cannot be negative", errInvalidRequest)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.furnitureCap = n
	return nil
}

// SetLimitedFurniture marks the furniture ids that may exist only once per home.
func (g *BlueprintGuard) SetLimitedFurniture(ids []string) error {
	limited := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("%w: empty limited furniture id", errInvalidRequest)
		}
		limited[id] = struct{}{}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.limited = limited
	return nil
}

// CreateBlueprint stores a blueprint, keeping the ownership of every placement.
func (g *BlueprintGuard) CreateBlueprint(id, ownerID string, placements []Placement, at time.Time) error {
	if id == "" || ownerID == "" {
		return fmt.Errorf("%w: blueprint id and owner id are required", errInvalidRequest)
	}
	normalized, err := normalizePlacements(placements, ownerID)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.blueprints[id]; exists {
		return fmt.Errorf("%w: blueprint %q already exists", errInvalidRequest, id)
	}
	if g.ownerCounts[ownerID] >= maxBlueprintsPerOwner {
		return fmt.Errorf("%w: owner %q already has %d blueprints", errInvalidRequest, ownerID, maxBlueprintsPerOwner)
	}

	g.blueprints[id] = Blueprint{
		ID:         id,
		OwnerID:    ownerID,
		Version:    currentBlueprintVersion,
		Placements: normalized,
		At:         at,
	}
	g.ownerCounts[ownerID]++
	return nil
}

// Blueprint returns a stored blueprint.
func (g *BlueprintGuard) Blueprint(id string) (Blueprint, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	bp, ok := g.blueprints[id]
	if !ok {
		return Blueprint{}, false
	}
	return cloneBlueprint(bp), true
}

// GrantAccess lets another player apply a shared blueprint.
func (g *BlueprintGuard) GrantAccess(blueprintID, playerID string) error {
	if blueprintID == "" || playerID == "" {
		return fmt.Errorf("%w: blueprint id and player id are required", errInvalidRequest)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	_, ok := g.blueprints[blueprintID]
	if !ok {
		return fmt.Errorf("%w: blueprint %q not found", errInvalidRequest, blueprintID)
	}

	players := g.grants[blueprintID]
	if players == nil {
		players = make(map[string]struct{})
		g.grants[blueprintID] = players
	}
	players[playerID] = struct{}{}
	return nil
}

// SetShared marks a blueprint as shareable with other players.
func (g *BlueprintGuard) SetShared(blueprintID string, shared bool) error {
	if blueprintID == "" {
		return fmt.Errorf("%w: blueprint id is required", errInvalidRequest)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	bp, ok := g.blueprints[blueprintID]
	if !ok {
		return fmt.Errorf("%w: blueprint %q not found", errInvalidRequest, blueprintID)
	}
	bp.Shared = shared
	g.blueprints[blueprintID] = bp
	return nil
}

// Apply places every furniture of a blueprint in a home.
func (g *BlueprintGuard) Apply(blueprintID, playerID, terrain string, at time.Time) ([]Placement, error) {
	if blueprintID == "" || playerID == "" || terrain == "" {
		return nil, fmt.Errorf("%w: blueprint id, player id and terrain are required", errInvalidRequest)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	bp, ok := g.blueprints[blueprintID]
	if !ok {
		return nil, fmt.Errorf("%w: blueprint %q not found", errInvalidRequest, blueprintID)
	}
	if !g.canApplyLocked(bp, playerID) {
		return nil, fmt.Errorf("%w: player %q may not use blueprint %q", errInvalidRequest, playerID, blueprintID)
	}
	if err := validateTerrain(bp.Placements, terrain); err != nil {
		return nil, err
	}

	key := applyKey{
		BlueprintID: bp.ID,
		Version:     bp.Version,
		PlayerID:    playerID,
		Terrain:     terrain,
	}
	if receipt, ok := g.receipts[key]; ok {
		// A successful receipt is replayed for non-limited furniture. Limited
		// furniture cannot be replayed as a second placement, because that
		// would make the same unique item exist twice in one home.
		if g.hasLimitedConflictLocked(playerID, bp.Placements) {
			return nil, fmt.Errorf("%w: limited furniture already placed in player %q's home", errInvalidRequest, playerID)
		}
		return clonePlacements(receipt.Placements), nil
	}

	if err := g.validateCapacityAndLimitLocked(playerID, bp.Placements); err != nil {
		return nil, err
	}

	applied := make([]Placement, 0, len(bp.Placements))
	for _, source := range bp.Placements {
		placement := source
		if placement.OwnerID == "" {
			placement.OwnerID = playerID
		}
		if placement.Terrain == "" {
			placement.Terrain = terrain
		}
		applied = append(applied, placement)
	}

	records := g.placed[playerID]
	for _, placement := range applied {
		records = append(records, placedFurniture{
			Placement:   placement,
			BlueprintID: bp.ID,
			Version:     bp.Version,
		})
	}
	g.placed[playerID] = records
	g.applyCounts[bp.ID]++
	g.receipts[key] = applyReceipt{Placements: clonePlacements(applied)}

	return clonePlacements(applied), nil
}

// ApplyCount reports how many times a blueprint was applied.
func (g *BlueprintGuard) ApplyCount(blueprintID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.applyCounts[blueprintID]
}

// Placed lists the furniture currently placed in a player home.
func (g *BlueprintGuard) Placed(playerID string) []Placement {
	g.mu.Lock()
	defer g.mu.Unlock()

	records := g.placed[playerID]
	out := make([]Placement, 0, len(records))
	for _, record := range records {
		out = append(out, record.Placement)
	}
	return out
}

// Delete removes a blueprint and reclaims the furniture it placed.
func (g *BlueprintGuard) Delete(blueprintID string) ([]Placement, error) {
	if blueprintID == "" {
		return nil, fmt.Errorf("%w: blueprint id is required", errInvalidRequest)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	bp, ok := g.blueprints[blueprintID]
	if !ok {
		return nil, fmt.Errorf("%w: blueprint %q not found", errInvalidRequest, blueprintID)
	}

	delete(g.blueprints, blueprintID)
	delete(g.grants, blueprintID)
	delete(g.applyCounts, blueprintID)
	for key := range g.receipts {
		if key.BlueprintID == blueprintID {
			delete(g.receipts, key)
		}
	}
	if g.ownerCounts[bp.OwnerID] <= 1 {
		delete(g.ownerCounts, bp.OwnerID)
	} else {
		g.ownerCounts[bp.OwnerID]--
	}

	reclaimed := make([]Placement, 0)
	for playerID, records := range g.placed {
		kept := make([]placedFurniture, 0, len(records))
		for _, record := range records {
			if record.BlueprintID == blueprintID {
				reclaimed = append(reclaimed, record.Placement)
				continue
			}
			kept = append(kept, record)
		}
		if len(kept) == 0 {
			delete(g.placed, playerID)
		} else {
			g.placed[playerID] = kept
		}
	}
	return reclaimed, nil
}

// Import stores a blueprint from another server, remapping conflicting ids.
func (g *BlueprintGuard) Import(bp Blueprint, at time.Time) (string, error) {
	if bp.Version < 0 || bp.Version > currentBlueprintVersion {
		return "", fmt.Errorf("%w: unsupported blueprint version %d", errInvalidRequest, bp.Version)
	}

	ownerID := bp.OwnerID
	if ownerID == "" {
		for _, placement := range bp.Placements {
			if placement.OwnerID != "" {
				ownerID = placement.OwnerID
				break
			}
		}
	}
	if ownerID == "" {
		return "", fmt.Errorf("%w: imported blueprint has no owner", errInvalidRequest)
	}
	placements, err := normalizePlacements(bp.Placements, ownerID)
	if err != nil {
		return "", err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.ownerCounts[ownerID] >= maxBlueprintsPerOwner {
		return "", fmt.Errorf("%w: owner %q already has %d blueprints", errInvalidRequest, ownerID, maxBlueprintsPerOwner)
	}

	blueprintID := bp.ID
	if blueprintID == "" {
		blueprintID = g.freshBlueprintIDLocked("imported")
	} else if _, exists := g.blueprints[blueprintID]; exists {
		blueprintID = g.freshBlueprintIDLocked(blueprintID)
	}

	known := g.knownFurnitureIDsLocked()
	for i := range placements {
		furnitureID := placements[i].FurnitureID
		if _, exists := known[furnitureID]; exists {
			furnitureID = g.freshFurnitureIDLocked(furnitureID, known)
			placements[i].FurnitureID = furnitureID
		}
		known[furnitureID] = struct{}{}
	}

	importedAt := at
	if importedAt.IsZero() {
		importedAt = bp.At
	}
	g.blueprints[blueprintID] = Blueprint{
		ID:         blueprintID,
		OwnerID:    ownerID,
		Version:    currentBlueprintVersion,
		Placements: placements,
		Shared:     bp.Shared,
		At:         importedAt,
	}
	g.ownerCounts[ownerID]++
	return blueprintID, nil
}

// Export returns a stored blueprint for transfer to another server.
func (g *BlueprintGuard) Export(blueprintID string) (Blueprint, error) {
	if blueprintID == "" {
		return Blueprint{}, fmt.Errorf("%w: blueprint id is required", errInvalidRequest)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	bp, ok := g.blueprints[blueprintID]
	if !ok {
		return Blueprint{}, fmt.Errorf("%w: blueprint %q not found", errInvalidRequest, blueprintID)
	}
	return cloneBlueprint(bp), nil
}

func (g *BlueprintGuard) canApplyLocked(bp Blueprint, playerID string) bool {
	if playerID == bp.OwnerID {
		return true
	}
	if !bp.Shared {
		return false
	}
	_, granted := g.grants[bp.ID][playerID]
	return granted
}

func (g *BlueprintGuard) validateCapacityAndLimitLocked(playerID string, placements []Placement) error {
	if g.hasLimitedConflictLocked(playerID, placements) {
		return fmt.Errorf("%w: limited furniture already placed in player %q's home", errInvalidRequest, playerID)
	}
	existing := len(g.placed[playerID])
	if existing > g.furnitureCap-len(placements) {
		return fmt.Errorf("%w: furniture cap %d would be exceeded", errInvalidRequest, g.furnitureCap)
	}
	return nil
}

func (g *BlueprintGuard) hasLimitedConflictLocked(playerID string, placements []Placement) bool {
	existing := make(map[string]int)
	for _, record := range g.placed[playerID] {
		if _, limited := g.limited[record.FurnitureID]; limited {
			existing[record.FurnitureID]++
		}
	}

	seen := make(map[string]struct{})
	for _, placement := range placements {
		if _, limited := g.limited[placement.FurnitureID]; !limited {
			continue
		}
		if existing[placement.FurnitureID] > 0 {
			return true
		}
		if _, duplicate := seen[placement.FurnitureID]; duplicate {
			return true
		}
		seen[placement.FurnitureID] = struct{}{}
	}
	return false
}

func (g *BlueprintGuard) knownFurnitureIDsLocked() map[string]struct{} {
	known := make(map[string]struct{})
	for _, bp := range g.blueprints {
		for _, placement := range bp.Placements {
			known[placement.FurnitureID] = struct{}{}
		}
	}
	for _, records := range g.placed {
		for _, record := range records {
			known[record.FurnitureID] = struct{}{}
		}
	}
	return known
}

func (g *BlueprintGuard) freshBlueprintIDLocked(base string) string {
	if base == "" {
		base = "imported"
	}
	for {
		g.importSeq++
		candidate := base + "-import-" + strconv.FormatUint(g.importSeq, 10)
		if _, exists := g.blueprints[candidate]; !exists {
			return candidate
		}
	}
}

func (g *BlueprintGuard) freshFurnitureIDLocked(base string, known map[string]struct{}) string {
	if base == "" {
		base = "furniture"
	}
	for {
		g.importSeq++
		candidate := base + "-import-" + strconv.FormatUint(g.importSeq, 10)
		if _, exists := known[candidate]; !exists {
			return candidate
		}
	}
}

func normalizePlacements(placements []Placement, ownerID string) ([]Placement, error) {
	normalized := make([]Placement, len(placements))
	for i, placement := range placements {
		if placement.FurnitureID == "" {
			return nil, fmt.Errorf("%w: placement %d has no furniture id", errInvalidRequest, i)
		}
		if placement.OwnerID == "" {
			placement.OwnerID = ownerID
		}
		normalized[i] = placement
	}
	return normalized, nil
}

func validateTerrain(placements []Placement, terrain string) error {
	for _, placement := range placements {
		if placement.Terrain != "" && placement.Terrain != terrain {
			return fmt.Errorf("%w: blueprint terrain %q does not match %q", errInvalidRequest, placement.Terrain, terrain)
		}
	}
	return nil
}

func cloneBlueprint(bp Blueprint) Blueprint {
	bp.Placements = clonePlacements(bp.Placements)
	return bp
}

func clonePlacements(placements []Placement) []Placement {
	if placements == nil {
		return nil
	}
	cloned := make([]Placement, len(placements))
	copy(cloned, placements)
	return cloned
}
