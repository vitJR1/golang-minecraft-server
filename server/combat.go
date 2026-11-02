package server

import (
	"fmt"
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/combat"
	"minecraft-server/server/effect"
	"minecraft-server/server/enchant"
	"minecraft-server/server/sound"
	"minecraft-server/server/tickloop"
	"minecraft-server/world"
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
	if cfg.Version == combat.PvP19 {
		prev := ap.AttackCooldownTick(now)
		strength = combat.AttackStrengthScale(now-prev, combat.CooldownTicks(cfg.AttackSpeed, tickloop.Rate))
	}

	aSnap := ap.Snapshot()
	vSnap := vp.Snapshot()

	// Base damage: the config's flat value, or — with weapon damage on —
	// the held item's vanilla attack damage plus Sharpness (0.5·lvl + 0.5).
	base := cfg.BaseDamage
	if c.instance.WeaponDamage() {
		base = world.AttackDamage(c.heldItemName())
		if lvl := c.heldEnchant(enchant.Sharpness); lvl > 0 {
			base += 0.5*float32(lvl) + 0.5
		}
	}
	damage := combat.ScaledDamage(base, strength)

	// Critical hit: attacker airborne and falling, by the server's own
	// ground test — the packet's onGround flag is the client's word, and a
	// modified client says "airborne" on every swing (1.9 additionally
	// requires a fully charged swing). Vanilla also forbids sprinting, but
	// we don't gate on that to keep it forgiving.
	crit := !c.serverOnGround() && c.ac.Move.LastDY < 0
	if cfg.Version == combat.PvP19 {
		crit = crit && strength > 0.9
	}
	if crit {
		damage *= cfg.CritMultiplier
	}
	damage = victim.absorbDamage(damage) // armor + Protection

	applied, newHealth, killed := vp.ApplyDamage(damage, now, cfg.InvulnTicks)
	if applied <= 0 {
		return // swallowed by i-frames
	}
	// Weapons wear on a landed hit: swords one point, other tools two.
	if t := world.ToolFromItem(c.heldItemName()); t.Type == world.Sword {
		c.damageHeldTool(1)
	} else if t.Type != world.NoTool {
		c.damageHeldTool(2)
	}

	c.applyKnockback(cfg, aSnap, vSnap, victim)

	// Red flash + recoil for everyone, including the victim's own client.
	hurtYaw := combat.YawTo(vSnap.X, vSnap.Z, aSnap.X, aSnap.Z)
	c.instance.Players.Broadcast(packetid.CbPlayHurtAnimation, combat.HurtAnimationPayload(vSnap.EntityID, hurtYaw), -1)

	// Hurt sound at the victim, audible to everyone nearby.
	c.instance.playSound("minecraft:entity.player.hurt", sound.CategoryPlayer, vSnap.X, vSnap.Y, vSnap.Z, 1, 1)

	if crit {
		c.broadcastEntityAnimation(4) // 4 = critical-hit particles on attacker
		c.instance.playSound("minecraft:entity.player.attack.crit", sound.CategoryPlayer, vSnap.X, vSnap.Y, vSnap.Z, 1, 1)
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
func (c *ClientConnection) applyKnockback(cfg combat.Config, aSnap, vSnap player.Snapshot, victim *ClientConnection) {
	strength := cfg.Knockback
	if c.sprintHit() {
		strength += cfg.SprintKnockback
		// The hit ends the sprint, as vanilla's Player.attack does server-
		// side: the next bonus needs a fresh "start sprinting" (a w-tap), so
		// a client that keeps its sprint flag up (KeepSprint) gains nothing.
		c.ac.Sprinting.Store(false)
	}
	strength += 0.5 * float32(c.heldEnchant(enchant.Knockback)) // knockback stick
	vx, vz := combat.KnockbackHoriz(aSnap.X, aSnap.Z, vSnap.X, vSnap.Z, strength)
	c.instance.Players.Broadcast(packetid.CbPlayEntityVelocity,
		combat.EntityVelocityPayload(vSnap.EntityID, vx, float64(cfg.VerticalKnockback), vz), -1)
	victim.giveVelocity(vx, float64(cfg.VerticalKnockback), vz)
}

// sprintHit reports whether c's hit earns the sprint-knockback bonus: the
// client said it sprints, and nothing a vanilla client can't sprint through
// is going on — sneaking, Blindness, eating or drawing a bow.
func (c *ClientConnection) sprintHit() bool {
	return c.ac.Sprinting.Load() && !c.ac.Sneaking.Load() &&
		c.effectLevel(effect.Blindness) == 0 && c.using.Load() == nil
}

// die resolves a lethal blow on c. killer may be nil (environmental death).
// The death sound and the OnPlayerDeath hook fire in both modes. The respawn
// path forks on Combat.InstantRespawn: arenas heal-and-teleport immediately
// (no death screen), otherwise the vanilla death screen is shown and the
// client respawns on click (handled by SbPlayClientCommand → respawn()).
func (c *ClientConnection) die(killer *ClientConnection) {
	c.extinguish()   // death puts the fire out
	c.clearEffects() // …and ends every potion effect
	c.cancelUse()
	s := c.player.Snapshot()
	c.instance.playSound("minecraft:entity.player.death", sound.CategoryPlayer, s.X, s.Y, s.Z, 1, 1)

	if c.instance.CustomRespawn() {
		// The game owns what happens next (spectator, countdown, kit): the
		// player stays Dead — no Combat Death screen, no Set Health 0, no
		// teleport — until the game calls Respawn.
		c.instance.fireDeath(c, killer)
		return
	}

	if c.instance.InstantRespawn() {
		// Heal + teleport to spawn now; no death screen. The hook fires
		// after, so a game can override the spawn (e.g. a random corner).
		c.instantRespawn()
		c.instance.fireDeath(c, killer)
		return
	}

	killerEID := int32(-1)
	msg := fmt.Sprintf("%s died", c.player.Name)
	if killer != nil {
		killerEID = killer.player.EntityID
		msg = fmt.Sprintf("%s was slain by %s", c.player.Name, killer.player.Name)
	}
	_ = c.safeWrite(packetid.CbPlayCombatDeath, combat.DeathPayload(c.player.EntityID, killerEID, msg))
	_ = c.sendSetHealth(0)
	c.instance.BroadcastChat("", msg)

	c.instance.fireDeath(c, killer)
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
	_ = c.sendSyncPlayerPosition(sp.X, sp.Y, sp.Z)
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
