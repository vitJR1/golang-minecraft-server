package world

import "sync"

// Position addresses a single block in absolute world coordinates. Y goes
// from -64 (bedrock) to 319 in 1.20.1 overworld.
type Position struct {
	X, Y, Z int
}

// Add returns p offset by (dx, dy, dz).
func (p Position) Add(dx, dy, dz int) Position {
	return Position{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz}
}

// BlockChange is one (position, block) pair for bulk updates such as
// Instance.SetBlocks: a region fill or a schematic paste is expressed as a
// slice of these, applied and broadcast together.
type BlockChange struct {
	Pos   Position
	Block Block
}

// World is the storage abstraction. Implementations decide whether blocks
// live in memory, on disk, or are generated on demand.
//
// Range iterates over every non-Air block. The callback runs without any
// world-level lock held, so callbacks may safely call back into the same
// World. Order is unspecified.
type World interface {
	GetBlock(p Position) Block
	SetBlock(p Position, b Block)
	Range(fn func(p Position, b Block))
}

// MemoryWorld is a sparse hash-map world: only non-air positions are
// stored. Missing positions return Air. Concurrent-safe via RWMutex.
//
// Suitable for tests and small playgrounds. Not a replacement for chunked
// region storage when real worlds appear.
type MemoryWorld struct {
	mu            sync.RWMutex
	blocks        map[Position]Block
	entities      []Entity
	blockEntities map[Position]string // pos → block-entity type name
	biome         string              // namespaced biome for the whole world

	// Change tracking for caches of the world's chunk data (ColumnTracker):
	// version counts every change; colVersion is the version of each chunk
	// column's last change, biomeVersion that of the last SetBiome (the
	// biome covers every column).
	version      uint64
	colVersion   map[Column]uint64
	biomeVersion uint64
}

// Column is a chunk column's coordinates: block X and Z divided by 16,
// rounding down.
type Column struct{ X, Z int32 }

// ColumnOf is the chunk column holding p.
func ColumnOf(p Position) Column {
	return Column{int32(p.X >> 4), int32(p.Z >> 4)}
}

// ColumnTracker is a World that reports which chunk columns changed, so a
// cache of their chunk data can rebuild only those. MemoryWorld implements
// it.
type ColumnTracker interface {
	World
	// Version is the world's change counter; it only grows.
	Version() uint64
	// ChangedSince lists the columns changed after version v; all is true
	// when every column changed (the biome).
	ChangedSince(v uint64) (cols []Column, all bool)
	// RangeColumns visits every non-Air block in the given columns (every
	// column when cols is nil) under the world's read lock: fn must not
	// call back into the world.
	RangeColumns(cols map[Column]bool, fn func(p Position, b Block))
}

// touch records a change to column c; w.mu held for writing.
func (w *MemoryWorld) touch(c Column) {
	w.version++
	if w.colVersion == nil {
		w.colVersion = make(map[Column]uint64)
	}
	w.colVersion[c] = w.version
}

// Version implements ColumnTracker.
func (w *MemoryWorld) Version() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.version
}

// ChangedSince implements ColumnTracker.
func (w *MemoryWorld) ChangedSince(v uint64) (cols []Column, all bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.biomeVersion > v {
		return nil, true
	}
	for c, cv := range w.colVersion {
		if cv > v {
			cols = append(cols, c)
		}
	}
	return cols, false
}

// RangeColumns implements ColumnTracker.
func (w *MemoryWorld) RangeColumns(cols map[Column]bool, fn func(p Position, b Block)) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for p, b := range w.blocks {
		if cols == nil || cols[ColumnOf(p)] {
			fn(p, b)
		}
	}
}

func NewMemoryWorld() *MemoryWorld {
	return &MemoryWorld{blocks: make(map[Position]Block)}
}

// AddEntity records a non-block entity (e.g. an item frame) in the world.
func (w *MemoryWorld) AddEntity(e Entity) {
	w.mu.Lock()
	w.entities = append(w.entities, e)
	w.mu.Unlock()
}

// Entities returns a copy of the world's entities, satisfying EntityProvider.
func (w *MemoryWorld) Entities() []Entity {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Entity, len(w.entities))
	copy(out, w.entities)
	return out
}

// AddBlockEntity records that the block at p is a block entity of the given
// type ("minecraft:bed"). Used so the chunk streamer can list it for the
// client's BlockEntityRenderer.
func (w *MemoryWorld) AddBlockEntity(p Position, typeName string) {
	w.mu.Lock()
	if w.blockEntities == nil {
		w.blockEntities = make(map[Position]string)
	}
	w.blockEntities[p] = typeName
	w.touch(ColumnOf(p)) // the column's chunk data lists its block entities
	w.mu.Unlock()
}

// BlockEntities returns a copy of the world's block-entity map, satisfying
// BlockEntityProvider.
func (w *MemoryWorld) BlockEntities() map[Position]string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make(map[Position]string, len(w.blockEntities))
	for p, t := range w.blockEntities {
		out[p] = t
	}
	return out
}

// SetBiome sets the world's (uniform) biome, e.g. "minecraft:plains".
func (w *MemoryWorld) SetBiome(name string) {
	w.mu.Lock()
	w.biome = name
	w.version++
	w.biomeVersion = w.version
	w.mu.Unlock()
}

// Biome returns the world's biome name (or "" if unset), satisfying
// BiomeProvider.
func (w *MemoryWorld) Biome() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.biome
}

func (w *MemoryWorld) GetBlock(p Position) Block {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if b, ok := w.blocks[p]; ok {
		return b
	}
	return Air
}

// SetBlock stores b at p. Setting Air deletes the entry so the map stays
// sparse — convenient for tests, and means GetBlock for any unset position
// is always cheap.
func (w *MemoryWorld) SetBlock(p Position, b Block) {
	w.mu.Lock()
	defer w.mu.Unlock()
	old, had := w.blocks[p]
	if b == Air {
		if !had {
			return
		}
		delete(w.blocks, p)
	} else {
		if had && old == b {
			return
		}
		w.blocks[p] = b
	}
	w.touch(ColumnOf(p))
}

// Range visits every non-Air block. Snapshots the map under the read lock,
// then invokes the callback without it — safe to call w.SetBlock from inside.
func (w *MemoryWorld) Range(fn func(p Position, b Block)) {
	w.mu.RLock()
	snapshot := make(map[Position]Block, len(w.blocks))
	for k, v := range w.blocks {
		snapshot[k] = v
	}
	w.mu.RUnlock()
	for p, b := range snapshot {
		fn(p, b)
	}
}

// Neighbor returns the block next to p on face (the Use Item On Block face
// enum: 0 -Y, 1 +Y, 2 -Z north, 3 +Z south, 4 -X west, 5 +X east); p itself
// for any other value. A block placed against a face goes there.
func (p Position) Neighbor(face int) Position {
	switch face {
	case 0:
		return p.Add(0, -1, 0)
	case 1:
		return p.Add(0, 1, 0)
	case 2:
		return p.Add(0, 0, -1)
	case 3:
		return p.Add(0, 0, 1)
	case 4:
		return p.Add(-1, 0, 0)
	case 5:
		return p.Add(1, 0, 0)
	}
	return p
}
