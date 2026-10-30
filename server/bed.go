package server

import (
	"minecraft-server/world"
)

// bed.go places beds: two blocks (foot + head) sharing a facing — the
// state maths is world.BedFromItem / BedFacing / BedPartner.

// placeBed places a two-block bed (foot at pos, head one block toward the
// player's facing). Honours the OnBlockPlace veto (rolling both halves back on
// the client) and registers bed block entities so the bed renders for late
// joiners too.
func (c *ClientConnection) placeBed(pos world.Position, bed world.Block) {
	// Games may recolour the bed (BedWars: the placer's team). The rewrite
	// must still be a bed in its default state; anything else keeps the
	// held colour.
	if rw := c.instance.rewritePlacedBlock(c, pos, bed); rw.Name != bed.Name {
		if b, ok := world.BedFromItem(rw.Name); ok {
			bed = b
		}
	}
	facingIdx, dx, dz := world.BedFacing(c.player.Snapshot().Yaw)
	headPos := world.Position{X: pos.X + dx, Y: pos.Y, Z: pos.Z + dz}

	minState := bed.StateID - world.BedDefaultOffset
	foot := world.Block{StateID: minState + facingIdx*4 + world.BedFootOffset, Name: bed.Name}
	head := world.Block{StateID: minState + facingIdx*4 + world.BedHeadOffset, Name: bed.Name}

	if !world.Replaceable(c.instance.World.GetBlock(pos)) || !world.Replaceable(c.instance.World.GetBlock(headPos)) ||
		c.instance.blockedByPlayer(pos) || c.instance.blockedByPlayer(headPos) ||
		!c.instance.allowBlockPlace(c, pos, foot) {
		// Veto: roll both targeted positions back to what they were.
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
		_ = c.sendBlockUpdate(headPos, c.instance.World.GetBlock(headPos))
		return
	}

	c.instance.SetBlock(pos, foot)
	c.instance.SetBlock(headPos, head)
	c.consumeHeld()
	// Beds are rendered by a BlockEntityRenderer; register the block entities
	// so a player loading these chunks later still sees the bed.
	if w, ok := c.instance.World.(*world.MemoryWorld); ok {
		w.AddBlockEntity(pos, "minecraft:bed")
		w.AddBlockEntity(headPos, "minecraft:bed")
	}
}
