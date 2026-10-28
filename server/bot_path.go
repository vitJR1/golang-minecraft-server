package server

import (
	"container/heap"
	"math"

	"minecraft-server/world"
)

// bot_path.go finds a bot's way across the blocks: A* over "feet cells"
// (the block a standing player's feet are in) with walking, a one-block
// jump up, drops of up to three blocks, and bridging — stepping out over a
// gap by placing a block under the next cell, against the block under the
// current one, the way a player bridges with wool.

// routeStep is one cell of a path; Bridge means a block must be placed under
// it before stepping there.
type routeStep struct {
	Cell   world.Position
	Bridge bool
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
	cell    world.Position
	g, f    float64
	parent  *pathNode
	bridge  bool
	bridges int // bridge steps from the start up to here
	index   int
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
// the distance to aim. maxBridge caps how many bridge steps the path may
// use; budget caps the nodes expanded. It returns the steps after start, or
// nil when there's no way within the budget. It only reads blocks, so it
// may run off the tick goroutine.
func findPath(at blockAt, start world.Position, aim world.Position, goal func(world.Position) bool, maxBridge, budget int) []routeStep {
	h := func(p world.Position) float64 {
		dx, dy, dz := float64(p.X-aim.X), float64(p.Y-aim.Y), float64(p.Z-aim.Z)
		return math.Sqrt(dx*dx + dy*dy + dz*dz)
	}
	best := map[world.Position]float64{}
	open := &nodeHeap{}
	push := func(parent *pathNode, cell world.Position, cost float64, bridge bool) {
		g, bridges := cost, 0
		if parent != nil {
			g += parent.g
			bridges = parent.bridges
		}
		if bridge {
			bridges++
		}
		if bridges > maxBridge {
			return
		}
		if old, ok := best[cell]; ok && old <= g {
			return
		}
		best[cell] = g
		heap.Push(open, &pathNode{cell: cell, g: g, f: g + pathGreed*h(cell), parent: parent, bridge: bridge, bridges: bridges})
	}
	push(nil, start, 0, false)
	for expanded := 0; open.Len() > 0 && expanded < budget; expanded++ {
		n := heap.Pop(open).(*pathNode)
		if goal(n.cell) {
			var out []routeStep
			for ; n.parent != nil; n = n.parent {
				out = append(out, routeStep{Cell: n.cell, Bridge: n.bridge})
			}
			for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
			return out
		}
		c := n.cell
		for _, d := range cardinals {
			t := c.Add(d[0], 0, d[1])
			switch {
			case standable(at, t):
				push(n, t, pathCostWalk, false)
			case bridgeable(at, c, t, n.bridge):
				push(n, t, pathCostBridge, true)
			}
			// Jump up one block.
			if up := t.Add(0, 1, 0); headClear(at, c.Add(0, 2, 0)) && standable(at, up) {
				push(n, up, pathCostJump, false)
			}
			// Drop down.
			if cellClear(at, t) && headClear(at, t.Add(0, 1, 0)) && !groundUnder(at, t) {
				for k := 1; k <= pathMaxDrop; k++ {
					down := t.Add(0, -k, 0)
					if standable(at, down) {
						push(n, down, pathCostDrop+float64(k)*0.5, false)
						break
					}
					if !cellClear(at, down) {
						break
					}
				}
			}
		}
		for _, d := range diagonals {
			t := c.Add(d[0], 0, d[1])
			a, b := c.Add(d[0], 0, 0), c.Add(0, 0, d[1])
			if standable(at, t) && cellClear(at, a) && headClear(at, a.Add(0, 1, 0)) &&
				cellClear(at, b) && headClear(at, b.Add(0, 1, 0)) {
				push(n, t, pathCostDiag, false)
			}
		}
	}
	return nil
}

// feetCell is the cell a player's feet at y are in, nudged up so standing
// on a full block (y exactly an integer) or on a slab maps to one cell.
func feetCell(x, y, z float64) world.Position {
	return world.Position{X: floorF(x), Y: floorF(y + 0.01), Z: floorF(z)}
}
