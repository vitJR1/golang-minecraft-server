package server

import (
	"fmt"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/world"
)

// actions.go: heuristics on world and combat actions (anticheat.md #10–#13)
// — fastbreak / nuker, fastplace / scaffold, killaura / autoclicker,
// anti-knockback. Each refused action is rolled back by the caller.

// poses returns the eye positions and view vectors the client may have
// acted from: the current pose and the one before the latest packet.
func (c *ClientConnection) poses() (eyes, looks []anticheat.Vec) {
	s := c.player.Snapshot()
	sneak := c.ac.Sneaking.Load()
	m := &c.ac.Move
	eyes = []anticheat.Vec{anticheat.Eye(anticheat.Vec{X: s.X, Y: s.Y, Z: s.Z}, sneak)}
	if m.HasPrev {
		eyes = append(eyes, anticheat.Eye(m.PrevFeet, sneak))
	}
	looks = []anticheat.Vec{anticheat.Look(s.Yaw, s.Pitch), anticheat.Look(m.PrevYaw, m.PrevPitch)}
	return eyes, looks
}

// rememberLook keeps the rotation before a look change (handler, before
// the new rotation is applied).
func (c *ClientConnection) rememberLook() {
	s := c.player.Snapshot()
	c.ac.Move.PrevYaw, c.ac.Move.PrevPitch = s.Yaw, s.Pitch
}

// --- #10 fastbreak / nuker ------------------------------------------------

// breakAllowed checks a survival dig action (start or finish): within the
// break budget, and aimed at the block. Creative digs aren't limited.
func (c *ClientConnection) breakAllowed(pos world.Position) bool {
	if c.gamemode() == player.Creative {
		return true
	}
	if !c.ac.AllowBreak(time.Now()) {
		c.flag("world.fastbreak", fmt.Sprintf("dig at %v over %.0f/s", pos, anticheat.BreakRate))
		return false
	}
	eyes, looks := c.poses()
	ok, angle := anticheat.DigAimed(eyes, looks, anticheat.BlockCentre(pos.X, pos.Y, pos.Z))
	if ok {
		return true
	}
	c.flag("world.angle", fmt.Sprintf("dig at %v %.0f° off the view", pos, angle))
	return false
}

// --- #11 fastplace / scaffold ---------------------------------------------

// placeAllowed checks a placement against the clicked block's face: within
// the place budget, and the face turned toward the eyes — the client can
// only click a face it sees; scaffold modules place against hidden ones.
func (c *ClientConnection) placeAllowed(clicked world.Position, face int) bool {
	if !c.ac.AllowPlace(time.Now()) {
		c.flag("world.fastplace", fmt.Sprintf("place at %v over %.0f/s", clicked, anticheat.PlaceRate))
		return false
	}
	if eyes, _ := c.poses(); anticheat.FaceSeen(eyes, clicked.X, clicked.Y, clicked.Z, face) {
		return true
	}
	c.flag("world.scaffold", fmt.Sprintf("placed against hidden face %d of %v", face, clicked))
	return false
}

// --- #12 killaura / autoclicker ---------------------------------------------

// attackAllowed checks a melee hit: within the attack budget, the previous
// hit was followed by its arm swing, and the view ray passes through the
// victim (any of the hitboxes the reach check accepted).
func (c *ClientConnection) attackAllowed(victim *ClientConnection) bool {
	if c.ac.Move.SwingDue {
		c.flag("combat.noswing", "attacked again without swinging")
	}
	c.ac.Move.SwingDue = true
	if !c.ac.AllowAttack(time.Now()) {
		c.flag("combat.cps", fmt.Sprintf("attack over %.0f/s", anticheat.AttackRate))
		return false
	}
	if eyes, looks := c.poses(); anticheat.Aimed(eyes, looks, c.victimSamples(victim)) {
		return true
	}
	c.flag("combat.aim", fmt.Sprintf("hit %s outside the view", victim.playerName))
	return false
}

// swung records an arm swing: it settles the attack waiting for one.
func (c *ClientConnection) swung() { c.ac.Move.SwingDue = false }

// tickEnded runs on each movement packet — the end of a client tick: an
// attack still without its swing came from a client that doesn't swing.
func (c *ClientConnection) tickEnded() {
	if c.ac.Move.SwingDue {
		c.ac.Move.SwingDue = false
		c.flag("combat.noswing", "attack without an arm swing")
	}
}

// --- #13 anti-knockback -------------------------------------------------------

// pushApplied starts verifying im once the client confirmed it (Pong).
func (c *ClientConnection) pushApplied(im *anticheat.Impulse) {
	if k, ok := anticheat.StartKnock(im, c.feet()); ok {
		c.ac.Move.Knock = k
	}
}

// checkKnock follows the push under verification over the next moves
// (KnockCheck.Follow) and returns evidence of anti-knockback, or "".
func (c *ClientConnection) checkKnock(to anticheat.Vec, exempt bool) string {
	at, sneak := c.instance.blocks(), c.ac.Sneaking.Load()
	return c.ac.Move.Knock.Follow(to, exempt, func(feet anticheat.Vec) bool {
		return anticheat.Blocked(at, feet, sneak)
	})
}
