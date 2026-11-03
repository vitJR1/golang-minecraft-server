package server

import (
	"math"
	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/combat"
	"minecraft-server/server/gameplay/itementity"
	"minecraft-server/server/gameplay/projectile"
	"minecraft-server/server/hud/visibility"
	"minecraft-server/world"
)

// projectile.go implements thrown items in an instance: egg, snowball, ender
// pearl, fire charge and arrows (arrow.go). Using one of these (right-click
// in air → SbPlayUseItem) spawns a projectile entity that the instance
// simulates on its tick loop with server/gameplay/projectile's flight. On impact an
// ender pearl teleports its thrower to the landing spot, a fireball
// explodes; egg/snowball just vanish. There's no item consumption (creative).

// eyeHeight is a standing player's eye level above the feet.
const eyeHeight = 1.62

// projectileEntity is one in-flight projectile: its flight, plus what the
// server does with it.
type projectileEntity struct {
	projectile.Flight
	item    string // namespaced item id (for impact behaviour)
	thrower *ClientConnection
	// Game-driven flights (Instance.launchProjectile): onTick runs after
	// each move and returns false to end the flight; onImpact runs when
	// the projectile hits a block, expires, or onTick stopped it.
	onTick   func(x, y, z float64) bool
	onImpact func(x, y, z float64)

	// Arrow state (arrow.go): enchant levels and crit flag from the bow,
	// the player the path crossed this tick, and the stuck-in-a-block
	// countdown.
	power, punch int
	crit         bool
	hitPlayer    *ClientConnection
	stuck        bool
	hitBlock     bool // the flight ended against a block (not expired / stopped)
	stuckTicks   int
	pickup       projectile.Pickup
}

// throwProjectile spawns a projectile from c's eye along their look direction
// and registers it for tick simulation. No-op when the held item isn't
// throwable.
func (c *ClientConnection) throwProjectile(item string) {
	typeID, ok := projectile.ThrowableEntityID(item)
	if !ok {
		return
	}
	c.launch(&projectileEntity{Flight: projectile.Flight{TypeID: typeID}, item: item}, projectile.ThrowSpeed)
}

// throwFireball launches a fire charge as a fireball entity.
func (c *ClientConnection) throwFireball() {
	c.launch(&projectileEntity{
		Flight: projectile.Flight{
			TypeID:    projectile.FireballEntityID,
			NoGravity: true,
			MaxTicks:  projectile.FireballLife,
		},
		item: "minecraft:fire_charge",
	}, projectile.FireballSpeed)
}

// launch fills in p's origin (the thrower's eyes), velocity (look direction
// × speed) and identity, registers it for tick simulation and spawns it for
// everyone. Returns false when the connection can't throw (no player /
// instance / server).
func (c *ClientConnection) launch(p *projectileEntity, speed float64) bool {
	if c.player == nil || c.instance == nil || c.server == nil {
		return false
	}
	s := c.player.Snapshot()
	look := anticheat.LookDir(s.Yaw, s.Pitch)
	p.EID = c.server.nextEntityID.Add(1)
	p.UUID = entityUUID(p.EID)
	p.X, p.Y, p.Z = s.X, s.Y+eyeHeight-0.1, s.Z // vanilla: eye height − 0.1
	p.VX, p.VY, p.VZ = look.X*speed, look.Y*speed, look.Z*speed
	p.thrower = c
	c.instance.addProjectile(p)
	return true
}

// addProjectile registers p for tick simulation and spawns it for everyone,
// with its initial velocity so the client animates it.
func (i *Instance) addProjectile(p *projectileEntity) {
	// Spawned under projMu, like primeTNT: the tick's Teleport / Remove for
	// p can't reach a client before its Spawn.
	i.projMu.Lock()
	defer i.projMu.Unlock()
	i.projectiles = append(i.projectiles, p)
	i.Players.Broadcast(packetid.CbPlaySpawnEntity, p.SpawnPayload(), -1)
	i.Players.Broadcast(packetid.CbPlayEntityVelocity,
		combat.EntityVelocityPayload(p.EID, p.VX, p.VY, p.VZ), -1)
}

