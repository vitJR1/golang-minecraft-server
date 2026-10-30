package server

import (
	"math"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/combat"
	"minecraft-server/server/enchant"
	"minecraft-server/server/projectile"
	"minecraft-server/server/sound"
	"minecraft-server/world"
)

// arrow.go: bows and arrows, vanilla-shaped.
//
//   - Use Item with a bow starts a draw (useState.bow); Player Action 5
//     (release) looses it. Draw force f = min(1, (t² + 2t)/3) for t seconds
//     held; below 0.1 nothing happens. One arrow leaves the inventory (not
//     in creative) and the bow wears a point.
//   - The arrow is a projectile (entity type 3) at 3·f blocks/tick with
//     gravity 0.05 and drag 0.99, spawned with the shooter as its owner
//     (Spawn Entity data) so the client's own arrow simulation doesn't
//     collide with the shooter and bounce back, and with its yaw/pitch
//     taken from the velocity so it renders along its path. Other players
//     see the shooter's draw pose through the living-entity hand-state
//     metadata (consume.go). Every tick its path segment is tested
//     against the players' hitboxes (inflated by 0.3, like vanilla); the
//     shooter, the dead and spectators are skipped, and the game's
//     OnPlayerAttack veto applies to the shooter→victim pair.
//   - Damage = ceil(speed × (2 + Power bonus)), Power bonus 0.5·lvl + 0.5;
//     ×1.5 on a full-draw (critical) shot; then armor. Punch shoves the
//     victim along the arrow by 0.6·lvl. A lethal arrow kills with the
//     shooter credited.
//   - An arrow that hits a block sticks there and vanishes after 60 s —
//     unless a player walks into it first: a survival shot without
//     Infinity goes back into their inventory (Pickup Item fly-in), a
//     creative or Infinity shot is only cleared by a creative player.
//     Infinity itself keeps the arrow in the quiver (one must still be
//     there).

// startDraw begins a bow draw. Needs an arrow somewhere in the inventory
// (creative players always have one).
func (c *ClientConnection) startDraw() {
	if c.instance == nil {
		return
	}
	if c.gamemode() != player.Creative && c.countItem(arrowItemID()) == 0 {
		return
	}
	c.beginUse(&useState{slot: c.heldSlot.Load(), item: "minecraft:bow", startTick: c.instance.Tick(), bow: true})
}

// shootBow looses the arrow of a finished draw.
func (c *ClientConnection) shootBow(u *useState) {
	if c.instance == nil || c.player == nil || c.player.IsDead() {
		return
	}
	if c.heldSlot.Load() != u.slot || c.heldItemName() != "minecraft:bow" {
		return
	}
	f := projectile.DrawForce(c.instance.Tick() - u.startTick)
	if f < projectile.ArrowMinForce {
		return
	}
	bow := c.inv.Held(u.slot)
	creative := c.gamemode() == player.Creative
	infinite := c.enchantOf(bow, enchant.Infinity) > 0
	switch {
	case creative:
	case infinite:
		// Infinity: the arrow stays in the quiver, but one must be there.
		if c.countItem(arrowItemID()) == 0 {
			return
		}
	default:
		if !c.takeItem(arrowItemID(), 1) {
			return
		}
	}
	pickup := projectile.PickupAllowed
	if creative || infinite {
		pickup = projectile.PickupCreativeOnly // vanilla: nothing to get back
	}
	p := &projectileEntity{
		Flight: projectile.Flight{
			TypeID:  projectile.ArrowEntityID,
			Gravity: world.ArrowGravity,
			Drag:    world.ArrowDrag,
			Data:    c.player.EntityID, // Spawn Entity data = owner id (0 = none) since 1.17's Projectile
		},
		pickup: pickup,
		item:   "minecraft:arrow",
		power:  c.enchantOf(bow, enchant.Power),
		punch:  c.enchantOf(bow, enchant.Punch),
		crit:   f >= 1,
	}
	if !c.launch(p, world.ArrowSpeed*f) {
		return
	}
	c.damageHeldTool(1)
	s := c.player.Snapshot()
	c.instance.playSound("minecraft:entity.arrow.shoot", sound.CategoryPlayer, s.X, s.Y, s.Z, 1, float32(1/(f*0.5+1.2)))
}

func arrowItemID() int32 {
	id, _ := world.ItemByName("minecraft:arrow")
	return id
}

// arrowHitPlayer returns the first player whose (inflated) hitbox the
// arrow's path from (p.X,p.Y,p.Z) to (nx,ny,nz) crosses, skipping the
// shooter, the dead and spectators.
func (i *Instance) arrowHitPlayer(p *projectileEntity, nx, ny, nz float64) *ClientConnection {
	var best *ClientConnection
	bestT := math.Inf(1)
	for _, c := range i.Players.snapshot() {
		if c == p.thrower || c.player == nil || c.player.IsDead() {
			continue
		}
		s := c.player.Snapshot()
		if s.Gamemode == player.Spectator {
			continue
		}
		half := anticheat.PlayerWidth/2 + world.ArrowHitInflate
		t, ok := anticheat.SegmentHitsBox(p.X, p.Y, p.Z, nx, ny, nz,
			s.X-half, s.Y-world.ArrowHitInflate, s.Z-half,
			s.X+half, s.Y+anticheat.PlayerHeight+world.ArrowHitInflate, s.Z+half)
		if ok && t < bestT {
			best, bestT = c, t
		}
	}
	return best
}

// arrowHit resolves an arrow that crossed victim's hitbox: game veto,
// damage through armor, Punch knockback, death credited to the shooter.
func (p *projectileEntity) arrowHit(i *Instance, victim *ClientConnection) {
	shooter := p.thrower
	if shooter != nil && (shooter.instance != i || shooter.isClosed()) {
		shooter = nil
	}
	if shooter != nil && !i.allowAttack(shooter, victim) {
		return
	}
	vp := victim.player
	if vp == nil || vp.IsDead() || !i.PvPEnabled() {
		return
	}
	speed := math.Sqrt(p.VX*p.VX + p.VY*p.VY + p.VZ*p.VZ)
	dmg := victim.absorbDamage(projectile.ArrowDamage(speed, p.power, p.crit))
	now := i.Tick()
	applied, newHealth, killed := vp.ApplyDamage(dmg, now, i.Combat.InvulnTicks)
	if applied <= 0 {
		return
	}
	s := vp.Snapshot()
	i.Players.Broadcast(packetid.CbPlayHurtAnimation, combat.HurtAnimationPayload(s.EntityID, 0), -1)
	i.playSound("minecraft:entity.arrow.hit_player", sound.CategoryPlayer, s.X, s.Y, s.Z, 1, 1)
	if p.punch > 0 && speed > 0 {
		k := projectile.PunchStrength * float64(p.punch)
		i.Players.Broadcast(packetid.CbPlayEntityVelocity,
			combat.EntityVelocityPayload(s.EntityID, p.VX/speed*k, 0.1, p.VZ/speed*k), -1)
		victim.giveVelocity(p.VX/speed*k, 0.1, p.VZ/speed*k)
	}
	if killed {
		victim.die(shooter)
		return
	}
	_ = victim.sendSetHealth(newHealth)
}
