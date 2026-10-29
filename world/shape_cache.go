package world

import "sync/atomic"

// shape_cache.go: Collidable / FullCube / CollisionHeight work from the
// block's name and state properties — string matching and parsing, cheap
// once but not at the rate the bots' path searches ask (tens of thousands
// of cells a search). The answer depends only on the block state, so it's
// worked out once per state and kept in a lock-free table indexed by state
// id; the entry keeps the name too, so a Block with an unexpected
// name/state pair never gets another block's answer.

// shape is everything the collision helpers say about one block state.
type shape struct {
	name       string
	collidable bool
	full       bool
	hasBox     bool
	lo, hi     float64
}

// shapeCacheSize covers every vanilla 1.20.1 block state id.
const shapeCacheSize = 1 << 15

var shapeCache [shapeCacheSize]atomic.Pointer[shape]

// shapeOf returns b's shape, from the table when it's there.
func shapeOf(b Block) *shape {
	id := b.StateID
	if id >= 0 && id < shapeCacheSize {
		if sh := shapeCache[id].Load(); sh != nil && sh.name == b.Name {
			return sh
		}
	}
	sh := &shape{name: b.Name, collidable: collidable(b), full: fullCube(b)}
	sh.lo, sh.hi, sh.hasBox = collisionHeight(b)
	if id >= 0 && id < shapeCacheSize {
		shapeCache[id].Store(sh)
	}
	return sh
}
