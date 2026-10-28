package server

import (
	"fmt"
	"strings"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/world"
)

// reach.go holds the gamemode gates and interaction-range checks every
// world action goes through (anticheat.md #7, #8). Ranges are vanilla
// 1.20.1's own; geometry lives in anticheat/reach.go.

// mayAct reports whether the player can act on the world at all: attack,
// use items, click blocks and entities. Dead players and spectators can't.
// A vanilla spectator's left click still sends an attack (to spectate), so
// refusals here are silent, not flagged.
func (c *ClientConnection) mayAct() bool {
	return c.player != nil && !c.player.IsDead() && c.gamemode() != player.Spectator
}

// mayBuild reports whether the player can break or place blocks: survival
// or creative, alive. The vanilla adventure client never sends digs or
// placements (no CanDestroy/CanPlaceOn modelled), so only a modified
// client gets refused here.
func (c *ClientConnection) mayBuild() bool {
	gm := c.gamemode()
	return c.mayAct() && (gm == player.Survival || gm == player.Creative)
}

// feet / eye are the player's server-side position and eye point.
func (c *ClientConnection) feet() anticheat.Vec {
	s := c.player.Snapshot()
	return anticheat.Vec{X: s.X, Y: s.Y, Z: s.Z}
}

func (c *ClientConnection) eye() anticheat.Vec {
	return anticheat.Eye(c.feet(), c.ac.Sneaking.Load())
}

// digInReach: vanilla refuses a dig whose block centre is more than
// MaxDigReach from the eyes. Out of reach is flagged.
func (c *ClientConnection) digInReach(pos world.Position) bool {
	d := anticheat.Dist(c.eye(), anticheat.BlockCentre(pos.X, pos.Y, pos.Z))
	if d <= anticheat.MaxDigReach {
		return true
	}
	c.flag("reach.block", fmt.Sprintf("dig %v at %.2f > %.1f", pos, d, anticheat.MaxDigReach))
	return false
}

// useInReach: Use Item On Block is honoured while the block centre is
// within MaxUseReach of the feet. Out of reach is flagged.
func (c *ClientConnection) useInReach(pos world.Position) bool {
	d := anticheat.Dist(c.feet(), anticheat.BlockCentre(pos.X, pos.Y, pos.Z))
	if d < anticheat.MaxUseReach {
		return true
	}
	c.flag("reach.block", fmt.Sprintf("use on %v at %.2f ≥ %.1f", pos, d, anticheat.MaxUseReach))
	return false
}

// entityBox approximates a world entity's hitbox: an item frame is a small
// box around its centre, anything else (villager NPCs) a standing mob.
func entityBox(e world.Entity) anticheat.Box {
	if strings.HasSuffix(e.Type, "item_frame") {
		c := anticheat.Vec{X: e.X, Y: e.Y, Z: e.Z}
		return anticheat.Box{Min: c, Max: c}.Expand(0.375)
	}
	return anticheat.FeetBox(anticheat.Vec{X: e.X, Y: e.Y, Z: e.Z}, 0.6, 1.95)
}

// entityInReach: Interact on a world entity is honoured while the eyes are
// within MaxEntityReach of its hitbox. Out of reach is flagged.
func (c *ClientConnection) entityInReach(e world.Entity) bool {
	d := entityBox(e).DistanceTo(c.eye())
	if d <= anticheat.MaxEntityReach {
		return true
	}
	c.flag("reach.entity", fmt.Sprintf("%s at %.2f > %.1f", e.Type, d, anticheat.MaxEntityReach))
	return false
}

// victimSamples are the positions c's client may have shown victim at: its
// history from InterpolationTicks before c's newest Pong-confirmed tick (the
// last second without transactions), plus where it is now.
func (c *ClientConnection) victimSamples(victim *ClientConnection) []anticheat.Sample {
	now := c.instance.Tick()
	confirmed, ok := c.ac.Transactions.LastConfirmedTick()
	samples := victim.ac.History.Since(anticheat.SeenFrom(now, confirmed, ok))
	vs := victim.player.Snapshot()
	return append(samples, anticheat.Sample{Tick: now, X: vs.X, Y: vs.Y, Z: vs.Z, Sneaking: victim.ac.Sneaking.Load()})
}

// attackInReach is the lag-compensated melee reach check. The victim may be
// anywhere the attacker's client could have shown it: from InterpolationTicks
// before the newest tick the attacker confirmed with a Pong up to now (or the
// last second without transactions), plus where it is right now. The closest
// of those hitboxes must be within the client's own reach — 3 blocks, 6 in
// creative. A hit beyond it is cancelled and flagged.
func (c *ClientConnection) attackInReach(victim *ClientConnection) bool {
	limit := anticheat.AttackReach
	if c.gamemode() == player.Creative {
		limit = anticheat.CreativeAttackReach
	}
	samples := c.victimSamples(victim)
	d, _ := anticheat.MinReach(c.eye(), samples)
	if d <= limit {
		return true
	}
	c.flag("combat.reach", fmt.Sprintf("hit %s at %.2f > %.1f (%d positions)", victim.playerName, d, limit, len(samples)))
	return false
}
