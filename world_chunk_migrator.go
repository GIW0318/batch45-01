package task

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
)

// errNotImplemented is retained for compatibility with the original skeleton.
var errNotImplemented = errors.New("not implemented")

var (
	// ErrResidentLimit is returned when loading another chunk would exceed the
	// resident chunk table limit.
	ErrResidentLimit = errors.New("resident chunk limit reached")
	// ErrChunkNotLoaded is returned when an operation needs a resident chunk.
	ErrChunkNotLoaded = errors.New("chunk is not loaded")
	// ErrChunkInCombat is returned when unloading a chunk with active combat.
	ErrChunkInCombat = errors.New("chunk has entities in combat")
	// ErrChunkMigrating is returned when unloading a chunk involved in a
	// two-phase migration.
	ErrChunkMigrating = errors.New("chunk has a pending migration")
	// ErrVersionMismatch is returned when a caller's chunk version is stale.
	ErrVersionMismatch = errors.New("chunk version mismatch")
	// ErrEntityNotFound is returned when an entity id is not in the live index.
	ErrEntityNotFound = errors.New("entity not found")
	// ErrEntityPending is returned when an entity already has a migration.
	ErrEntityPending = errors.New("entity already has a pending migration")
	// ErrMigrationNotFound is returned for an unknown migration token.
	ErrMigrationNotFound = errors.New("migration not found")
	// ErrMigrationConflict is returned when a migration can no longer commit.
	ErrMigrationConflict = errors.New("migration checkpoint conflict")
	// ErrLoadCanceled is returned when a chunk is unloaded before loading ends.
	ErrLoadCanceled = errors.New("chunk load canceled")
)

const defaultMaxResidentChunks = 1024

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

type chunkState struct {
	chunk     Chunk
	version   int
	loaded    bool
	entering  bool
	loading   bool
	canceled  bool
	combat    bool
	initCount int
	entities  map[int]struct{}
	loadDone  chan struct{}
}

type migrationRecord struct {
	id          int
	from        Chunk
	to          Chunk
	fromVersion int
	toVersion   int
}

// ChunkMigrator owns chunk loading, unloading and entity migration.
type ChunkMigrator struct {
	chunkSize int
	maxChunks int

	mu sync.Mutex

	chunks   map[Chunk]*chunkState
	entities map[int]Entity

	// nextVersion keeps versions monotonic across unload/reload cycles.
	nextVersion map[Chunk]int

	// persisted is the checkpoint owned by this migrator. It is updated only at
	// committed ownership changes, so Rebuild can restore a consistent index
	// after a restart. Unloading removes entities from the live index but leaves
	// them in this durable snapshot for the caller to persist/reload.
	persisted map[int]Entity

	pending         map[string]*migrationRecord
	pendingByEntity map[int]string
	pendingByChunk  map[Chunk]int
	nextToken       uint64
}

// NewChunkMigrator builds a migrator for the given chunk edge length.
func NewChunkMigrator(chunkSize int) *ChunkMigrator {
	return NewChunkMigratorWithLimit(chunkSize, defaultMaxResidentChunks)
}

// NewChunkMigratorWithLimit builds a migrator with an explicit resident chunk
// table limit. A non-positive limit falls back to the default.
func NewChunkMigratorWithLimit(chunkSize, maxChunks int) *ChunkMigrator {
	if chunkSize <= 0 {
		chunkSize = 1
	}
	if maxChunks <= 0 {
		maxChunks = defaultMaxResidentChunks
	}
	return &ChunkMigrator{
		chunkSize:       chunkSize,
		maxChunks:       maxChunks,
		chunks:          make(map[Chunk]*chunkState),
		entities:        make(map[int]Entity),
		nextVersion:     make(map[Chunk]int),
		persisted:       make(map[int]Entity),
		pending:         make(map[string]*migrationRecord),
		pendingByEntity: make(map[int]string),
		pendingByChunk:  make(map[Chunk]int),
	}
}

