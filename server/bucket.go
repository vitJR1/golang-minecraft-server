package server

import (
	"math"

	"minecraft-server/player"
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

const (
	bucketEmpty = "minecraft:bucket"
	bucketWater = "minecraft:water_bucket"
	bucketLava  = "minecraft:lava_bucket"

	bucketReach = 5.0 // blocks (creative reach; survival's 4.5 is close enough)
	bucketStep  = 0.05
)

// bucketFluid maps a filled bucket item to the fluid it holds.
func bucketFluid(item string) (*fluidKind, bool) {
	switch item {
	case bucketWater:
		return fluidWater, true
	case bucketLava:
		return fluidLava, true
	}
	return nil, false
}

// useBucket handles a Use Item with a bucket in hand. Returns false when the
// held item isn't a bucket at all.
func (c *ClientConnection) useBucket(held string) bool {
	if held != bucketEmpty {
		if _, ok := bucketFluid(held); !ok {
			return false
		}
	}
	if c.player == nil || c.instance == nil {
		return true
	}
	if held == bucketEmpty {
		c.scoopFluid()
	} else {
		c.emptyBucket(held)
	}
	return true
}

// emptyBucket places the bucket's fluid source in front of the first solid
// block the player looks at.
func (c *ClientConnection) emptyBucket(held string) {
	kind, _ := bucketFluid(held)
	target, ok := c.rayToPlacement()
	if !ok {
		return
	}
	src := kind.block(0)
	if !c.instance.allowBlockPlace(c, target, src) {
		_ = c.sendBlockUpdate(target, c.instance.World.GetBlock(target))
		return
	}
	c.instance.SetBlock(target, src)
	if c.gamemode() != player.Creative {
		c.swapHeld(bucketEmpty)
	}
}

// scoopFluid picks up the first fluid source the player looks at.
func (c *ClientConnection) scoopFluid() {
	pos, kind, ok := c.rayToFluidSource()
	if !ok {
		return
	}
	if !c.instance.allowBlockBreak(c, pos) {
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
		return
	}
	c.instance.SetBlock(pos, world.Air)
	if c.gamemode() != player.Creative {
		filled := bucketWater
		if kind == fluidLava {
			filled = bucketLava
		}
		c.swapHeld(filled)
	}
}

// swapHeld replaces the held stack with a single item (bucket ↔ filled
// bucket) and syncs the slot.
func (c *ClientConnection) swapHeld(item string) {
	id, ok := world.ItemByName(item)
	if !ok {
		return
	}
	slot := int16(hotbarStart) + int16(c.heldSlot.Load())
	st := itemStack{ID: id, Count: 1}
	c.inv.set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
}

// lookRay returns the eye position and the unit look vector.
func (c *ClientConnection) lookRay() (ox, oy, oz, dx, dy, dz float64) {
	s := c.player.Snapshot()
	yaw := float64(s.Yaw) * math.Pi / 180
	pitch := float64(s.Pitch) * math.Pi / 180
	dx = -math.Sin(yaw) * math.Cos(pitch)
	dz = math.Cos(yaw) * math.Cos(pitch)
	dy = -math.Sin(pitch)
	return s.X, s.Y + eyeHeight, s.Z, dx, dy, dz
}

// rayToPlacement walks the look ray through air and fluids until it meets a
// solid block, returning the last block position the ray occupied before
// the hit. false when nothing solid is within reach.
func (c *ClientConnection) rayToPlacement() (world.Position, bool) {
	ox, oy, oz, dx, dy, dz := c.lookRay()
	last := world.Position{X: floorF(ox), Y: floorF(oy), Z: floorF(oz)}
	for d := 0.0; d <= bucketReach; d += bucketStep {
		pos := world.Position{X: floorF(ox + dx*d), Y: floorF(oy + dy*d), Z: floorF(oz + dz*d)}
		b := c.instance.World.GetBlock(pos)
		if _, _, fluid := fluidOf(b); b != world.Air && !fluid {
			return last, true
		}
		last = pos
	}
	return world.Position{}, false
}

// rayToFluidSource walks the look ray until it meets a fluid source block.
func (c *ClientConnection) rayToFluidSource() (world.Position, *fluidKind, bool) {
	ox, oy, oz, dx, dy, dz := c.lookRay()
	for d := 0.0; d <= bucketReach; d += bucketStep {
		pos := world.Position{X: floorF(ox + dx*d), Y: floorF(oy + dy*d), Z: floorF(oz + dz*d)}
		b := c.instance.World.GetBlock(pos)
		if b == world.Air {
			continue
		}
		kind, level, fluid := fluidOf(b)
		if !fluid {
			return world.Position{}, nil, false // a solid block is in the way
		}
		if level == 0 {
			return pos, kind, true
		}
	}
	return world.Position{}, nil, false
}