// sendProjectiles streams every projectile still in the world — flying, or
// an arrow stuck in a block — to this client (join/respawn), where it is
// now.
func (c *ClientConnection) sendProjectiles() error {
	c.instance.projMu.Lock()
	pkts := make([][]byte, 0, len(c.instance.projectiles))
	for _, p := range c.instance.projectiles {
		pkts = append(pkts, p.SpawnPayload())
	}
	c.instance.projMu.Unlock()
	for _, p := range pkts {
		if err := c.safeWrite(packetid.CbPlaySpawnEntity, p); err != nil {
			return err
		}
	}
	return nil
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
func (i *Instance) stepProjectiles() []*projectileEntity {
	i.projMu.Lock()
	defer i.projMu.Unlock()
	if len(i.projectiles) == 0 {
		return nil
	}

	kept := i.projectiles[:0]
	var landed []*projectileEntity
	var players []*ClientConnection // fetched lazily for arrow pickup
	for _, p := range i.projectiles {
		// An arrow in a block waits to be picked up or for its time to run out.
		if p.stuck {
			if p.pickup != projectile.PickupDisallowed {
				if players == nil {
					players = i.Players.snapshot()
				}
				if c := i.pickupCandidate(p.X, p.Y, p.Z, players); c != nil && i.collectArrow(p, c) {
					continue
				}
			}
			if p.stuckTicks--; p.stuckTicks > 0 {
				kept = append(kept, p)
				continue
			}
			i.Players.Broadcast(packetid.CbPlayRemoveEntities, visibility.RemoveEntitiesPayload([]int32{p.EID}), -1)
			continue
		}

		nx, ny, nz := p.Advance()

		// Walk the step: a full-draw arrow covers 3 blocks a tick, so
		// testing only the end point would tunnel through a thin wall and
		// drag the client's (already stuck) arrow out of sight behind it.
		// On a hit the step ends at the last free point before the block.
		fx, fy, fz, hit := projectile.March(i.solidAt, p.X, p.Y, p.Z, nx, ny, nz)
		if hit {
			nx, ny, nz = fx, fy, fz
		}

		// Arrows hurt whoever stands on this tick's path (up to the wall).
		if p.item == "minecraft:arrow" {
			if v := i.arrowHitPlayer(p, nx, ny, nz); v != nil {
				p.hitPlayer = v
				i.Players.Broadcast(packetid.CbPlayRemoveEntities, visibility.RemoveEntitiesPayload([]int32{p.EID}), -1)
				landed = append(landed, p)
				continue
			}
		}

		if hit && p.item == "minecraft:arrow" {
			// Stick in the block face for a while instead of vanishing:
			// park it where it hit (facing along its last flight) so the
			// client's arrow agrees, then keep it still.
			p.X, p.Y, p.Z = nx, ny, nz
			i.Players.Broadcast(packetid.CbPlayTeleportEntity, p.TeleportPayload(), -1)
			p.stuck, p.stuckTicks = true, projectile.ArrowStuckTicks
			p.VX, p.VY, p.VZ = 0, 0, 0
			kept = append(kept, p)
			continue
		}
		if !hit && !p.Expired() {
			// Still flying: commit the move and show it; a game hook may end
			// the flight here.
			p.X, p.Y, p.Z = nx, ny, nz
			i.Players.Broadcast(packetid.CbPlayTeleportEntity, p.TeleportPayload(), -1)
			if p.onTick == nil || p.onTick(p.X, p.Y, p.Z) {
				kept = append(kept, p)
				continue
			}
		}

		// Impact (or expired, or stopped by the hook): despawn now, act
		// after the lock is released.
		p.hitBlock = hit
		i.Players.Broadcast(packetid.CbPlayRemoveEntities, visibility.RemoveEntitiesPayload([]int32{p.EID}), -1)
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
func (i *Instance) collectArrow(p *projectileEntity, c *ClientConnection) bool {
	switch p.pickup {
	case projectile.PickupAllowed:
		if c.giveItem(arrowItemID(), 1) != 0 {
			return false // inventory full
		}
	case projectile.PickupCreativeOnly:
		if c.gamemode() != player.Creative {
			return false
		}
	default:
		return false
	}
	i.Players.Broadcast(packetid.CbPlayPickupItem, itementity.PickupPayload(p.EID, c.player.EntityID, 1), -1)
	i.Players.Broadcast(packetid.CbPlayRemoveEntities, visibility.RemoveEntitiesPayload([]int32{p.EID}), -1)
	return true
}

// impact runs a landed projectile's effect at its last in-air position:
// ender pearls teleport the thrower, fireballs explode, and the game's
// onImpact hook (if any) fires last.
func (p *projectileEntity) impact(i *Instance) {
	switch p.item {
	case "minecraft:ender_pearl":
		if p.hitBlock { // a pearl lost in the void (expired) takes nobody along
			p.teleportThrower(i)
		}
	case "minecraft:fire_charge":
		i.explodeWith(p.X, p.Y, p.Z, projectile.FireballPower, projectile.FireballKnockback, p.thrower)
	case "minecraft:arrow":
		if p.hitPlayer != nil {
			p.arrowHit(i, p.hitPlayer)
		}
	}
	if p.onImpact != nil {
		p.onImpact(p.X, p.Y, p.Z)
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
// Nobody is moved who has meanwhile died, turned spectator or left the
// instance the pearl flies in (its coordinates mean nothing elsewhere).
func (p *projectileEntity) teleportThrower(i *Instance) {
	c := p.thrower
	if c == nil || c.player == nil || c.isClosed() || c.instance != i ||
		c.player.IsDead() || c.gamemode() == player.Spectator {
		return
	}
	c.player.MoveTo(p.X, p.Y, p.Z, false)
	_ = c.sendSyncPlayerPosition(p.X, p.Y, p.Z)
	c.broadcastEntityTeleport()
}

// floorF is math.Floor as an int.
func floorF(v float64) int { return int(math.Floor(v)) }