// SetMaxResidentChunks changes the resident chunk table limit. It refuses a
// limit below the number of chunks that are currently resident.
func (m *ChunkMigrator) SetMaxResidentChunks(limit int) error {
	if limit <= 0 {
		return errors.New("max resident chunks must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < len(m.chunks) {
		return ErrResidentLimit
	}
	m.maxChunks = limit
	return nil
}

// ResidentChunks reports the current resident chunk table size.
func (m *ChunkMigrator) ResidentChunks() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.chunks)
}

// MaxResidentChunks reports the configured resident chunk table limit.
func (m *ChunkMigrator) MaxResidentChunks() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxChunks
}

// ChunkOf maps a world position onto its chunk, including negative coordinates.
func (m *ChunkMigrator) ChunkOf(x, y int) Chunk {
	return Chunk{X: floorDiv(x, m.chunkSize), Y: floorDiv(y, m.chunkSize)}
}

// OwnerOf returns the single chunk that owns a position on a chunk seam.
// floorDiv already puts a seam coordinate into the chunk on its positive side,
// so a point such as (32, 32) belongs only to chunk {1, 1}.
func (m *ChunkMigrator) OwnerOf(x, y int) Chunk {
	return m.ChunkOf(x, y)
}

// VisibleChunks lists the chunk itself plus its eight neighbours.
func (m *ChunkMigrator) VisibleChunks(c Chunk) []Chunk {
	out := make([]Chunk, 0, 9)
	out = append(out, c)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			out = append(out, Chunk{X: c.X + dx, Y: c.Y + dy})
		}
	}
	return out
}

// Load loads a chunk once and returns its version. It returns 0 when the load
// cannot start because the resident table is full or the load was canceled by
// a concurrent Unload. Use LoadErr when the reason matters.
func (m *ChunkMigrator) Load(c Chunk) int {
	version, _ := m.LoadErr(c)
	return version
}

// LoadErr loads a chunk once and reports capacity or cancellation failures.
func (m *ChunkMigrator) LoadErr(c Chunk) (int, error) {
	for {
		m.mu.Lock()
		if st := m.chunks[c]; st != nil {
			if st.loaded {
				version := st.version
				m.mu.Unlock()
				return version, nil
			}
			if st.loading {
				done := st.loadDone
				m.mu.Unlock()
				<-done

				m.mu.Lock()
				if st.loaded {
					version := st.version
					m.mu.Unlock()
					return version, nil
				}
				m.mu.Unlock()
				return 0, ErrLoadCanceled
			}
			m.mu.Unlock()
			return 0, ErrChunkNotLoaded
		}

		if len(m.chunks) >= m.maxChunks {
			m.mu.Unlock()
			return 0, ErrResidentLimit
		}

		version := m.nextVersion[c]
		if version < 1 {
			version = 1
		}
		st := &chunkState{
			chunk:    c,
			version:  version,
			loading:  true,
			entities: make(map[int]struct{}),
			loadDone: make(chan struct{}),
		}
		m.chunks[c] = st
		m.mu.Unlock()

		go m.finishLoad(st)
		<-st.loadDone

		m.mu.Lock()
		if st.loaded {
			loadedVersion := st.version
			m.mu.Unlock()
			return loadedVersion, nil
		}
		m.mu.Unlock()
		return 0, ErrLoadCanceled
	}
}

// LoadWithError is an alias for LoadErr.
func (m *ChunkMigrator) LoadWithError(c Chunk) (int, error) {
	return m.LoadErr(c)
}

func (m *ChunkMigrator) finishLoad(st *chunkState) {
	// Give a concurrent Unload a chance to cancel an in-flight load. The loader
	// still always exits and signals completion, so cancellation cannot leak a
	// goroutine.
	runtime.Gosched()

	m.mu.Lock()
	if st.canceled || m.chunks[st.chunk] != st {
		m.mu.Unlock()
		close(st.loadDone)
		return
	}

	st.loading = false
	st.loaded = true
	st.entering = true
	st.initCount++
	if m.nextVersion[st.chunk] <= st.version {
		m.nextVersion[st.chunk] = st.version + 1
	}
	m.mu.Unlock()
	close(st.loadDone)
}

