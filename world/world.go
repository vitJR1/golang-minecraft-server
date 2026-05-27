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
	mu     sync.RWMutex
	blocks map[Position]Block
}

func NewMemoryWorld() *MemoryWorld {
	return &MemoryWorld{blocks: make(map[Position]Block)}
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
