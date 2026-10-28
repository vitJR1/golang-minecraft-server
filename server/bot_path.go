package server

import (
	"container/heap"
	"math"

	"minecraft-server/world"
)

// bot_path.go finds a bot's way across the blocks: A* over "feet cells"
// (the block a standing player's feet are in) with walking, a one-block
// jump up, drops of up to three blocks, and three moves that need more than
// legs:
//
//   - bridging: out over a gap, a block placed under the next cell against
//     the one under the current cell, the way a player bridges with wool;
//   - stairs: up a level where there's nothing to step on — a block placed
//     in front at foot level (and one under it, if that's void too), then a
//     jump onto it;
//   - leaps: a sprint-jump over a gap of up to opts.maxLeap cells.

// routeStep is one cell of a path. Place lists the blocks to put down,
// in order, before stepping there; Leap marks a sprint-jump over a gap.
type routeStep struct {
	Cell  world.Position
	Place []world.Position
	Leap  bool
}

// routeOpts tunes a search.
type routeOpts struct {
	maxBlocks int     // blocks the path may place (the bot carries them)
	maxLeap   int     // widest gap a leap may cross (0 = never leap)
	leapCost  float64 // extra cost per leap: how much the bot dislikes them
	budget    int     // nodes to expand
}

const (
	// pathNearNodes / pathFarNodes are search budgets: a short route
	// (chasing, the shop) is found on the tick; a route to another island,
	// bridging across the void, runs on its own goroutine (bot_brain.go).
	pathNearNodes  = 4000
	pathFarNodes   = 80000
	pathCostWalk   = 1.0
	pathCostDiag   = 1.4142
	pathCostJump   = 2.0
	pathCostDrop   = 1.5
	pathCostBridge = 4.0 // bridging is slow and spends blocks: walk if possible
	pathCostStair  = 5.0 // a stair step: a block (or two) and a jump
	pathCostLeap   = 2.0 // base cost of a sprint-jump, plus its length
	pathMaxDrop    = 3
	// pathGreed weighs the distance-to-go over the cost so far (weighted
	// A*): above the bridge cost, the search heads across the void for the
	// goal instead of flooding the island first. On the DOTA map that finds
	// an island-to-island route (≈70 bridge blocks) in ~10–30k nodes.
	pathGreed = 4.5
	// lowBlock is the tallest block that can share the feet cell (slabs,
	// beds, carpets — the auto-step climbs onto them).
	lowBlock = 0.6
)

// cellClear reports whether a body may occupy the cell: no collision, or a
// low one (a slab, a bed) that the feet stand on.
func cellClear(at blockAt, p world.Position) bool {
	// A wall or fence below reaches 1.5 blocks up, into this cell — and out
	// of a jump's reach (1.25): nobody stands there, nobody walks through.
	if _, hi, ok := world.CollisionHeight(at(p.Add(0, -1, 0))); ok && hi > 1 {
		return false
	}
	lo, hi, ok := world.CollisionHeight(at(p))
	return !ok || (lo == 0 && hi <= lowBlock)
}

// headClear: the head cell must be entirely free.
func headClear(at blockAt, p world.Position) bool {
	_, _, ok := world.CollisionHeight(at(p))
	return !ok
}

// groundUnder reports whether something holds up a body whose feet are in p.
func groundUnder(at blockAt, p world.Position) bool {
	if lo, hi, ok := world.CollisionHeight(at(p)); ok && lo == 0 && hi > 0 {
		return true // standing on a slab / bed in the feet cell
	}
	_, hi, ok := world.CollisionHeight(at(p.Add(0, -1, 0)))
	return ok && hi >= 0.5
}

// standable reports whether a player can stand with its feet in p.
func standable(at blockAt, p world.Position) bool {
	return cellClear(at, p) && headClear(at, p.Add(0, 1, 0)) && groundUnder(at, p)
}

