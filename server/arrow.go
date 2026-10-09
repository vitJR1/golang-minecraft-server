package server

import (
	"math"

	"minecraft-server/player"
	"minecraft-server/world"
)

// arrow.go: bows and arrows, vanilla-shaped.
//
//   - Use Item with a bow starts a draw (useState.bow); Player Action 5
//     (release) looses it. Draw force f = min(1, (t² + 2t)/3) for t seconds
//     held; below 0.1 nothing happens. One arrow leaves the inventory (not
//     in creative) and the bow wears a point.
//   - The arrow is a projectile (entity type 3) at 3·f blocks/tick with
//     gravity 0.05 and drag 0.99. Every tick its path segment is tested
//     against the players' hitboxes (inflated by 0.3, like vanilla); the
//     shooter, the dead and spectators are skipped, and the game's
//     OnPlayerAttack veto applies to the shooter→victim pair.
//   - Damage = ceil(speed × (2 + Power bonus)), Power bonus 0.5·lvl + 0.5;
//     ×1.5 on a full-draw (critical) shot; then armor. Punch shoves the
//     victim along the arrow by 0.6·lvl. A lethal arrow kills with the
//     shooter credited.
//   - An arrow that hits a block sticks there and vanishes after 60 s.
const (
	arrowEntityID   int32   = 3
	arrowSpeed              = 3.0  // blocks/tick at full draw
	arrowGravity            = 0.05 // per tick
	arrowDrag               = 0.99
	arrowMinForce           = 0.1
	arrowHitInflate         = 0.3
	arrowStuckTicks         = 1200 // 60 s in a block, then gone
	arrowBaseDamage float32 = 2
	arrowCritScale  float32 = 1.5
	punchStrength           = 0.6 // horizontal blocks/tick per Punch level
)

// startDraw begins a bow draw. Needs an arrow somewhere in the inventory
// (creative players always have one).
func (c *ClientConnection) startDraw() {
	if c.instance == nil {
		return
	}
	if c.gamemode() != player.Creative && c.countItem(arrowItemID()) == 0 {
		return
	}
	c.using.Store(&useState{slot: c.heldSlot.Load(), item: "minecraft:bow", startTick: c.instance.Tick(), bow: true})
}

// drawForce is the vanilla charge curve for a bow held for ticks.
func drawForce(ticks uint64) float64 {
	t := float64(ticks) / 20
	f := (t*t + 2*t) / 3
	if f > 1 {
		f = 1
	}
	return f
}

// shootBow looses the arrow of a finished draw.
func (c *ClientConnection) shootBow(u *useState) {
	if c.instance == nil || c.player == nil || c.player.IsDead() {
		return
	}
	if c.heldSlot.Load() != u.slot || c.heldItemName() != "minecraft:bow" {
		return
	}
	f := drawForce(c.instance.Tick() - u.startTick)
	if f < arrowMinForce {
		return
	}
	if c.gamemode() != player.Creative && !c.takeItem(arrowItemID(), 1) {
		return
	}
	bow := c.inv.held(u.slot)
	p := &projectile{
		typeID:  arrowEntityID,
		item:    "minecraft:arrow",
		gravity: arrowGravity,
		drag:    arrowDrag,
		data:    c.player.EntityID + 1, // vanilla: shooter id + 1
		power:   c.enchantOf(bow, EnchantPower),
		punch:   c.enchantOf(bow, EnchantPunch),
		crit:    f >= 1,
	}
	if !c.launch(p, arrowSpeed*f) {
		return
	}
	c.damageHeldTool(1)
	s := c.player.Snapshot()
	c.instance.playSound("minecraft:entity.arrow.shoot", soundCategoryPlayer, s.X, s.Y, s.Z, 1, float32(1/(f*0.5+1.2)))
}

func arrowItemID() int32 {
	id, _ := world.ItemByName("minecraft:arrow")
	return id
}

// arrowHitPlayer returns the first player whose (inflated) hitbox the
// arrow's path from (p.x,p.y,p.z) to (nx,ny,nz) crosses, skipping the
// shooter, the dead and spectators.
func (i *Instance) arrowHitPlayer(p *projectile, nx, ny, nz float64) *ClientConnection {
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
		half := playerHalfWidth + arrowHitInflate
		t, ok := segmentHitsBox(p.x, p.y, p.z, nx, ny, nz,
			s.X-half, s.Y-arrowHitInflate, s.Z-half,
			s.X+half, s.Y+playerHeight+arrowHitInflate, s.Z+half)
		if ok && t < bestT {
			best, bestT = c, t
		}
	}
	return best
}

// segmentHitsBox is the slab test of the segment a→b against an
// axis-aligned box, returning the entry parameter t ∈ [0,1] on a hit.
func segmentHitsBox(ax, ay, az, bx, by, bz, minX, minY, minZ, maxX, maxY, maxZ float64) (float64, bool) {
	tmin, tmax := 0.0, 1.0
	axis := func(a, b, lo, hi float64) bool {
		d := b - a
		if math.Abs(d) < 1e-9 {
			return a >= lo && a <= hi
		}
		t1, t2 := (lo-a)/d, (hi-a)/d
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		tmin = math.Max(tmin, t1)
		tmax = math.Min(tmax, t2)
		return tmin <= tmax
	}
	if !axis(ax, bx, minX, maxX) || !axis(ay, by, minY, maxY) || !axis(az, bz, minZ, maxZ) {
		return 0, false
	}
	return tmin, true
}

// arrowDamage is the hit damage of an arrow travelling at speed blocks/tick.
func arrowDamage(speed float64, power int, crit bool) float32 {
	per := arrowBaseDamage
	if power > 0 {
		per += 0.5*float32(power) + 0.5
	}
	dmg := float32(math.Ceil(speed * float64(per)))
	if crit {
		dmg *= arrowCritScale
	}
	return dmg
}

// arrowHit resolves an arrow that crossed victim's hitbox: game veto,
// damage through armor, Punch knockback, death credited to the shooter.
func (p *projectile) arrowHit(i *Instance, victim *ClientConnection) {
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
	speed := math.Sqrt(p.vx*p.vx + p.vy*p.vy + p.vz*p.vz)
	dmg := victim.absorbDamage(arrowDamage(speed, p.power, p.crit))
	now := i.Tick()
	applied, newHealth, killed := vp.ApplyDamage(dmg, now, i.Combat.InvulnTicks)
	if applied <= 0 {
		return
	}
	s := vp.Snapshot()
	i.Players.Broadcast(CbPlayHurtAnimation, hurtAnimationPayload(s.EntityID, 0), -1)
	i.playSound("minecraft:entity.arrow.hit_player", soundCategoryPlayer, s.X, s.Y, s.Z, 1, 1)
	if p.punch > 0 && speed > 0 {
		k := punchStrength * float64(p.punch)
		i.Players.Broadcast(CbPlayEntityVelocity,
			entityVelocityPayload(s.EntityID, p.vx/speed*k, 0.1, p.vz/speed*k), -1)
	}
	if killed {
		victim.die(shooter)
		return
	}
	_ = victim.sendSetHealth(newHealth)
}
