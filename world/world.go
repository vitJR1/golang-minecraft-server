package world

import "sync"

// Position addresses a single block in absolute world coordinates. Y goes
// from -64 (bedrock) to 319 in 1.20.1 overworld.
type Position struct {
	X, Y, Z int
}

// World is the storage abstraction. Implementations decide whether blocks
// live in memory, on disk, or are generated on demand.
type World interface {
	GetBlock(p Position) Block
	SetBlock(p Position, b Block)
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