// bridgeable reports whether a block can be placed under t to stand there,
// coming from n: t and its head cell are free, there's nothing to stand on,
// the cell under t takes a block, and the block under n holds it from the
// side — a real block, or (onBridge) the one this path placed under n.
func bridgeable(at blockAt, n, t world.Position, onBridge bool) bool {
	if !cellClear(at, t) || !headClear(at, t.Add(0, 1, 0)) || groundUnder(at, t) {
		return false
	}
	if !world.Replaceable(at(t.Add(0, -1, 0))) {
		return false
	}
	return onBridge || world.Collidable(at(n.Add(0, -1, 0)))
}

var (
	cardinals = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	diagonals = [4][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
)

// pathNode is an A* entry.
type pathNode struct {
	cell   world.Position
	g, f   float64
	parent *pathNode
	place  []world.Position
	leap   bool
	blocks int  // blocks placed from the start up to here
	placed bool // this cell stands on a block the path placed
	index  int
}

type nodeHeap []*pathNode

func (h nodeHeap) Len() int            { return len(h) }
func (h nodeHeap) Less(i, j int) bool  { return h[i].f < h[j].f }
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *nodeHeap) Push(x interface{}) { n := x.(*pathNode); n.index = len(*h); *h = append(*h, n) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := old[len(old)-1]
	*h = old[:len(old)-1]
	return n
}

// findPath searches from start to any cell where goal is true, steering by
// the distance to aim, within opts. It returns the steps after start, or nil
// when there's no way within the budget. It only reads blocks, so it may run
// off the tick goroutine.
func findPath(at blockAt, start world.Position, aim world.Position, goal func(world.Position) bool, opts routeOpts) []routeStep {
	h := func(p world.Position) float64 {
		dx, dy, dz := float64(p.X-aim.X), float64(p.Y-aim.Y), float64(p.Z-aim.Z)
		return math.Sqrt(dx*dx + dy*dy + dz*dz)
	}
	best := map[world.Position]float64{}
	open := &nodeHeap{}
	push := func(parent *pathNode, cell world.Position, cost float64, place []world.Position, leap bool) {
		g, blocks := cost, len(place)
		if parent != nil {
			g += parent.g
			blocks += parent.blocks
		}
		if blocks > opts.maxBlocks {
			return
		}
		if old, ok := best[cell]; ok && old <= g {
			return
		}
		best[cell] = g
		heap.Push(open, &pathNode{cell: cell, g: g, f: g + pathGreed*h(cell), parent: parent,
			place: place, leap: leap, blocks: blocks, placed: len(place) > 0})
	}
	push(nil, start, 0, nil, false)
	for expanded := 0; open.Len() > 0 && expanded < opts.budget; expanded++ {
		n := heap.Pop(open).(*pathNode)
		if goal(n.cell) {
			var out []routeStep
			for ; n.parent != nil; n = n.parent {
				out = append(out, routeStep{Cell: n.cell, Place: n.place, Leap: n.leap})
			}
			for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
			return out
		}
		c := n.cell
		// The block under c holds a new block from the side: a real block,
		// or one this path put there.
		supported := n.placed || world.Collidable(at(c.Add(0, -1, 0)))
		for _, d := range cardinals {
			t := c.Add(d[0], 0, d[1])
			switch {
			case standable(at, t):
				push(n, t, pathCostWalk, nil, false)
			case supported && bridgeable(at, c, t, true):
				push(n, t, pathCostBridge, []world.Position{t.Add(0, -1, 0)}, false)
			}
			up := t.Add(0, 1, 0)
			if headClear(at, c.Add(0, 2, 0)) {
				if standable(at, up) {
					// Jump up one block.
					push(n, up, pathCostJump, nil, false)
				} else if step, ok := stairStep(at, c, t, supported); ok {
					push(n, up, pathCostStair+float64(len(step)-1)*pathCostBridge, step, false)
				}
			}
			// Drop down.
			if cellClear(at, t) && headClear(at, t.Add(0, 1, 0)) && !groundUnder(at, t) {
				for k := 1; k <= pathMaxDrop; k++ {
					down := t.Add(0, -k, 0)
					if standable(at, down) {
						push(n, down, pathCostDrop+float64(k)*0.5, nil, false)
						break
					}
					if !cellClear(at, down) {
						break
					}
				}
			}
			// Leap over a gap.
			for k := 2; k <= opts.maxLeap+1; k++ {
				land := c.Add(d[0]*k, 0, d[1]*k)
				if !leapClear(at, c, d, k) {
					break
				}
				if standable(at, land) && standable(at, c.Add(-d[0], 0, -d[1])) { // a run-up behind
					push(n, land, pathCostLeap+float64(k)+opts.leapCost, nil, true)
					break
				}
			}
		}
		for _, d := range diagonals {
			t := c.Add(d[0], 0, d[1])
			a, b := c.Add(d[0], 0, 0), c.Add(0, 0, d[1])
			if standable(at, t) && cellClear(at, a) && headClear(at, a.Add(0, 1, 0)) &&
				cellClear(at, b) && headClear(at, b.Add(0, 1, 0)) {
				push(n, t, pathCostDiag, nil, false)
			}
		}
	}
	return nil
}

