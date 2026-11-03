package server

import (
	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/server/gameplay/fluid"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// bucket.go: water and lava buckets. The vanilla client never sends "use
// item on block" for a bucket — BucketItem only has use(), so the packet is
// a plain Use Item and the server has to find the target itself by casting
// a ray from the player's eyes along the look direction:
//
//   - A filled bucket ignores fluids on the way, stops at the first solid
//     block and empties into the block in front of it (the one the ray was
//     in just before the hit) — a fluid source of the bucket's kind. The
//     bucket becomes empty in survival; creative keeps it.
//   - An empty bucket stops at the first fluid SOURCE block and scoops it
//     (the block turns to air, the bucket becomes water_bucket/lava_bucket).
//
// Both go through the OnBlockPlace / OnBlockBreak veto chains, so the hub
// and lobbies stay dry and BedWars' map protection still applies.

// useBucket handles a Use Item with a bucket in hand. Returns false when the
// held item isn't a bucket at all.
func (c *ClientConnection) useBucket(held string) bool {
	if held != fluid.BucketEmpty {
		if _, ok := fluid.BucketKind(held); !ok {
			return false
		}
	}
	if c.player == nil || c.instance == nil {
		return true
	}
	if held == fluid.BucketEmpty {
		c.scoopFluid()
	} else {
		c.emptyBucket(held)
	}
	return true
}

// emptyBucket places the bucket's fluid source in front of the first solid
// block the player looks at.
func (c *ClientConnection) emptyBucket(held string) {
	kind, _ := fluid.BucketKind(held)
	ox, oy, oz, dx, dy, dz := c.lookRay()
	target, ok := fluid.RayToPlacement(c.instance.World.GetBlock, ox, oy, oz, dx, dy, dz)
	if !ok {
		return
	}
	src := kind.Block(0)
	if !c.instance.allowBlockPlace(c, target, src) {
		_ = c.sendBlockUpdate(target, c.instance.World.GetBlock(target))
		return
	}
	c.instance.SetBlock(target, src)
	if c.gamemode() != player.Creative {
		c.swapHeld(fluid.BucketEmpty)
	}
}

// scoopFluid picks up the first fluid source the player looks at.
func (c *ClientConnection) scoopFluid() {
	ox, oy, oz, dx, dy, dz := c.lookRay()
	pos, kind, ok := fluid.RayToSource(c.instance.World.GetBlock, ox, oy, oz, dx, dy, dz)
	if !ok {
		return
	}
	if !c.instance.allowBlockBreak(c, pos) {
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
		return
	}
	c.instance.SetBlock(pos, world.Air)
	if c.gamemode() != player.Creative {
		c.swapHeld(fluid.BucketFor(kind))
	}
}

// swapHeld replaces the held stack with a single item (bucket ↔ filled
// bucket) and syncs the slot.
func (c *ClientConnection) swapHeld(item string) {
	id, ok := world.ItemByName(item)
	if !ok {
		return
	}
	slot := int16(inventory.HotbarStart) + int16(c.heldSlot.Load())
	st := inventory.Stack{ID: id, Count: 1}
	c.inv.Set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
}

// lookRay returns the eye position and the unit look vector.
func (c *ClientConnection) lookRay() (ox, oy, oz, dx, dy, dz float64) {
	s := c.player.Snapshot()
	look := anticheat.LookDir(s.Yaw, s.Pitch)
	return s.X, s.Y + eyeHeight, s.Z, look.X, look.Y, look.Z
}
