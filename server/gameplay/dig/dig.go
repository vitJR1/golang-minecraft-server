// Package dig is survival digging's vanilla rules, independent of
// connections: when a "finished digging" comes late enough to count, what a
// broken block drops for the tool that broke it, how much the tool wears,
// and whether a block would overlap a player (placement is refused there).
// The dig time itself is world.BreakTicksWith; the server tracks who digs
// what since when (server/dig.go).
package dig

import (
	"math"

	"minecraft-server/anticheat"
	"minecraft-server/world"
)

// AcceptFraction is how much of the dig time must have passed for a
// "finished digging" to count: vanilla accepts a finish once its own
// progress reaches 70%, which forgives latency and the client's
// one-tick-earlier estimate.
const AcceptFraction = 0.7

// Accepted reports whether a dig that needs needed ticks (< 0: unbreakable)
// is done after elapsed ticks.
func Accepted(needed int, elapsed uint64) bool {
	return needed >= 0 && float64(elapsed+1) >= math.Ceil(float64(needed)*AcceptFraction)
}

// Drop is what breaking blk with held drops: the block's item when the tool
// can harvest it (world.CanHarvest), else nothing ("").
func Drop(blk world.Block, held string) (item string, n int) {
	if !world.CanHarvest(world.BreakInfoFor(blk.Name), held) {
		return "", 0
	}
	return world.BlockDrop(blk.Name)
}

// ToolWear is the durability held loses breaking blk: one point per
// non-instant block, two for a sword (like vanilla).
func ToolWear(blk world.Block, held string) int {
	if world.BreakInfoFor(blk.Name).Hardness <= 0 {
		return 0
	}
	if world.ToolFromItem(held).Type == world.Sword {
		return 2
	}
	return 1
}

// Overlaps reports whether the block at pos overlaps the hitbox of a player
// with feet at (x, y, z) (vanilla 0.6 × 1.8).
func Overlaps(pos world.Position, x, y, z float64) bool {
	const half = anticheat.PlayerWidth / 2
	return x+half > float64(pos.X) && x-half < float64(pos.X+1) &&
		y+anticheat.PlayerHeight > float64(pos.Y) && y < float64(pos.Y+1) &&
		z+half > float64(pos.Z) && z-half < float64(pos.Z+1)
}