// Version reports the loaded version of a chunk, or 0 when it is not resident.
func (m *ChunkMigrator) Version(c Chunk) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.chunks[c]; st != nil && st.loaded {
		return st.version
	}
	return 0
}

// Spawn places an entity into a chunk and returns the stored entity.
func (m *ChunkMigrator) Spawn(c Chunk, id int) Entity {
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := m.LoadErr(c); err != nil {
			return Entity{}
		}

		m.mu.Lock()
		st := m.chunks[c]
		if st == nil || !st.loaded {
			m.mu.Unlock()
			continue
		}

		if old, ok := m.entities[id]; ok {
			if old.Chunk == c {
				m.mu.Unlock()
				return old
			}
			if oldState := m.chunks[old.Chunk]; oldState != nil {
				delete(oldState.entities, id)
			}
			m.cancelPendingLocked(id)
		}

		entity := Entity{ID: id, Chunk: c}
		m.entities[id] = entity
		m.persisted[id] = entity
		st.entities[id] = struct{}{}
		m.mu.Unlock()
		return entity
	}
	return Entity{}
}

// EntityCount is the number of entities currently owned by a chunk.
func (m *ChunkMigrator) EntityCount(c Chunk) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.chunks[c]; st != nil {
		return len(st.entities)
	}
	return 0
}

