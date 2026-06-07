package server

import (
	"fmt"
	"math"
	"minecraft-server/player"
)

// combat.go implements the PvP model in two flavors selected per instance:
//   - 1.9 (PvP19): attack-cooldown "charge" scaling — a fresh swing deals
//     20% damage, ramping to 100% as the cooldown bar refills.
//   - 1.8 (PvP18): no cooldown — every hit deals full weapon damage, the
//     classic spam-click combat.
// Both share critical hits, knockback (with the sprint "w-tap" bonus),
// per-target invulnerability, death, and respawn. It is core-server
// behavior driven from the SbPlayInteract handler — games observe (and may
// veto) a hit via the OnPlayerAttack hook, but the damage math lives here.

// PvPVersion selects the melee combat model for an instance. The integer
// values match the PVP_VERSION env var (1 = 1.8, 2 = 1.9).
type PvPVersion int

const (
	PvP18 PvPVersion = 1 // 1.8: no attack cooldown, full damage per hit
	PvP19 PvPVersion = 2 // 1.9: attack-cooldown charge scaling
)

// cooldownTicks converts an attack-speed attribute (attacks/second) into the
// tick window the charge bar refills over. Guards a non-positive speed so a
// misconfigured instance can't divide by zero (treated as "instant").
func cooldownTicks(attackSpeed float32) float64 {
	if attackSpeed <= 0 {
		return 0
	}
	return float64(TickRate) / float64(attackSpeed)
}

// attackStrengthScale is vanilla's getAttackStrengthScale(0.5): how charged
// the attack is, in [0,1]. ticksSince is ticks elapsed since the attacker's
// previous swing; the +0.5 matches vanilla's mid-tick sampling. A zero (or
// negative) cooldown means always fully charged.
func attackStrengthScale(ticksSince uint64, cooldown float64) float32 {
	if cooldown <= 0 {
		return 1
	}
	f := (float64(ticksSince) + 0.5) / cooldown
	return float32(max(0, min(1, f)))
}

// scaledDamage applies the 1.9 charge curve: a fully charged hit deals the
// full base, a freshly-spammed hit deals 20%. damage = base*(0.2+0.8*s²).
func scaledDamage(base, strength float32) float32 {
	return base * (0.2 + strength*strength*0.8)
}

// knockbackHoriz returns the horizontal velocity (blocks/tick) to launch the
// victim directly away from the attacker, at the given strength. When the
// two share a column (degenerate direction) it pushes along +Z so the hit
// still registers visibly.
func knockbackHoriz(attackerX, attackerZ, victimX, victimZ float64, strength float32) (vx, vz float64) {
	dx := victimX - attackerX
	dz := victimZ - attackerZ
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		return 0, float64(strength)
	}
	return dx / dist * float64(strength), dz / dist * float64(strength)
}

// yawTo computes the Minecraft yaw (degrees) pointing from (fromX,fromZ)
// toward (toX,toZ). Used for the Hurt Animation recoil direction.
func yawTo(fromX, fromZ, toX, toZ float64) float32 {
	dx := toX - fromX
	dz := toZ - fromZ
	return float32(math.Atan2(-dx, dz) * 180 / math.Pi)
}

// handleAttack runs one melee hit from c against victim. Called from the
// Interact handler after the OnPlayerAttack hook has approved it. No-op when
// combat is disabled or either side is already dead.
func (c *ClientConnection) handleAttack(victim *ClientConnection) {
	if !c.instance.PvPEnabled() {
		return
	}
	cfg := c.instance.Combat
	ap, vp := c.player, victim.player
	if ap == nil || vp == nil || ap.IsDead() || vp.IsDead() {
		return
	}

	now := c.instance.Tick()

	// 1.8 has no attack cooldown: every hit is full strength. 1.9 scales
	// damage by how charged the swing is since the attacker's last hit.
	var strength float32 = 1
	if cfg.Version == PvP19 {
		prev := ap.AttackCooldownTick(now)
		strength = attackStrengthScale(now-prev, cooldownTicks(cfg.AttackSpeed))
	}

	aSnap := ap.Snapshot()
	vSnap := vp.Snapshot()

	damage := scaledDamage(cfg.BaseDamage, strength)

	// Critical hit: attacker airborne (1.9 additionally requires a fully
	// charged swing). Vanilla also forbids sprinting, but we don't gate on
	// that to keep it forgiving.
	crit := !aSnap.OnGround
	if cfg.Version == PvP19 {
		crit = crit && strength > 0.9
	}
	if crit {
		damage *= cfg.CritMultiplier
	}

	applied, newHealth, killed := vp.ApplyDamage(damage, now, cfg.InvulnTicks)
	if applied <= 0 {
		return // swallowed by i-frames
	}

	c.applyKnockback(cfg, aSnap, vSnap)

	// Red flash + recoil for everyone, including the victim's own client.
	hurtYaw := yawTo(vSnap.X, vSnap.Z, aSnap.X, aSnap.Z)
	c.instance.Players.Broadcast(CbPlayHurtAnimation, hurtAnimationPayload(vSnap.EntityID, hurtYaw), -1)

	// Hurt sound at the victim, audible to everyone nearby.
	c.instance.Players.Broadcast(CbPlaySoundEffect,
		soundEffectPayload("minecraft:entity.player.hurt", soundCategoryPlayer, vSnap.X, vSnap.Y, vSnap.Z, 1, 1), -1)

	if crit {
		c.broadcastEntityAnimation(4) // 4 = critical-hit particles on attacker
		c.instance.Players.Broadcast(CbPlaySoundEffect,
			soundEffectPayload("minecraft:entity.player.attack.crit", soundCategoryPlayer, vSnap.X, vSnap.Y, vSnap.Z, 1, 1), -1)
	}

	if killed {
		// On a lethal blow we deliberately skip the Set Health(0) here —
		// die() owns the death presentation (death screen vs instant heal),
		// and sending 0 first would flash the death screen even in instant
		// mode (the client opens it whenever its health hits 0).
		victim.die(c)
	} else {
		_ = victim.sendSetHealth(newHealth)
	}
}

