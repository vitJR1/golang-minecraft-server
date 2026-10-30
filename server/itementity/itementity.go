// Package itementity models dropped items ("minecraft:item" entities): a
// resource popped out of a BedWars generator or a stack a player threw, lying
// on the ground until a player walks over it. An Entity falls onto the block
// below and slides to a stop (Step), merges with a same-item drop close by
// (CanMerge), is collected from inside a player's pickup box (InPickupBox)
// once its pickup delay is over, and despawns after five minutes. The server
// keeps them per instance and mirrors every change to the clients (Spawn
// Entity + metadata, Teleport Entity, Pickup Item, Remove Entities).
package itementity

import (
	"bytes"
	"math"

	"minecraft-server/anticheat"
	"minecraft-server/protocol"
	"minecraft-server/server/inventory"
)

// TypeID is the item entity type id (protocol 763), from minecraft-data.
const TypeID int32 = 54

// Physics / pickup tuning.
const (
	PopVelocity  = 0.2  // initial upward hop when a drop spawns (blocks/tick)
	PickupDelay  = 10   // ticks before a fresh drop can be collected (shows the hop)
	ThrowDelay   = 40   // ticks before a player-thrown item can be collected (vanilla: 2s)
	DespawnTicks = 6000 // 5 minutes on the ground, like vanilla
	MergeRadius  = 1.0  // same-item drops within this distance merge into one stack

	gravity        = 0.04 // subtracted from vy each tick while airborne
	drag           = 0.98 // vertical velocity retained each tick
	groundFriction = 0.6  // horizontal velocity retained per tick once resting on a block
	pickupRadiusXZ = 1.25 // horizontal reach of the pickup sweep from the player's centre
	pickupBelow    = 0.5  // how far below the feet a drop is still collected
	pickupAbove    = 2.25 // how far above the feet (player box 1.8 + 0.5 reach)

	throwSpeed  = 0.3 // blocks/tick along the look direction (vanilla item toss)
	throwLift   = 0.1 // extra upward component
	throwHeight = anticheat.EyeHeight - 0.3
)

// SolidAt reports whether the block at a point stops an item.
type SolidAt func(x, y, z float64) bool

// Entity is one dropped stack lying in (or falling through) an instance.
type Entity struct {
	EID        int32
	UUID       [16]byte
	Stack      inventory.Stack // item + count + NBT (a dropped enchanted sword keeps its enchantments)
	X, Y, Z    float64
	VX, VY, VZ float64
	OnGround   bool
	Ticks      int // age in ticks (pickup delay + despawn)
	PickupAt   int // tick age from which the item can be collected
}

// Step advances the item one tick: airborne it falls until it lands on a
// block; resting but still sliding (a throw that just landed) it keeps
// moving with ground friction and falls again off an edge. moved reports
// whether its position changed; out that it fell out of the world.
func (it *Entity) Step(solid SolidAt) (moved, out bool) {
	it.Ticks++
	if !it.OnGround {
		it.VY = it.VY*drag - gravity
		ny := it.Y + it.VY
		if it.VY < 0 && solid(it.X, ny, it.Z) {
			it.Y = math.Floor(ny) + 1 // rest on top of the block it hit
			it.VY = 0
			it.OnGround = true
		} else {
			it.Y = ny
		}
		it.slide(solid, drag)
		return true, it.Y < -64
	}
	if it.VX != 0 || it.VZ != 0 {
		it.slide(solid, groundFriction)
		if !solid(it.X, it.Y-0.01, it.Z) {
			it.OnGround = false
		}
		return true, false
	}
	return false, false
}

// slide advances the item horizontally by its velocity, stopping dead
// against a solid block, then applies the per-tick drag. Velocities below
// a hair are zeroed so a resting item stops generating Teleport packets.
func (it *Entity) slide(solid SolidAt, drag float64) {
	if it.VX == 0 && it.VZ == 0 {
		return
	}
	nx, nz := it.X+it.VX, it.Z+it.VZ
	if solid(nx, it.Y+0.1, nz) {
		it.VX, it.VZ = 0, 0
		return
	}
	it.X, it.Z = nx, nz
	it.VX *= drag
	it.VZ *= drag
	if math.Abs(it.VX) < 0.003 && math.Abs(it.VZ) < 0.003 {
		it.VX, it.VZ = 0, 0
	}
}

