package task

import (
	"testing"

	impl "gsb/batch45-game-a-02"
)

func TestChunkOfHandlesNegativeWorldCoords(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	if got := m.ChunkOf(-1, -1); got != (impl.Chunk{X: -1, Y: -1}) {
		t.Fatalf("chunk of (-1,-1) = %+v, want {-1 -1}", got)
	}
	if got := m.ChunkOf(33, 1); got != (impl.Chunk{X: 1, Y: 0}) {
		t.Fatalf("chunk of (33,1) = %+v, want {1 0}", got)
	}
}

func TestOwnerOfSeamPositionIsSingleChunk(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	if got := m.OwnerOf(32, 32); got != (impl.Chunk{X: 1, Y: 1}) {
		t.Fatalf("seam position owned by %+v, want exactly {1 1}", got)
	}
}

func TestVisibleChunksCoversNeighbours(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	got := m.VisibleChunks(impl.Chunk{X: 5, Y: 5})
	if len(got) != 9 {
		t.Fatalf("visible chunks = %d, want the 3x3 neighbourhood", len(got))
	}
	seen := make(map[impl.Chunk]bool, len(got))
	for _, c := range got {
		seen[c] = true
	}
	if !seen[impl.Chunk{X: 4, Y: 4}] || !seen[impl.Chunk{X: 6, Y: 6}] {
		t.Fatalf("visible chunks must include diagonal neighbours, got %+v", got)
	}
}

func TestLoadIsIdempotentPerChunk(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	c := impl.Chunk{X: 2, Y: 3}
	first := m.Load(c)
	if first == 0 {
		t.Fatalf("first load must return a real chunk version, got 0")
	}
	if second := m.Load(c); second != first {
		t.Fatalf("second load returned version %d, want the same version %d", second, first)
	}
}

func TestSpawnKeepsEntityInOwningChunk(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	c := impl.Chunk{X: 4, Y: 4}
	m.Load(c)
	got := m.Spawn(c, 77)
	if got.ID != 77 {
		t.Fatalf("spawned entity id = %d, want 77", got.ID)
	}
	if got.Chunk != c {
		t.Fatalf("spawned entity chunk = %+v, want %+v", got.Chunk, c)
	}
	if m.EntityCount(c) != 1 {
		t.Fatalf("chunk entity count = %d, want 1", m.EntityCount(c))
	}
}

func TestMigratePreservesEntityID(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	from := impl.Chunk{X: 0, Y: 0}
	to := impl.Chunk{X: 1, Y: 0}
	m.Load(from)
	m.Spawn(from, 501)
	moved, err := m.Migrate(501, to, 1, 1)
	if err != nil {
		t.Fatalf("migrate returned error: %v", err)
	}
	if moved.ID != 501 {
		t.Fatalf("entity id after migration = %d, want the original 501", moved.ID)
	}
}

func TestMigrateRejectsChunkVersionMismatch(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	from := impl.Chunk{X: 0, Y: 0}
	to := impl.Chunk{X: 2, Y: 0}
	m.Load(from)
	m.Load(to)
	m.Spawn(from, 9)
	if _, err := m.Migrate(9, to, 1, 1); err != nil {
		t.Fatalf("migration with matching versions failed: %v", err)
	}
	if _, err := m.Migrate(9, from, 1, 7); err == nil {
		t.Fatalf("migration with mismatched chunk versions must be rejected")
	}
}

func TestMigrateRemovesEntityFromOldChunk(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	from := impl.Chunk{X: 3, Y: 3}
	to := impl.Chunk{X: 4, Y: 3}
	m.Load(from)
	m.Load(to)
	m.Spawn(from, 12)
	if _, err := m.Migrate(12, to, 1, 1); err != nil {
		t.Fatalf("migrate returned error: %v", err)
	}
	if got := m.EntityCount(from); got != 0 {
		t.Fatalf("old chunk still owns %d entities, want 0", got)
	}
	if got := m.EntityCount(to); got != 1 {
		t.Fatalf("new chunk owns %d entities, want 1", got)
	}
}

func TestUnloadRefusesChunksWithEntitiesInCombat(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	free := impl.Chunk{X: 5, Y: 5}
	m.Load(free)
	if _, err := m.Unload(free); err != nil {
		t.Fatalf("unloading an idle chunk failed: %v", err)
	}
	busy := impl.Chunk{X: 6, Y: 6}
	m.Load(busy)
	m.CombatLock(busy)
	if _, err := m.Unload(busy); err == nil {
		t.Fatalf("unloading a chunk whose entities are fighting must be refused")
	}
}

func TestPathAcrossChunksForBoundaryPathing(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	got := m.PathAcross(impl.Chunk{X: 0, Y: 0}, impl.Chunk{X: 2, Y: 0})
	if len(got) != 3 {
		t.Fatalf("cross chunk path has %d chunks, want 3", len(got))
	}
	if got[0] != (impl.Chunk{X: 0, Y: 0}) || got[2] != (impl.Chunk{X: 2, Y: 0}) {
		t.Fatalf("path endpoints wrong: %+v", got)
	}
}

func TestAssignDropOnSeamLandsInOneChunk(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	got := m.AssignDrop(32, 64)
	if got != (impl.Chunk{X: 1, Y: 2}) {
		t.Fatalf("drop on the seam assigned to %+v, want {1 2}", got)
	}
}

func TestConcurrentLoadInitialisesChunkOnce(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	c := impl.Chunk{X: 9, Y: 9}
	if got := m.ConcurrentLoad(c, 8); got != 1 {
		t.Fatalf("chunk initialised %d times under concurrent entry, want 1", got)
	}
}

func TestTwoPhaseMigrateKeepsEntityInExactlyOneChunk(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	from := impl.Chunk{X: 0, Y: 1}
	to := impl.Chunk{X: 0, Y: 2}
	m.Load(from)
	m.Load(to)
	m.Spawn(from, 300)
	token, err := m.BeginMigrate(300, to)
	if err != nil {
		t.Fatalf("begin migrate failed: %v", err)
	}
	ok, err := m.CommitMigrate(token)
	if err != nil {
		t.Fatalf("commit migrate failed: %v", err)
	}
	if !ok {
		t.Fatalf("commit migrate reported failure")
	}
	total := m.EntityCount(from) + m.EntityCount(to)
	if total != 1 {
		t.Fatalf("entity exists in %d chunks after migration, want exactly 1", total)
	}
}

func TestRebuildIndexAfterRestart(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	got := m.Rebuild([]impl.Entity{
		{ID: 1, Chunk: impl.Chunk{X: 0, Y: 0}},
		{ID: 2, Chunk: impl.Chunk{X: 0, Y: 0}},
		{ID: 3, Chunk: impl.Chunk{X: 1, Y: 1}},
	})
	if got != 3 {
		t.Fatalf("rebuilt index size = %d, want 3", got)
	}
	if m.EntityCount(impl.Chunk{X: 0, Y: 0}) != 2 {
		t.Fatalf("chunk 0,0 indexed %d entities after rebuild, want 2", m.EntityCount(impl.Chunk{X: 0, Y: 0}))
	}
}

func TestEnteringWaitsForTerrainReady(t *testing.T) {
	m := impl.NewChunkMigrator(32)
	c := impl.Chunk{X: 7, Y: 7}
	m.Load(c)
	if !m.Entering(c) {
		t.Fatalf("player must be able to enter a loaded chunk")
	}
	if m.Entering(impl.Chunk{X: 8, Y: 8}) {
		t.Fatalf("player must not enter a chunk that is still loading")
	}
}
