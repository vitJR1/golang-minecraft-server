package world

import "sync"

// Position addresses a single block in absolute world coordinates. Y goes
// from -64 (bedrock) to 319 in 1.20.1 overworld.
type Position struct {
	X, Y, Z int
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
	if b == Air {
		delete(w.blocks, p)
		return
	}
	w.blocks[p] = b
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