// Expired: the item has lain long enough to despawn.
func (it *Entity) Expired() bool { return it.Ticks >= DespawnTicks }

// Collectable: its pickup delay is over.
func (it *Entity) Collectable() bool { return it.Ticks >= it.PickupAt }

// Near reports whether the item lies within radius of (x, y, z), in the
// horizontal plane and vertically.
func (it *Entity) Near(x, y, z, radius float64) bool {
	return math.Hypot(it.X-x, it.Z-z) <= radius && math.Abs(it.Y-y) <= radius
}

// CanMerge reports whether count more of st dropped at (x, y, z) can join
// this item: the same kind, room in the stack (limit), close enough.
func (it *Entity) CanMerge(st inventory.Stack, count, limit int, x, y, z float64) bool {
	return it.Stack.SameKind(st) && int(it.Stack.Count)+count <= limit && it.Near(x, y, z, MergeRadius)
}

// InPickupBox reports whether a point lies in the pickup box of a player with
// feet at (fx, fy, fz) (vanilla: the hitbox grown by 1 horizontally, 0.5
// vertically). Stuck arrows use it too.
func InPickupBox(fx, fy, fz, x, y, z float64) bool {
	return math.Hypot(fx-x, fz-z) <= pickupRadiusXZ && y >= fy-pickupBelow && y <= fy+pickupAbove
}

// Throw is where a stack thrown by a player at feet (x, y, z) looking
// (yaw, pitch) starts and how fast it flies (vanilla item toss).
func Throw(x, y, z float64, yaw, pitch float32) (sx, sy, sz, vx, vy, vz float64) {
	look := anticheat.LookDir(yaw, pitch)
	return x, y + throwHeight, z, look.X * throwSpeed, look.Y*throwSpeed + throwLift, look.Z * throwSpeed
}

// SpawnPayload builds Spawn Entity (0x01) for the item, with its current
// velocity so a fresh drop visibly hops.
func (it *Entity) SpawnPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, it.EID)
	buf.Write(it.UUID[:])
	protocol.WriteVarInt32ToBuffer(&buf, TypeID)
	buf.Write(protocol.WriteDouble(it.X))
	buf.Write(protocol.WriteDouble(it.Y))
	buf.Write(protocol.WriteDouble(it.Z))
	buf.WriteByte(0)                        // pitch
	buf.WriteByte(0)                        // yaw
	buf.WriteByte(0)                        // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, 0) // data (unused for items)
	buf.Write(protocol.WriteShort(protocol.VelocityShort(it.VX)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(it.VY)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(it.VZ)))
	return buf.Bytes()
}

// MetadataPayload builds Set Entity Metadata (0x52) carrying the item
// stack (index 8, type Slot) — without it the client renders nothing.
func (it *Entity) MetadataPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, it.EID)
	buf.WriteByte(8)                        // index: Item
	protocol.WriteVarInt32ToBuffer(&buf, 7) // type: Slot
	inventory.WriteSlot(&buf, it.Stack)
	buf.WriteByte(0xFF) // end of metadata
	return buf.Bytes()
}

// TeleportPayload builds Teleport Entity (0x68) for the item's position.
func (it *Entity) TeleportPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, it.EID)
	buf.Write(protocol.WriteDouble(it.X))
	buf.Write(protocol.WriteDouble(it.Y))
	buf.Write(protocol.WriteDouble(it.Z))
	buf.WriteByte(0) // yaw
	buf.WriteByte(0) // pitch
	if it.OnGround {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// PickupPayload builds Pickup Item (0x67): the client plays the "item
// flies into the player" animation for collected → collector.
func PickupPayload(collected, collector int32, count int) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, collected)
	protocol.WriteVarInt32ToBuffer(&buf, collector)
	protocol.WriteVarInt32ToBuffer(&buf, int32(count))
	return buf.Bytes()
}
