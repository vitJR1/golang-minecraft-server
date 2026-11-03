package server

import (
	"minecraft-server/player"
	"minecraft-server/server/gameplay/dig"
	"minecraft-server/server/gameplay/fluid"
	"minecraft-server/world"
)

// dig.go gives survival digging its vanilla timing and tool rules:
//
//   - Player Action 0 (start digging) only breaks the block at once for
//     creative players or for instant blocks (hardness 0). Otherwise the
//     server remembers where and when the dig began.
//   - Player Action 2 (finished digging) breaks the block only if enough
//     ticks have passed for the held tool (world.BreakTicks, dig.Accepted:
//     70% of it). An early finish is rejected and the block is replayed to
//     the client.
//   - A broken block drops its item (as an item entity) only when harvested
//     with a sufficient tool (world.CanHarvest); creative breaks drop nothing.
//   - The dig time honours Efficiency / Haste / Mining Fatigue / Aqua
//     Affinity, being underwater and being airborne (digContext,
//     modifiers.go), and the tool loses durability per block.
//   - A bed half takes the other half with it and drops one bed.
//
// Placement is also guarded: a block can't go where a player stands.

// startDig handles "started digging" at pos for this player.
func (c *ClientConnection) startDig(pos world.Position) {
	c.digPos = nil
	if _, _, isFluid := fluid.Of(c.instance.World.GetBlock(pos)); isFluid {
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos)) // can't punch water away
		return
	}
	if c.gamemode() == player.Creative {
		c.breakBlock(pos, false)
		return
	}
	blk := c.instance.World.GetBlock(pos)
	if blk == world.Air {
		return
	}
	ticks := world.BreakTicksWith(world.BreakInfoFor(blk.Name), c.heldItemName(), c.digContext())
	switch {
	case ticks < 0: // unbreakable
		_ = c.sendBlockUpdate(pos, blk)
	case ticks == 0: // instant: the client sends no "finished"
		c.breakBlock(pos, true)
	default:
		p := pos
		c.digPos = &p
		c.digStartTick = c.instance.Tick()
	}
}

// cancelDig forgets an in-progress dig.
func (c *ClientConnection) cancelDig() { c.digPos = nil }

// finishDig handles "finished digging": break if the dig was long enough.
func (c *ClientConnection) finishDig(pos world.Position) {
	if _, _, isFluid := fluid.Of(c.instance.World.GetBlock(pos)); isFluid {
		c.digPos = nil
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
		return
	}
	if c.gamemode() == player.Creative {
		c.breakBlock(pos, false)
		return
	}
	blk := c.instance.World.GetBlock(pos)
	if c.digPos == nil || *c.digPos != pos || blk == world.Air {
		c.digPos = nil
		_ = c.sendBlockUpdate(pos, blk)
		return
	}
	c.digPos = nil
	needed := world.BreakTicksWith(world.BreakInfoFor(blk.Name), c.heldItemName(), c.digContext())
	if !dig.Accepted(needed, c.instance.Tick()-c.digStartTick) {
		_ = c.sendBlockUpdate(pos, blk) // too early (or unbreakable): put it back
		return
	}
	c.breakBlock(pos, true)
}

// breakBlock runs the block-break veto chain (listeners, then the instance
// hook) and either air-fills the block — dropping its item when harvest is
// true and the held tool qualifies — or rolls the client back. A bed half
// removes its partner too. Nothing drops if the hook already cleared the
// block (BedWars does that for beds).
func (c *ClientConnection) breakBlock(pos world.Position, harvest bool) {
	if !c.instance.allowBlockBreak(c, pos) {
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
		return
	}
	blk := c.instance.World.GetBlock(pos)
	if blk == world.Air {
		return
	}
	c.instance.SetBlock(pos, world.Air)
	if partner, ok := world.BedPartner(pos, blk); ok {
		c.instance.SetBlock(partner, world.Air)
	}
	if !harvest {
		return
	}
	held := c.heldItemName()
	if item, n := dig.Drop(blk, held); item != "" {
		c.instance.DropItem(float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, item, n)
	}
	// Tools wear one point per non-instant block (swords two, like vanilla).
	if wear := dig.ToolWear(blk, held); wear > 0 {
		c.damageHeldTool(wear)
	}
}

// gamemode is the player's current gamemode (Survival when unknown).
func (c *ClientConnection) gamemode() player.Gamemode {
	if c.player == nil {
		return player.Survival
	}
	return c.player.Snapshot().Gamemode
}

// blockedByPlayer reports whether placing a block at pos would overlap any
// non-spectator player's hitbox in the instance (including the placer).
func (i *Instance) blockedByPlayer(pos world.Position) bool {
	for _, p := range i.Players.snapshot() {
		if p.player == nil {
			continue
		}
		s := p.player.Snapshot()
		if s.Gamemode == player.Spectator {
			continue
		}
		if dig.Overlaps(pos, s.X, s.Y, s.Z) {
			return true
		}
	}
	return false
}