// stairStep checks a stair step from c toward t (one cell over, same level):
// the bot stands on a block placed at t, its feet a level up. Returns the
// blocks to place: t itself, preceded by the block under t when that is
// void too (and c's support can hold it from the side).
func stairStep(at blockAt, c, t world.Position, supported bool) ([]world.Position, bool) {
	up := t.Add(0, 1, 0)
	if !cellClear(at, up) || !headClear(at, up.Add(0, 1, 0)) {
		return nil, false
	}
	if !world.Replaceable(at(t)) || !headClear(at, t) {
		return nil, false
	}
	below := t.Add(0, -1, 0)
	if world.Collidable(at(below)) {
		return []world.Position{t}, true // t sits on solid ground: one block
	}
	if !supported || !world.Replaceable(at(below)) {
		return nil, false
	}
	return []world.Position{below, t}, true
}

// leapClear reports whether the k-1 cells between c and c+k·d are an open
// gap a sprint-jump flies through: no ground to walk on (else it isn't a
// gap), and room for the body and the jump's arc.
func leapClear(at blockAt, c world.Position, d [2]int, k int) bool {
	for i := 1; i < k; i++ {
		p := c.Add(d[0]*i, 0, d[1]*i)
		if !cellClear(at, p) || !headClear(at, p.Add(0, 1, 0)) || !headClear(at, p.Add(0, 2, 0)) || groundUnder(at, p) {
			return false
		}
	}
	return true
}

// findRoute prefers walking over building: a walk-only search first — it
// finds bridges already there, this bot's from an earlier trip or anyone
// else's — and a search that may place blocks only when walking (and
// leaping) can't get there. Without it the greedy search would happily lay
// a fresh bridge alongside an existing one.
func findRoute(at blockAt, start, aim world.Position, goal func(world.Position) bool, opts routeOpts) []routeStep {
	walk := opts
	walk.maxBlocks = 0
	if r := findPath(at, start, aim, goal, walk); r != nil || opts.maxBlocks == 0 {
		return r
	}
	return findPath(at, start, aim, goal, opts)
}

// resumeAt finds where a route can be picked up again from cell here: the
// index of the step after the one the bot stands on (or next to, at the same
// level), or -1. A bot knocked off its bridge mid-way continues that bridge
// instead of starting a new one from wherever it landed.
func resumeAt(route []routeStep, here world.Position) int {
	best, bestD := -1, 2
	for i, s := range route {
		dx, dz := s.Cell.X-here.X, s.Cell.Z-here.Z
		if s.Cell.Y != here.Y || dx < -1 || dx > 1 || dz < -1 || dz > 1 {
			continue
		}
		if d := dx*dx + dz*dz; d < bestD || (d == bestD && i > best) {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return -1
	}
	if bestD == 0 {
		return best + 1 // standing on it: go on to the next
	}
	return best // next to it: walk onto it first
}

// feetCell is the cell a player's feet at y are in, nudged up so standing
// on a full block (y exactly an integer) or on a slab maps to one cell.
func feetCell(x, y, z float64) world.Position {
	return world.Position{X: floorF(x), Y: floorF(y + 0.01), Z: floorF(z)}
}
