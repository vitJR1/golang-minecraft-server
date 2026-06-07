package server

import (
	"bytes"
	"math"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// projectile.go implements thrown items: egg, snowball, and ender pearl. Using
// one of these (right-click in air → SbPlayUseItem) spawns a projectile entity
// that the instance simulates on its tick loop — gravity, air drag, and block
// collision. On impact an ender pearl teleports its thrower to the landing
// spot; egg/snowball just vanish. There's no item consumption (creative).

// Projectile entity type ids (protocol 763), from minecraft-data.
const (
	eggEntityID        int32 = 24
	snowballEntityID   int32 = 92
	enderPearlEntityID int32 = 28
)

// Throw/flight tuning (blocks per tick). Roughly matches vanilla thrown items.
const (
	throwSpeed         = 1.5
	throwGravity       = 0.03 // subtracted from vy each tick
	throwDrag          = 0.99 // velocity retained each tick
	eyeHeight          = 1.62
	projectileMaxTicks = 200 // ~10s safety cap so a stray projectile can't live forever
)

// projectile is one in-flight thrown item.
type projectile struct {
	eid        int32
	uuid       [16]byte
	typeID     int32
	item       string // namespaced item id (for impact behaviour)
	x, y, z    float64
	vx, vy, vz float64
	thrower    *ClientConnection
	ticks      int
}

// throwableEntityID maps a throwable item id to its projectile entity type id,
// returning ok=false for non-throwable items.
func throwableEntityID(item string) (int32, bool) {
	switch item {
	case "minecraft:egg":
		return eggEntityID, true
	case "minecraft:snowball":
		return snowballEntityID, true
	case "minecraft:ender_pearl":
		return enderPearlEntityID, true
	default:
		return 0, false
	}
}

// throwProjectile spawns a projectile from c's eye along their look direction
// and registers it for tick simulation. No-op when the held item isn't
// throwable.
func (c *ClientConnection) throwProjectile(item string) {
	typeID, ok := throwableEntityID(item)
	if !ok || c.player == nil || c.instance == nil || c.server == nil {
		return
	}
	s := c.player.Snapshot()

	// Look direction (Minecraft yaw 0 = +Z south, pitch down = +).
	yaw := float64(s.Yaw) * math.Pi / 180
	pitch := float64(s.Pitch) * math.Pi / 180
	dx := -math.Sin(yaw) * math.Cos(pitch)
	dy := -math.Sin(pitch)
	dz := math.Cos(yaw) * math.Cos(pitch)

	p := &projectile{
		eid:     c.server.nextEntityID.Add(1),
		typeID:  typeID,
		item:    item,
		x:       s.X,
		y:       s.Y + eyeHeight,
		z:       s.Z,
		vx:      dx * throwSpeed,
		vy:      dy * throwSpeed,
		vz:      dz * throwSpeed,
		thrower: c,
	}
	p.uuid = entityUUID(p.eid)

	c.instance.projMu.Lock()
	c.instance.projectiles = append(c.instance.projectiles, p)
	c.instance.projMu.Unlock()

	// Spawn for everyone with the initial velocity so the client animates it.
	c.instance.Players.Broadcast(CbPlaySpawnEntity, spawnProjectilePayload(p), -1)
	c.instance.Players.Broadcast(CbPlayEntityVelocity,
		entityVelocityPayload(p.eid, p.vx, p.vy, p.vz), -1)
}

// projectileTick advances every in-flight projectile one step: physics, then
// collision / lifetime checks. Survivors get a Teleport Entity so clients see
// the real (gravity-affected) path; impacts are removed and ender pearls
// teleport their thrower. Registered on every instance in NewInstance.
func (i *Instance) projectileTick(uint64) {
	i.projMu.Lock()
	defer i.projMu.Unlock()
	if len(i.projectiles) == 0 {
		return
	}

	kept := i.projectiles[:0]
	for _, p := range i.projectiles {
		// Integrate: drag, gravity, move.
		p.vx *= throwDrag
		p.vy = p.vy*throwDrag - throwGravity
		p.vz *= throwDrag
		nx, ny, nz := p.x+p.vx, p.y+p.vy, p.z+p.vz
		p.ticks++

		hit := i.solidAt(nx, ny, nz)
		if !hit && p.ticks < projectileMaxTicks {
			// Still flying: commit the move and show it.
			p.x, p.y, p.z = nx, ny, nz
			i.Players.Broadcast(CbPlayTeleportEntity, projectileTeleportPayload(p), -1)
			kept = append(kept, p)
			continue
		}

		// Impact (or expired): despawn, and pearl-teleport the thrower to the
		// last in-air position (p.x/y/z, before entering the block).
		i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload([]int32{p.eid}), -1)
		if p.item == "minecraft:ender_pearl" {
			p.teleportThrower()
		}
	}
	// Zero out the drained tail so removed projectiles can be GC'd.
	for j := len(kept); j < len(i.projectiles); j++ {
		i.projectiles[j] = nil
	}
	i.projectiles = kept
}

// solidAt reports whether the block containing (x,y,z) is non-air (a crude
// collision test — treats every non-air block as solid).
func (i *Instance) solidAt(x, y, z float64) bool {
	pos := world.Position{X: floorF(x), Y: floorF(y), Z: floorF(z)}
	return i.World.GetBlock(pos) != world.Air
}

// teleportThrower moves the ender pearl's thrower to the projectile's landing
// position (centre of the block column, on top), syncing the client and
// updating the entity for everyone else. Runs on the tick goroutine.
func (p *projectile) teleportThrower() {
	c := p.thrower
	if c == nil || c.player == nil || c.isClosed() || c.instance == nil {
		return
	}
	c.player.MoveTo(p.x, p.y, p.z, false)
	_ = c.sendSyncPlayerPosition(p.x, p.y, p.z, 1)
	c.broadcastEntityTeleport()
}

// floorF is math.Floor as an int.
func floorF(v float64) int { return int(math.Floor(v)) }

// spawnProjectilePayload builds Spawn Entity (0x01) for a projectile, carrying
// its initial velocity so the client animates the throw.
func spawnProjectilePayload(p *projectile) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, p.eid)
	buf.Write(p.uuid[:])
	protocol.WriteVarInt32ToBuffer(&buf, p.typeID)
	buf.Write(protocol.WriteDouble(p.x))
	buf.Write(protocol.WriteDouble(p.y))
	buf.Write(protocol.WriteDouble(p.z))
	buf.WriteByte(0)                        // pitch
	buf.WriteByte(0)                        // yaw
	buf.WriteByte(0)                        // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, 0) // data (no owner)
	buf.Write(protocol.WriteShort(velocityShort(p.vx)))
	buf.Write(protocol.WriteShort(velocityShort(p.vy)))
	buf.Write(protocol.WriteShort(velocityShort(p.vz)))
	return buf.Bytes()
}

// projectileTeleportPayload builds Teleport Entity (0x68) for a projectile at
// its current position.
func projectileTeleportPayload(p *projectile) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, p.eid)
	buf.Write(protocol.WriteDouble(p.x))
	buf.Write(protocol.WriteDouble(p.y))
	buf.Write(protocol.WriteDouble(p.z))
	buf.WriteByte(0) // yaw
	buf.WriteByte(0) // pitch
	buf.WriteByte(0) // on ground = false
	return buf.Bytes()
}
