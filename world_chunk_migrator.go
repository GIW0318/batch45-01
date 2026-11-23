package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Chunk is a block coordinate in the open world grid.
type Chunk struct {
	X int
	Y int
}

// Entity is a world entity plus the chunk that owns it.
type Entity struct {
	ID    int
	Chunk Chunk
}

// ChunkMigrator owns chunk loading, unloading and entity migration.
type ChunkMigrator struct {
	chunkSize int
}

// NewChunkMigrator builds a migrator for the given chunk edge length.
func NewChunkMigrator(chunkSize int) *ChunkMigrator {
	return &ChunkMigrator{chunkSize: chunkSize}
}

// ChunkOf maps a world position onto its chunk, including negative coordinates.
func (m *ChunkMigrator) ChunkOf(x, y int) Chunk {
	return Chunk{}
}

// OwnerOf returns the single chunk that owns a position on a chunk seam.
func (m *ChunkMigrator) OwnerOf(x, y int) Chunk {
	return Chunk{}
}

// VisibleChunks lists the chunk itself plus its neighbours.
func (m *ChunkMigrator) VisibleChunks(c Chunk) []Chunk {
	return nil
}

// Load loads a chunk once and returns its version.
func (m *ChunkMigrator) Load(c Chunk) int {
	return 0
}

// Version reports the loaded version of a chunk.
func (m *ChunkMigrator) Version(c Chunk) int {
	return 0
}

// Spawn places an entity into a chunk and returns the stored entity.
func (m *ChunkMigrator) Spawn(c Chunk, id int) Entity {
	return Entity{}
}

// EntityCount is the number of entities currently owned by a chunk.
func (m *ChunkMigrator) EntityCount(c Chunk) int {
	return 0
}

// Migrate moves an entity to another chunk, preserving its id.
func (m *ChunkMigrator) Migrate(id int, to Chunk, fromVersion, toVersion int) (Entity, error) {
	return Entity{}, errNotImplemented
}

// CombatLock marks a chunk as containing entities that are still fighting.
func (m *ChunkMigrator) CombatLock(c Chunk) error {
	return errNotImplemented
}

// Unload removes a chunk unless entities there are still in combat.
func (m *ChunkMigrator) Unload(c Chunk) ([]int, error) {
	return nil, errNotImplemented
}

// PathAcross returns the chunk path between two chunks for boundary pathing.
func (m *ChunkMigrator) PathAcross(from, to Chunk) []Chunk {
	return nil
}

// AssignDrop picks the single owning chunk for a dropped item position.
func (m *ChunkMigrator) AssignDrop(x, y int) Chunk {
	return Chunk{}
}

// ConcurrentLoad simulates several players entering the same chunk at once.
func (m *ChunkMigrator) ConcurrentLoad(c Chunk, players int) int {
	return 0
}

// BeginMigrate starts the first phase of an atomic entity migration.
func (m *ChunkMigrator) BeginMigrate(id int, to Chunk) (string, error) {
	return "", errNotImplemented
}

// CommitMigrate finishes a migration started by BeginMigrate.
func (m *ChunkMigrator) CommitMigrate(token string) (bool, error) {
	return false, errNotImplemented
}

// Rebuild reconstructs the chunk index from persisted entities after a restart.
func (m *ChunkMigrator) Rebuild(entities []Entity) int {
	return 0
}

// Entering reports whether a player may enter, i.e. terrain is ready.
func (m *ChunkMigrator) Entering(c Chunk) bool {
	return false
}
