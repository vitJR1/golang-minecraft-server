package server

import (
	"bytes"
	"math"

	"minecraft-server/player"
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
	yaw, pitch float32 // last facing sent (projectileAngles)
	// Game-driven flights (Instance.launchProjectile): onTick runs after
	// each move and returns false to end the flight; onImpact runs when
	// the projectile hits a block, expires, or onTick stopped it.
	onTick   func(x, y, z float64) bool
	onImpact func(x, y, z float64)

	// noGravity keeps the projectile on a straight line (fireballs);
	// gravity/drag override the thrown-item defaults when non-zero;
	// maxTicks overrides projectileMaxTicks when > 0; data is the Spawn
	// Entity data field (arrows: the owner's entity id).
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
	pickup       arrowPickup
}

// arrowPickup is who may collect a stuck arrow (vanilla AbstractArrow.Pickup).
type arrowPickup uint8

const (
	pickupDisallowed   arrowPickup = iota // thrown items, game projectiles
	pickupAllowed                         // a plain survival shot: anyone gets the arrow back
	pickupCreativeOnly                    // creative / Infinity shot: only creative clears it, nothing is gained
)

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
	p.x, p.y, p.z = s.X, s.Y+eyeHeight-0.1, s.Z // vanilla: eye height − 0.1
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
	var players []*ClientConnection // fetched lazily for arrow pickup
	for _, p := range i.projectiles {
		// An arrow in a block waits to be picked up or for its time to run out.
		if p.stuck {
			if p.pickup != pickupDisallowed {
				if players == nil {
					players = i.Players.snapshot()
				}
				if c := i.pickupCandidate(p.x, p.y, p.z, players); c != nil && i.collectArrow(p, c) {
					continue
				}
			}
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

		// Walk the step: a full-draw arrow covers 3 blocks a tick, so
		// testing only the end point would tunnel through a thin wall and
		// drag the client's (already stuck) arrow out of sight behind it.
		// On a hit the step ends at the last free point before the block.
		fx, fy, fz, hit := i.marchSolid(p.x, p.y, p.z, nx, ny, nz)
		if hit {
			nx, ny, nz = fx, fy, fz
		}

		// Arrows hurt whoever stands on this tick's path (up to the wall).
		if p.item == "minecraft:arrow" {
			if v := i.arrowHitPlayer(p, nx, ny, nz); v != nil {
				p.hitPlayer = v
				i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload([]int32{p.eid}), -1)
				landed = append(landed, p)
				continue
			}
		}

		if hit && p.item == "minecraft:arrow" {
			// Stick in the block face for a while instead of vanishing:
			// park it where it hit (facing along its last flight) so the
			// client's arrow agrees, then keep it still.
			p.x, p.y, p.z = nx, ny, nz
			i.Players.Broadcast(CbPlayTeleportEntity, projectileTeleportPayload(p), -1)
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

// collectArrow hands a stuck arrow to c if the arrow's pickup mode lets
// them have it: an allowed arrow goes into the inventory (refused when it
// is full, so the arrow stays), a creative-only one is simply cleared by a
// creative player. Plays the pickup fly-in and removes the entity. Runs
// under projMu, like item pickup runs under itemsMu.
func (i *Instance) collectArrow(p *projectile, c *ClientConnection) bool {
	switch p.pickup {
	case pickupAllowed:
		if c.giveItem(arrowItemID(), 1) != 0 {
			return false // inventory full
		}
	case pickupCreativeOnly:
		if c.gamemode() != player.Creative {
			return false
		}
	default:
		return false
	}
	i.Players.Broadcast(CbPlayPickupItem, pickupItemPayload(p.eid, c.player.EntityID, 1), -1)
	i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload([]int32{p.eid}), -1)
	return true
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

// marchStep is the sampling distance of marchSolid; fine enough that no
// full block is skipped at any projectile speed.
const marchStep = 0.1

// marchSolid walks the segment (x0,y0,z0)→(x1,y1,z1) in marchStep
// increments and reports the first solid block on it: the last free point
// before it and true, or the end point and false when nothing is in the
// way.
func (i *Instance) marchSolid(x0, y0, z0, x1, y1, z1 float64) (float64, float64, float64, bool) {
	dx, dy, dz := x1-x0, y1-y0, z1-z0
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length == 0 {
		return x1, y1, z1, i.solidAt(x1, y1, z1)
	}
	steps := int(math.Ceil(length / marchStep))
	px, py, pz := x0, y0, z0
	for n := 1; n <= steps; n++ {
		f := float64(n) / float64(steps)
		cx, cy, cz := x0+dx*f, y0+dy*f, z0+dz*f
		if i.solidAt(cx, cy, cz) {
			return px, py, pz, true
		}
		px, py, pz = cx, cy, cz
	}
	return x1, y1, z1, false
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
	_ = c.sendSyncPlayerPosition(p.x, p.y, p.z)
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
	yaw, pitch := projectileAngles(p)
	buf.WriteByte(protocol.AngleToByte(pitch))
	buf.WriteByte(protocol.AngleToByte(yaw))
	buf.WriteByte(protocol.AngleToByte(yaw))     // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, p.data) // data (arrows: owner id)
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
	yaw, pitch := projectileAngles(p)
	buf.WriteByte(protocol.AngleToByte(yaw))
	buf.WriteByte(protocol.AngleToByte(pitch))
	buf.WriteByte(0) // on ground = false
	return buf.Bytes()
}

// projectileAngles is the yaw/pitch (degrees) a projectile faces, derived
// from its velocity the way vanilla's AbstractArrow.tick does — yaw =
// atan2(vx, vz), pitch = atan2(vy, horizontal) — so arrows render along
// their flight instead of all pointing the same way. A resting projectile
// keeps the angles of its last move.
func projectileAngles(p *projectile) (yaw, pitch float32) {
	h := math.Sqrt(p.vx*p.vx + p.vz*p.vz)
	if h == 0 && p.vy == 0 {
		return p.yaw, p.pitch
	}
	p.yaw = float32(math.Atan2(p.vx, p.vz) * 180 / math.Pi)
	p.pitch = float32(math.Atan2(p.vy, h) * 180 / math.Pi)
	return p.yaw, p.pitch
}
