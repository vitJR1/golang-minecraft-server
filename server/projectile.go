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
	// Game-driven flights (Instance.launchProjectile): onTick runs after
	// each move and returns false to end the flight; onImpact runs when
	// the projectile hits a block, expires, or onTick stopped it.
	onTick   func(x, y, z float64) bool
	onImpact func(x, y, z float64)

	// noGravity keeps the projectile on a straight line (fireballs);
	// gravity/drag override the thrown-item defaults when non-zero;
	// maxTicks overrides projectileMaxTicks when > 0; data is the Spawn
	// Entity data field (arrows: shooter id + 1).
	noGravity     bool
	gravity, drag float64
	maxTicks      int
	data          int32

	// Arrow state (arrow.go): enchant levels and crit flag from the bow,
	// the player the path crossed this tick, and the stuck-in-a-block
	// countdown.
	power, punch int
	crit         bool
	hitPlayer    *ClientConnection
	stuck        bool
	stuckTicks   int
}

// Fireball (fire charge thrown in the air): straight flight at fireballSpeed
// for up to fireballLife ticks, then a power-1.5 explosion with 1.5× the
// knockback (vanilla ghast fireball feel).
const (
	fireballEntityID  int32 = 57
	fireballSpeed           = 1.0
	fireballLife            = 100
	fireballPower           = 1.5
	fireballKnockback       = 1.5
)

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
	if !ok {
		return
	}
	c.launch(&projectile{typeID: typeID, item: item}, throwSpeed)
}

// throwFireball launches a fire charge as a fireball entity.
func (c *ClientConnection) throwFireball() {
	c.launch(&projectile{
		typeID:    fireballEntityID,
		item:      "minecraft:fire_charge",
		noGravity: true,
		maxTicks:  fireballLife,
	}, fireballSpeed)
}

// launch fills in p's origin (the thrower's eyes), velocity (look direction
// × speed) and identity, registers it for tick simulation and spawns it for
// everyone. Returns false when the connection can't throw (no player /
// instance / server).
func (c *ClientConnection) launch(p *projectile, speed float64) bool {
	if c.player == nil || c.instance == nil || c.server == nil {
		return false
	}
	s := c.player.Snapshot()

	// Look direction (Minecraft yaw 0 = +Z south, pitch down = +).
	yaw := float64(s.Yaw) * math.Pi / 180
	pitch := float64(s.Pitch) * math.Pi / 180
	dx := -math.Sin(yaw) * math.Cos(pitch)
	dy := -math.Sin(pitch)
	dz := math.Cos(yaw) * math.Cos(pitch)

	p.eid = c.server.nextEntityID.Add(1)
	p.uuid = entityUUID(p.eid)
	p.x, p.y, p.z = s.X, s.Y+eyeHeight, s.Z
	p.vx, p.vy, p.vz = dx*speed, dy*speed, dz*speed
	p.thrower = c

	c.instance.projMu.Lock()
	c.instance.projectiles = append(c.instance.projectiles, p)
	c.instance.projMu.Unlock()

	// Spawn for everyone with the initial velocity so the client animates it.
	c.instance.Players.Broadcast(CbPlaySpawnEntity, spawnProjectilePayload(p), -1)
	c.instance.Players.Broadcast(CbPlayEntityVelocity,
		entityVelocityPayload(p.eid, p.vx, p.vy, p.vz), -1)
	return true
}

// projectileTick advances every in-flight projectile one step: physics, then
// collision / lifetime checks. Survivors get a Teleport Entity so clients see
// the real (gravity-affected) path; impacts are removed and ender pearls
// teleport their thrower. Registered on every instance in NewInstance.
func (i *Instance) projectileTick(uint64) {
	for _, p := range i.stepProjectiles() {
		p.impact(i)
	}
}

// stepProjectiles advances every projectile under projMu and returns the
// ones that landed this tick; their effects run after the lock is released
// (explosions and teleports call back into the instance).
func (i *Instance) stepProjectiles() []*projectile {
	i.projMu.Lock()
	defer i.projMu.Unlock()
	if len(i.projectiles) == 0 {
		return nil
	}

	kept := i.projectiles[:0]
	var landed []*projectile
	for _, p := range i.projectiles {
		// An arrow in a block just waits out its time.
		if p.stuck {
			if p.stuckTicks--; p.stuckTicks > 0 {
				kept = append(kept, p)
				continue
			}
			i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload([]int32{p.eid}), -1)
			continue
		}

		// Integrate: drag, gravity (unless the projectile flies straight), move.
		drag, gravity := throwDrag, throwGravity
		if p.drag != 0 {
			drag = p.drag
		}
		if p.gravity != 0 {
			gravity = p.gravity
		}
		p.vx *= drag
		p.vz *= drag
		if p.noGravity {
			p.vy *= drag
		} else {
			p.vy = p.vy*drag - gravity
		}
		nx, ny, nz := p.x+p.vx, p.y+p.vy, p.z+p.vz
		p.ticks++
		life := projectileMaxTicks
		if p.maxTicks > 0 {
			life = p.maxTicks
		}

		// Arrows hurt whoever stands on this tick's path.
		if p.item == "minecraft:arrow" {
			if v := i.arrowHitPlayer(p, nx, ny, nz); v != nil {
				p.hitPlayer = v
				i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload([]int32{p.eid}), -1)
				landed = append(landed, p)
				continue
			}
		}

		hit := i.solidAt(nx, ny, nz)
		if hit && p.item == "minecraft:arrow" {
			// Stick in the block face for a while instead of vanishing.
			p.stuck, p.stuckTicks = true, arrowStuckTicks
			p.vx, p.vy, p.vz = 0, 0, 0
			kept = append(kept, p)
			continue
		}
		if !hit && p.ticks < life {
			// Still flying: commit the move and show it; a game hook may end
			// the flight here.
			p.x, p.y, p.z = nx, ny, nz
			i.Players.Broadcast(CbPlayTeleportEntity, projectileTeleportPayload(p), -1)
			if p.onTick == nil || p.onTick(p.x, p.y, p.z) {
				kept = append(kept, p)
				continue
			}
		}

		// Impact (or expired, or stopped by the hook): despawn now, act
		// after the lock is released.
		i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload([]int32{p.eid}), -1)
		landed = append(landed, p)
	}
	// Zero out the drained tail so removed projectiles can be GC'd.
	for j := len(kept); j < len(i.projectiles); j++ {
		i.projectiles[j] = nil
	}
	i.projectiles = kept
	return landed
}

// impact runs a landed projectile's effect at its last in-air position:
// ender pearls teleport the thrower, fireballs explode, and the game's
// onImpact hook (if any) fires last.
func (p *projectile) impact(i *Instance) {
	switch p.item {
	case "minecraft:ender_pearl":
		p.teleportThrower()
	case "minecraft:fire_charge":
		i.explodeWith(p.x, p.y, p.z, fireballPower, fireballKnockback, p.thrower)
	case "minecraft:arrow":
		if p.hitPlayer != nil {
			p.arrowHit(i, p.hitPlayer)
		}
	}
	if p.onImpact != nil {
		p.onImpact(p.x, p.y, p.z)
	}
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
	buf.WriteByte(0)                             // pitch
	buf.WriteByte(0)                             // yaw
	buf.WriteByte(0)                             // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, p.data) // data (arrows: shooter id + 1)
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
