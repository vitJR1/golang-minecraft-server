package server

import (
	"fmt"
	"math"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/world"
)

// actions.go: heuristics on world and combat actions (anticheat.md #10–#13)
// — fastbreak / nuker, fastplace / scaffold, killaura / autoclicker,
// anti-knockback. Each refused action is rolled back by the caller.

const (
	// digAngleMax: a block being dug is in front of the player — within
	// this angle of the view from the eyes to its centre — unless it's so
	// close (digAngleNear) that the crosshair may be on a far corner.
	digAngleMax  = 60.0
	digAngleNear = 2.0
	// faceSlack: how far the eye may sit behind a clicked face's plane.
	faceSlack = 0.1
	// aimMargin grows the victim's hitbox for the aim test: the crosshair
	// moves every frame, the server sees the rotation once a tick.
	aimMargin = 1.0
	// knockMoves: moves after a confirmed push before judging it.
	knockMoves = 5
	// knockMinH / knockMinUp: pushes smaller than these aren't judged.
	knockMinH  = 0.2
	knockMinUp = 0.25
	// knockShare: the least share of the push a client that applied it
	// shows — even walking straight back into it.
	knockShareH  = 0.3
	knockShareUp = 0.2
)

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
	centre := anticheat.BlockCentre(pos.X, pos.Y, pos.Z)
	eyes, looks := c.poses()
	best := 180.0
	for _, e := range eyes {
		if anticheat.Dist(e, centre) < digAngleNear {
			return true
		}
		for _, l := range looks {
			best = math.Min(best, anticheat.AngleTo(e, l, centre))
		}
	}
	if best <= digAngleMax {
		return true
	}
	c.flag("world.angle", fmt.Sprintf("dig at %v %.0f° off the view", pos, best))
	return false
}

// --- #11 fastplace / scaffold ---------------------------------------------

// cursorValid: the click's position inside the block is in [0, 1] on every
// axis (vanilla refuses anything else).
func cursorValid(x, y, z float32) bool {
	ok := func(v float32) bool { return v >= -0.0001 && v <= 1.0001 }
	return ok(x) && ok(y) && ok(z)
}

// placeAllowed checks a placement against the clicked block's face: within
// the place budget, and the face turned toward the eyes — the client can
// only click a face it sees; scaffold modules place against hidden ones.
func (c *ClientConnection) placeAllowed(clicked world.Position, face int) bool {
	if !c.ac.AllowPlace(time.Now()) {
		c.flag("world.fastplace", fmt.Sprintf("place at %v over %.0f/s", clicked, anticheat.PlaceRate))
		return false
	}
	eyes, _ := c.poses()
	for _, e := range eyes {
		if anticheat.FaceVisible(e, clicked.X, clicked.Y, clicked.Z, face, faceSlack) {
			return true
		}
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
	eyes, looks := c.poses()
	for _, s := range c.victimSamples(victim) {
		box := anticheat.PlayerBox(s).Expand(aimMargin)
		for _, e := range eyes {
			for _, l := range looks {
				if anticheat.RayHitsBox(e, l, box, anticheat.CreativeAttackReach+aimMargin) {
					return true
				}
			}
		}
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
	if im.H < knockMinH && im.Up < knockMinUp {
		return
	}
	c.ac.Move.Knock = anticheat.KnockCheck{
		Active: true, DirX: im.DirX, DirZ: im.DirZ, H: im.H, Up: im.Up, Start: c.feet(),
	}
}

// checkKnock follows the push under verification over the next moves and
// returns evidence of anti-knockback, or "". A wall in the push's way or a
// ceiling over the head excuses a short move; so does a fluid or cobweb.
func (c *ClientConnection) checkKnock(to anticheat.Vec, exempt bool) string {
	k := &c.ac.Move.Knock
	if !k.Active {
		return ""
	}
	if exempt {
		k.Active = false
		return ""
	}
	k.Moves++
	k.BestH = math.Max(k.BestH, (to.X-k.Start.X)*k.DirX+(to.Z-k.Start.Z)*k.DirZ)
	k.BestUp = math.Max(k.BestUp, to.Y-k.Start.Y)
	if k.Moves < knockMoves {
		return ""
	}
	k.Active = false
	inst := c.instance
	sneak := c.ac.Sneaking.Load()
	var weak []string
	if k.H >= knockMinH && k.BestH < knockShareH*k.H {
		ahead := anticheat.Vec{X: k.Start.X + k.DirX*0.6, Y: k.Start.Y, Z: k.Start.Z + k.DirZ*0.6}
		if !inst.insideWall(playerBoxAt(ahead, sneak, boxEpsilon)) {
			weak = append(weak, fmt.Sprintf("moved %.2f along a %.2f push", k.BestH, k.H))
		}
	}
	if k.Up >= knockMinUp && k.BestUp < knockShareUp*anticheat.Apex(k.Up) {
		above := anticheat.Vec{X: k.Start.X, Y: k.Start.Y + 0.6, Z: k.Start.Z}
		if !inst.insideWall(playerBoxAt(above, sneak, boxEpsilon)) {
			weak = append(weak, fmt.Sprintf("rose %.2f on a %.2f lift", k.BestUp, k.Up))
		}
	}
	if len(weak) == 0 {
		return ""
	}
	return fmt.Sprint(weak)
}