// applyKnockback launches the victim away from the attacker, broadcasting
// Set Entity Velocity to everyone (the victim's own client included, so its
// movement actually changes). Sprinting attackers add the w-tap bonus.
func (c *ClientConnection) applyKnockback(cfg CombatConfig, aSnap, vSnap player.Snapshot) {
	strength := cfg.Knockback
	if c.sprinting.Load() {
		strength += cfg.SprintKnockback
	}
	vx, vz := knockbackHoriz(aSnap.X, aSnap.Z, vSnap.X, vSnap.Z, strength)
	c.instance.Players.Broadcast(CbPlayEntityVelocity,
		entityVelocityPayload(vSnap.EntityID, vx, float64(cfg.VerticalKnockback), vz), -1)
}

// die resolves a lethal blow on c. killer may be nil (environmental death).
// The death sound and the OnPlayerDeath hook fire in both modes. The respawn
// path forks on Combat.InstantRespawn: arenas heal-and-teleport immediately
// (no death screen), otherwise the vanilla death screen is shown and the
// client respawns on click (handled by SbPlayClientCommand → respawn()).
func (c *ClientConnection) die(killer *ClientConnection) {
	s := c.player.Snapshot()
	c.instance.Players.Broadcast(CbPlaySoundEffect,
		soundEffectPayload("minecraft:entity.player.death", soundCategoryPlayer, s.X, s.Y, s.Z, 1, 1), -1)

	if c.instance.InstantRespawn() {
		// Heal + teleport to spawn now; no death screen. The hook fires
		// after, so a game can override the spawn (e.g. a random corner).
		c.instantRespawn()
		if hook := c.instance.OnPlayerDeath; hook != nil {
			safeHook(c.instance, "OnPlayerDeath", func() { hook(c, killer) })
		}
		return
	}

	killerEID := int32(-1)
	msg := fmt.Sprintf("%s died", c.player.Name)
	if killer != nil {
		killerEID = killer.player.EntityID
		msg = fmt.Sprintf("%s was slain by %s", c.player.Name, killer.player.Name)
	}
	_ = c.safeWrite(CbPlayCombatDeath, combatDeathPayload(c.player.EntityID, killerEID, msg))
	_ = c.sendSetHealth(0)
	c.instance.BroadcastChat("", msg)

	if hook := c.instance.OnPlayerDeath; hook != nil {
		safeHook(c.instance, "OnPlayerDeath", func() { hook(c, killer) })
	}
}

// instantRespawn heals the player to full and teleports them to the instance
// spawn point without the death screen. Unlike respawn(), it doesn't send a
// Respawn packet — the client's world is intact (the player never left), so
// a Set Health + position sync is all it takes.
func (c *ClientConnection) instantRespawn() {
	sp := c.instance.SpawnPoint
	c.player.Respawn()
	c.player.MoveTo(sp.X, sp.Y, sp.Z, false)
	_ = c.sendSetHealth(player.MaxHealth)
	_ = c.sendSyncPlayerPosition(sp.X, sp.Y, sp.Z, 1)
	c.broadcastEntityTeleport()
}

// respawn brings c back at the instance's spawn point with full health.
// Triggered by the client's Client Command (perform respawn) after it shows
// the death screen. Mirrors the world-resync half of Server.MovePlayer but
// stays in the same instance: the player never left the player list, so we
// only rebuild what the client's Respawn packet wiped (its world + the other
// entities) and re-place this player for everyone else.
func (c *ClientConnection) respawn() error {
	if c.player == nil {
		return nil
	}
	sp := c.instance.SpawnPoint
	c.player.Respawn() // full health, clear dead flag
	c.player.MoveTo(sp.X, sp.Y, sp.Z, false)
	// resyncView rebuilds the whole client view from the (now reset) player
	// state + current instance, sending the restored hearts as part of it.
	return c.resyncView()
}

// combatTick runs natural health regeneration once per RegenIntervalTicks:
// every alive, below-max player heals one half-heart. Registered on every
// instance in NewInstance; cheap and a no-op when combat or regen is off.
func (i *Instance) combatTick(tick uint64) {
	cfg := i.Combat
	if !i.PvPEnabled() || cfg.RegenIntervalTicks == 0 || tick%cfg.RegenIntervalTicks != 0 {
		return
	}
	i.Players.Range(func(c *ClientConnection) {
		p := c.player
		if p == nil || p.IsDead() {
			return
		}
		h := p.Health()
		if h <= 0 || h >= player.MaxHealth {
			return
		}
		p.SetHealth(h + 1)
		_ = c.sendSetHealth(p.Health())
	})
}
