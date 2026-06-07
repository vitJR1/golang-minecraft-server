package server

import (
	"math"
	"strings"

	"minecraft-server/world"
)

// bed.go places beds correctly: a bed is two blocks (foot + head) sharing a
// facing, not two identical foot blocks. The foot lands where the player
// clicked; the head extends one block in the direction the player is facing.
//
// Every bed colour has the same state layout (from blocks.json), so we don't
// need a per-colour variant table — just the block's default state id:
//
//	properties: facing[north,south,west,east], occupied[true,false], part[head,foot]
//	strides:    facing=4, occupied=2, part=1
//	defaultState = min + facing(north=0)*4 + occupied(false=1)*2 + part(foot=1) = min + 3
//
// so min = default - 3, and for a given facing index:
//
//	foot (occupied=false, part=foot) = min + facing*4 + 3
//	head (occupied=false, part=head) = min + facing*4 + 2
const (
	bedDefaultOffset = 3 // defaultState - minStateID for any bed
	bedFootOffset    = 3 // within a facing group: occupied=false, part=foot
	bedHeadOffset    = 2 // within a facing group: occupied=false, part=head
)

// bedFromItem returns the bed block for a held item id ("minecraft:red_bed"),
// and whether the item is a bed at all.
func bedFromItem(item string) (world.Block, bool) {
	if !strings.HasSuffix(item, "_bed") {
		return world.Block{}, false
	}
	return world.BlockByName(item)
}

// bedFacing maps a player yaw to the bed's facing index (matching the
// facing[north,south,west,east] order) and the (dx,dz) step from foot to head
// — the head extends in the direction the player is looking.
func bedFacing(yaw float32) (facingIdx int32, dx, dz int) {
	bucket := (int(math.Floor(float64(yaw)/90+0.5))%4 + 4) % 4
	switch bucket {
	case 0: // looking south (+Z)
		return 1, 0, 1
	case 1: // looking west (-X)
		return 2, -1, 0
	case 2: // looking north (-Z)
		return 0, 0, -1
	default: // looking east (+X)
		return 3, 1, 0
	}
}

// placeBed places a two-block bed (foot at pos, head one block toward the
// player's facing). Honours the OnBlockPlace veto (rolling both halves back on
// the client) and registers bed block entities so the bed renders for late
// joiners too.
func (c *ClientConnection) placeBed(pos world.Position, bed world.Block) {
	facingIdx, dx, dz := bedFacing(c.player.Snapshot().Yaw)
	headPos := world.Position{X: pos.X + dx, Y: pos.Y, Z: pos.Z + dz}

	minState := bed.StateID - bedDefaultOffset
	foot := world.Block{StateID: minState + facingIdx*4 + bedFootOffset, Name: bed.Name}
	head := world.Block{StateID: minState + facingIdx*4 + bedHeadOffset, Name: bed.Name}

	if hook := c.instance.OnBlockPlace; hook != nil && !hook(c, pos, foot) {
		// Veto: roll both targeted positions back to what they were.
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
		_ = c.sendBlockUpdate(headPos, c.instance.World.GetBlock(headPos))
		return
	}

	c.instance.SetBlock(pos, foot)
	c.instance.SetBlock(headPos, head)
	// Beds are rendered by a BlockEntityRenderer; register the block entities
	// so a player loading these chunks later still sees the bed.
	if w, ok := c.instance.World.(*world.MemoryWorld); ok {
		w.AddBlockEntity(pos, "minecraft:bed")
		w.AddBlockEntity(headPos, "minecraft:bed")
	}
}
