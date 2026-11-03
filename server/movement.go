package server

import (
	"fmt"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/server/gameplay/effect"
)

// movement.go: server-side movement checks (anticheat.md #9) — the timer,
// phase (anticheat.Phased), the server's own idea of "on the ground"
// (anticheat.OnGround), and speed / flight (MoveState.Judge). The checks
// live in the anticheat package; this file feeds them the instance's blocks
// and the player's state, and flags what they find.

// countMove feeds one movement packet to the timer check.
func (c *ClientConnection) countMove() {
	c.tickEnded() // a movement packet closes the client's tick (actions.go)
	if ahead, flagged := c.ac.Timer.Move(time.Now()); flagged {
		c.flag("movement.timer", fmt.Sprintf("%d ms ahead of real time", ahead.Milliseconds()))
	}
}

// blocks is the instance's block reader for the anticheat checks.
func (i *Instance) blocks() anticheat.BlockAt { return i.World.GetBlock }

// phased reports whether moving from `from` to `to` takes the player into or
// through a full block (anticheat.Phased). A spectator is left alone.
func (c *ClientConnection) phased(from, to anticheat.Vec) bool {
	if c.gamemode() == player.Spectator {
		return false
	}
	return anticheat.Phased(c.instance.blocks(), from, to, c.ac.Sneaking.Load())
}

// serverOnGround is the server's ground state, worked out from the blocks
// under the player's position, for gameplay that used to trust the packet's
// onGround flag — a free "critical hit on every swing" switch for a
// modified client (critical hits, the airborne dig penalty).
func (c *ClientConnection) serverOnGround() bool {
	if c.instance == nil || c.player == nil {
		return true
	}
	return anticheat.OnGround(c.instance.blocks(), c.feet())
}

// checkMotion is the speed and flight check for an accepted (non-phasing)
// move from → to. It returns the violated check and its evidence, or "".
// Creative and spectator players fly legitimately and are skipped, as are
// players with /fly (fly.go).
func (c *ClientConnection) checkMotion(from, to anticheat.Vec) (check, detail string) {
	m := &c.ac.Move
	gm := c.gamemode()
	if gm == player.Creative || gm == player.Spectator || c.player.IsDead() || c.flightExempt() {
		m.Landed(to.Y)
		m.Knock.Active = false
		return "", ""
	}
	at := c.instance.blocks()
	ground := anticheat.OnGround(at, to)
	defer c.ac.Velocity.Moved(ground)

	m.NoteSurface(anticheat.BlocksUnder(at, to))
	pushH, pushUp := c.ac.Velocity.Allowance()
	exempt := anticheat.MotionExempt(at, to, c.ac.Sneaking.Load())
	if ev := c.checkKnock(to, exempt); ev != "" {
		// Flag only: the push is the client's to apply, there's nothing to
		// roll back.
		c.flag("combat.velocity", ev)
	}
	return m.Judge(anticheat.Motion{
		From: from, To: to, Ground: ground, Exempt: exempt,
		SpeedLevel: c.effectLevel(effect.Speed), JumpLevel: c.effectLevel(effect.JumpBoost),
		PushH: pushH, PushUp: pushUp,
	})
}