// Migrate moves an entity to another chunk, preserving its id.
func (m *ChunkMigrator) Migrate(id int, to Chunk, fromVersion, toVersion int) (Entity, error) {
	// The destination may be the neighbouring chunk that is about to become
	// visible. Load it first so validation uses its real version rather than a
	// synthetic zero.
	if _, err := m.LoadErr(to); err != nil {
		return Entity{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.pendingByEntity[id]; ok {
		return Entity{}, ErrEntityPending
	}
	entity, ok := m.entities[id]
	if !ok {
		return Entity{}, ErrEntityNotFound
	}

	from := entity.Chunk
	fromState := m.chunks[from]
	toState := m.chunks[to]
	if fromState == nil || !fromState.loaded {
		return Entity{}, ErrChunkNotLoaded
	}
	if toState == nil || !toState.loaded {
		return Entity{}, ErrChunkNotLoaded
	}
	if fromState.version != fromVersion || toState.version != toVersion {
		return Entity{}, ErrVersionMismatch
	}
	if from == to {
		return entity, nil
	}

	delete(fromState.entities, id)
	toState.entities[id] = struct{}{}
	entity.Chunk = to
	m.entities[id] = entity
	m.persisted[id] = entity
	return entity, nil
}

// CombatLock marks a chunk as containing entities that are still fighting.
func (m *ChunkMigrator) CombatLock(c Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.chunks[c]
	if st == nil || !st.loaded {
		return ErrChunkNotLoaded
	}
	st.combat = true
	return nil
}

// CombatUnlock clears the combat lock for a chunk.
func (m *ChunkMigrator) CombatUnlock(c Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.chunks[c]
	if st == nil || !st.loaded {
		return ErrChunkNotLoaded
	}
	st.combat = false
	return nil
}

// Unload removes a chunk unless entities there are still in combat.
func (m *ChunkMigrator) Unload(c Chunk) ([]int, error) {
	m.mu.Lock()
	st := m.chunks[c]
	if st == nil || !st.loaded {
		if st != nil && st.loading {
			return m.cancelLoadingLocked(c, st), nil
		}
		m.mu.Unlock()
		return nil, ErrChunkNotLoaded
	}
	if st.combat {
		m.mu.Unlock()
		return nil, ErrChunkInCombat
	}
	if m.pendingByChunk[c] > 0 {
		m.mu.Unlock()
		return nil, ErrChunkMigrating
	}

	ids := make([]int, 0, len(st.entities))
	for id := range st.entities {
		ids = append(ids, id)
	}
	delete(m.chunks, c)
	for _, id := range ids {
		delete(m.entities, id)
	}
	m.mu.Unlock()

	sort.Ints(ids)
	return ids, nil
}

func (m *ChunkMigrator) cancelLoadingLocked(c Chunk, st *chunkState) []int {
	st.canceled = true
	delete(m.chunks, c)
	done := st.loadDone
	m.mu.Unlock()
	<-done
	return []int{}
}

// PathAcross returns a continuous four-neighbour chunk path including both
// endpoints, suitable for boundary pathing.
func (m *ChunkMigrator) PathAcross(from, to Chunk) []Chunk {
	path := make([]Chunk, 0, abs(from.X-to.X)+abs(from.Y-to.Y)+1)
	cur := from
	path = append(path, cur)
	for cur.X != to.X {
		if cur.X < to.X {
			cur.X++
		} else {
			cur.X--
		}
		path = append(path, cur)
	}
	for cur.Y != to.Y {
		if cur.Y < to.Y {
			cur.Y++
		} else {
			cur.Y--
		}
		path = append(path, cur)
	}
	return path
}

// AssignDrop picks the single owning chunk for a dropped item position.
func (m *ChunkMigrator) AssignDrop(x, y int) Chunk {
	return m.OwnerOf(x, y)
}

// ConcurrentLoad simulates several players entering the same chunk at once and
// returns how many times that resident chunk was initialised.
func (m *ChunkMigrator) ConcurrentLoad(c Chunk, players int) int {
	if players < 1 {
		players = 1
	}

	var wg sync.WaitGroup
	wg.Add(players)
	for i := 0; i < players; i++ {
		go func() {
			defer wg.Done()
			m.Load(c)
		}()
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.chunks[c]; st != nil {
		return st.initCount
	}
	return 0
}

// BeginMigrate starts the first phase of an atomic entity migration.
func (m *ChunkMigrator) BeginMigrate(id int, to Chunk) (string, error) {
	if _, err := m.LoadErr(to); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.pendingByEntity[id]; ok {
		return "", ErrEntityPending
	}
	entity, ok := m.entities[id]
	if !ok {
		return "", ErrEntityNotFound
	}

	from := entity.Chunk
	fromState := m.chunks[from]
	toState := m.chunks[to]
	if fromState == nil || !fromState.loaded {
		return "", ErrChunkNotLoaded
	}
	if toState == nil || !toState.loaded {
		return "", ErrChunkNotLoaded
	}

	m.nextToken++
	token := fmt.Sprintf("mig-%d-%d", m.nextToken, id)
	record := &migrationRecord{
		id:          id,
		from:        from,
		to:          to,
		fromVersion: fromState.version,
		toVersion:   toState.version,
	}
	m.pending[token] = record
	m.pendingByEntity[id] = token
	m.pendingByChunk[from]++
	if to != from {
		m.pendingByChunk[to]++
	}

	// The checkpoint keeps the entity in its source chunk until CommitMigrate.
	// Therefore a restart between the two phases still rebuilds the old owner.
	return token, nil
}

// CommitMigrate finishes a migration started by BeginMigrate.
func (m *ChunkMigrator) CommitMigrate(token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.pending[token]
	if !ok {
		return false, ErrMigrationNotFound
	}
	m.removePendingLocked(token, record)

	entity, ok := m.entities[record.id]
	if !ok || entity.Chunk != record.from {
		return false, ErrMigrationConflict
	}
	if record.from == record.to {
		return true, nil
	}

	fromState := m.chunks[record.from]
	toState := m.chunks[record.to]
	if fromState == nil || !fromState.loaded || toState == nil || !toState.loaded {
		return false, ErrMigrationConflict
	}
	if fromState.version != record.fromVersion || toState.version != record.toVersion {
		return false, ErrVersionMismatch
	}

	// Single atomic ownership handoff: one deletion and one insertion while the
	// global entity id still maps to exactly one chunk.
	delete(fromState.entities, record.id)
	toState.entities[record.id] = struct{}{}
	entity.Chunk = record.to
	m.entities[record.id] = entity
	m.persisted[record.id] = entity
	return true, nil
}

func (m *ChunkMigrator) cancelPendingLocked(id int) {
	token, ok := m.pendingByEntity[id]
	if !ok {
		return
	}
	if record := m.pending[token]; record != nil {
		m.removePendingLocked(token, record)
	}
}

func (m *ChunkMigrator) removePendingLocked(token string, record *migrationRecord) {
	delete(m.pending, token)
	delete(m.pendingByEntity, record.id)
	if m.pendingByChunk[record.from] > 1 {
		m.pendingByChunk[record.from]--
	} else {
		delete(m.pendingByChunk, record.from)
	}
	if record.to != record.from {
		if m.pendingByChunk[record.to] > 1 {
			m.pendingByChunk[record.to]--
		} else {
			delete(m.pendingByChunk, record.to)
		}
	}
}

// Rebuild reconstructs the chunk index from persisted entities after a restart.
func (m *ChunkMigrator) Rebuild(entities []Entity) int {
	m.mu.Lock()

	var oldLoads []*chunkState
	for _, st := range m.chunks {
		if st.loading {
			st.canceled = true
			oldLoads = append(oldLoads, st)
		}
	}

	m.chunks = make(map[Chunk]*chunkState)
	m.entities = make(map[int]Entity)
	m.nextVersion = make(map[Chunk]int)
	m.persisted = make(map[int]Entity)
	m.pending = make(map[string]*migrationRecord)
	m.pendingByEntity = make(map[int]string)
	m.pendingByChunk = make(map[Chunk]int)

	for _, entity := range entities {
		if previous, ok := m.entities[entity.ID]; ok {
			if previousState := m.chunks[previous.Chunk]; previousState != nil {
				delete(previousState.entities, entity.ID)
			}
		}

		st := m.chunks[entity.Chunk]
		if st == nil {
			st = &chunkState{
				chunk:     entity.Chunk,
				version:   1,
				loaded:    true,
				entering:  true,
				initCount: 1,
				entities:  make(map[int]struct{}),
			}
			m.chunks[entity.Chunk] = st
			m.nextVersion[entity.Chunk] = 2
		}
		st.entities[entity.ID] = struct{}{}
		m.entities[entity.ID] = entity
		m.persisted[entity.ID] = entity
	}

	size := len(m.entities)
	m.mu.Unlock()

	// Wait for any load goroutines from before the restart to observe the
	// cancellation and exit, so a rebuild never leaves work running in the
	// background.
	for _, st := range oldLoads {
		<-st.loadDone
	}
	return size
}

// Snapshot returns the durable entity ownership checkpoint in id order. It is
// the input expected by Rebuild after a process restart.
func (m *ChunkMigrator) Snapshot() []Entity {
	m.mu.Lock()
	out := make([]Entity, 0, len(m.persisted))
	for _, entity := range m.persisted {
		out = append(out, entity)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PersistedEntities is an alias for Snapshot.
func (m *ChunkMigrator) PersistedEntities() []Entity {
	return m.Snapshot()
}

// EntityIDs returns the live entity ids owned by a chunk in ascending order.
func (m *ChunkMigrator) EntityIDs(c Chunk) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.chunks[c]
	if st == nil {
		return nil
	}
	ids := make([]int, 0, len(st.entities))
	for id := range st.entities {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// Entering reports whether a player may enter, i.e. terrain is ready.
func (m *ChunkMigrator) Entering(c Chunk) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.chunks[c]
	return st != nil && st.loaded && st.entering
}

func floorDiv(n, d int) int {
	if n >= 0 {
		return n / d
	}
	return -((-n + d - 1) / d)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
