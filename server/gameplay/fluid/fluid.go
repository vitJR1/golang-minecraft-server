// Package fluid is vanilla-style fluid flow, driven by the server from the
// instance tick loop, and the bucket rules (bucket.go).
//
// Fluids are ordinary blocks whose state encodes the "level" property:
// 0 = source, 1..7 = flowing (1 right next to the source, thinner each
// step), 8..15 = falling (a full column below another fluid block). Water
// updates every 5 ticks and thins by 1 per block (reach 7); lava every 30
// ticks, thinning by 2 (reach 3, like the overworld).
//
// Nothing flows on its own at map load: a fluid block is re-evaluated only
// when it or one of its six neighbours changes (Instance.SetBlock /
// SetBlocks schedule it on a Queue). Each evaluation does what vanilla's
// FlowingFluid.tick does — derive the block's correct level from its
// neighbours (draining when the source is gone, turning into a new source
// between two sources), then spread: down into air as falling fluid, and
// sideways (sources always; flowing fluid only when it can't fall) into air
// or thinner fluid of the same kind. Water and lava don't interact.
package fluid

import (
	"sync"

	"minecraft-server/world"
)

// Kind holds one fluid's tuning.
type Kind struct {
	name     string
	minState int32 // state id of level 0
	interval uint64
	dropOff  int
}

var (
	Water = &Kind{name: world.Water.Name, minState: world.Water.StateID, interval: 5, dropOff: 1}
	Lava  = &Kind{name: world.Lava.Name, minState: world.Lava.StateID, interval: 30, dropOff: 2}
	Kinds = []*Kind{Water, Lava}
)

const (
	MaxLevel = 7 // thinnest flowing level
	Falling  = 8 // level used for a falling column
)

// Of identifies a fluid block: its kind and level.
func Of(b world.Block) (*Kind, int, bool) {
	for _, k := range Kinds {
		if b.Name == k.name {
			lvl := int(b.StateID - k.minState)
			if lvl < 0 || lvl > 15 {
				lvl = 0
			}
			return k, lvl, true
		}
	}
	return nil, 0, false
}

func (k *Kind) Block(level int) world.Block {
	return world.Block{StateID: k.minState + int32(level), Name: k.name}
}

// spreadLevel is the level a block contributes to its side neighbours:
// sources and falling columns count as full.
func spreadLevel(level int) int {
	if level >= Falling {
		return 0
	}
	return level
}

func Neighbours6(p world.Position) [6]world.Position {
	return [6]world.Position{
		{X: p.X, Y: p.Y - 1, Z: p.Z}, {X: p.X, Y: p.Y + 1, Z: p.Z},
		{X: p.X - 1, Y: p.Y, Z: p.Z}, {X: p.X + 1, Y: p.Y, Z: p.Z},
		{X: p.X, Y: p.Y, Z: p.Z - 1}, {X: p.X, Y: p.Y, Z: p.Z + 1},
	}
}

func sides4(p world.Position) [4]world.Position {
	return [4]world.Position{
		{X: p.X - 1, Y: p.Y, Z: p.Z}, {X: p.X + 1, Y: p.Y, Z: p.Z},
		{X: p.X, Y: p.Y, Z: p.Z - 1}, {X: p.X, Y: p.Y, Z: p.Z + 1},
	}
}

// Queue holds block positions whose fluid state must be re-evaluated
// on their kind's next due tick.
type Queue struct {
	mu      sync.Mutex
	pending map[world.Position]struct{}
}

// Schedule marks pos and its six neighbours for re-evaluation.
func (q *Queue) Schedule(pos world.Position) {
	q.mu.Lock()
	if q.pending == nil {
		q.pending = map[world.Position]struct{}{}
	}
	q.pending[pos] = struct{}{}
	for _, n := range Neighbours6(pos) {
		q.pending[n] = struct{}{}
	}
	q.mu.Unlock()
}

// Due takes the pending fluid blocks whose kind is due this tick. Pending
// non-fluid positions are dropped (they were scheduled as neighbours of a
// change and turned out not to hold fluid).
func (q *Queue) Due(tick uint64, at func(world.Position) world.Block) []world.Position {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	due := make([]world.Position, 0, len(q.pending))
	for pos := range q.pending {
		k, _, ok := Of(at(pos))
		switch {
		case !ok:
			delete(q.pending, pos)
		case tick%k.interval == 0:
			due = append(due, pos)
			delete(q.pending, pos)
		}
	}
	return due
}

// World is where fluids flow: block reads, and writes that schedule
// the neighbours (Instance.SetBlock does).
type World interface {
	GetBlock(pos world.Position) world.Block
	SetBlock(pos world.Position, b world.Block)
}

// Update is one vanilla fluid tick for the block at pos.
func Update(w World, pos world.Position) {
	cur := w.GetBlock(pos)
	k, level, ok := Of(cur)
	if !ok {
		return
	}
	below := world.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	belowBlk := w.GetBlock(below)
	_, belowLevel, belowIsFluid := Of(belowBlk)
	belowSame := belowIsFluid && belowBlk.Name == k.name

	// 1. Derive the level this block should have from its neighbours.
	if level != 0 {
		want := -1
		aboveBlk := w.GetBlock(world.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
		if aboveBlk.Name == k.name {
			want = Falling
		} else {
			sources := 0
			for _, n := range sides4(pos) {
				nb := w.GetBlock(n)
				if nb.Name != k.name {
					continue
				}
				_, nl, _ := Of(nb)
				if nl == 0 {
					sources++
				}
				if l := spreadLevel(nl) + k.dropOff; l <= MaxLevel && (want < 0 || l < want) {
					want = l
				}
			}
			// Infinite water: two adjacent sources over solid ground (or a
			// source) make a new source. Lava never does.
			if k == Water && sources >= 2 && (belowSame && belowLevel == 0 || (!belowIsFluid && belowBlk != world.Air)) {
				want = 0
			}
		}
		if want < 0 {
			w.SetBlock(pos, world.Air) // nothing feeds it any more
			return
		}
		if want != level {
			w.SetBlock(pos, k.Block(want))
			level = want
		}
	}

	// 2. Spread. Down first.
	if belowBlk == world.Air || (belowSame && belowLevel != 0 && belowLevel != Falling) {
		w.SetBlock(below, k.Block(Falling))
	}
	// Sideways: sources always; flowing fluid only when it can't fall.
	hole := belowBlk == world.Air || belowSame
	if level != 0 && hole {
		return
	}
	next := spreadLevel(level) + k.dropOff
	if next > MaxLevel {
		return
	}
	for _, n := range sides4(pos) {
		nb := w.GetBlock(n)
		if nb == world.Air {
			w.SetBlock(n, k.Block(next))
			continue
		}
		if nb.Name == k.name {
			if _, nl, _ := Of(nb); nl != 0 && nl < Falling && nl > next {
				w.SetBlock(n, k.Block(next))
			}
		}
	}
}
