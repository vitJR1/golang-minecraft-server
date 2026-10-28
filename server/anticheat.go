package server

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/protocol"
)

// anticheat.go wires the anticheat package into the connection: teleport
// confirmations, Ping/Pong transactions, the per-tick position history, the
// packet budget and violation flags. Checks build on these; a check that
// reaches its threshold goes up the punishment ladder (anticheat_punish.go).
// See anticheat.md in the repo root.

// flag records a violation of check on this connection and logs it. detail
// is the human-readable evidence kept for the punishment record. Reaching
// the check's threshold is a detection (anticheat_punish.go).
func (c *ClientConnection) flag(check, detail string) {
	vl := c.ac.Violations.Flag(check, 1, detail, time.Now())
	slog.Warn("anticheat flag",
		"player", c.playerName, "check", check, "vl", fmt.Sprintf("%.1f", vl), "detail", detail)
	if vl >= anticheat.Threshold(check) {
		c.detect(check)
	}
}

// kickFor disconnects the client with reason (shown on its disconnect
// screen) after logging why.
func (c *ClientConnection) kickFor(reason, why string) {
	slog.Warn("kicking", "player", c.playerName, "reason", why)
	_ = c.sendPlayDisconnect(reason)
	go c.cleanup()
}

// allowPacket spends one token of the play-state packet budget. False = the
// client is flooding; it has been kicked and the packet must be dropped.
func (c *ClientConnection) allowPacket() bool {
	if c.ac.Packets.Allow(time.Now()) {
		return true
	}
	c.kickFor("Too many packets", "packet flood")
	return false
}

// acceptChat validates a chat message or command and spends the chat
// budget. Characters a vanilla chat box can't produce ('§' codes, control
// characters) or over-long text are flagged and dropped — they would forge
// coloured or multi-line system lines. Spamming past vanilla's budget (a
// burst of 10, then one a second) is kicked, as vanilla does; ops are
// exempt.
func (c *ClientConnection) acceptChat(text string) bool {
	if !anticheat.ValidChat(text) {
		c.flag("badpackets.chat", fmt.Sprintf("%d chars, illegal characters or too long: %.40q", len([]rune(text)), text))
		return false
	}
	if c.server != nil && c.server.Ops.Has(c.playerName) {
		return true
	}
	if !c.ac.AllowChat(time.Now()) {
		c.kickFor("Kicked for spamming", "chat spam")
		return false
	}
	return true
}

// onTeleportConfirm handles Confirm Teleportation. A forged id is flagged;
// it settles nothing, so the movement gate stays closed.
func (c *ClientConnection) onTeleportConfirm(id int32) {
	tp, ok := c.ac.Teleports.Confirm(id)
	if !ok {
		c.flag("badpackets.teleport", fmt.Sprintf("confirmed unknown teleport id %d", id))
		return
	}
	// A real teleport lands the player somewhere new: the air/fly state
	// starts over there. A setback only puts them back mid-air — resetting
	// on it would let a flying client "land" on every rubber-band.
	if !tp.Setback {
		c.teleported(tp.Y)
	}
	// The vanilla client follows every confirm with an extra position
	// packet in the same tick; it isn't a timer.
	c.ac.Timer.Credit()
}

// awaitingTeleport reports whether a server teleport is still unconfirmed.
// Movement packets sent before the client saw it describe a position the
// server already overrode, so they're dropped (vanilla does the same).
func (c *ClientConnection) awaitingTeleport() bool {
	return c.ac.Teleports.Awaiting()
}

// giveVelocity records a push the server just sent this player (knockback,
// Punch, an explosion) in its velocity budget, and follows it with a Ping:
// the Pong tells when the client applied it (anticheat/velocity.go). Call
// after the velocity packet is queued, so the Ping comes after it.
func (c *ClientConnection) giveVelocity(vx, vy, vz float64) {
	if c == nil || c.player == nil {
		return
	}
	im := c.ac.Velocity.Add(vx, vy, vz)
	c.sendTransaction(func() { c.ac.Velocity.Confirm(im) })
}

// sendTransaction sends a Ping numbered in c.ac.Transactions; onConfirm (may
// be nil) runs on the read loop when the client's Pong proves it processed
// everything sent before it. A client sitting on MaxOutstanding Pings is
// kicked: it answers keep-alives but hides what it has seen.
func (c *ClientConnection) sendTransaction(onConfirm func()) {
	var tick uint64
	if c.instance != nil {
		tick = c.instance.Tick()
	}
	id, ok := c.ac.Transactions.Begin(tick, time.Now(), onConfirm)
	if !ok {
		c.kickFor("Timed out", fmt.Sprintf("%d unanswered transactions", anticheat.MaxOutstanding))
		return
	}
	_ = c.safeWrite(CbPlayPing, protocol.WriteInt(id))
}

// onPong handles Sb Pong. Unknown or out-of-order ids are flagged.
func (c *ClientConnection) onPong(id int32) {
	cb, err := c.ac.Transactions.Confirm(id, time.Now())
	switch {
	case errors.Is(err, anticheat.ErrTransactionOrder):
		c.flag("badpackets.transaction", fmt.Sprintf("pong %d skipped an older ping", id))
	case err != nil:
		c.flag("badpackets.transaction", fmt.Sprintf("pong for unknown ping %d", id))
	case cb != nil:
		cb()
	}
}

// anticheatTick runs once per instance tick: every player's accepted
// position goes into their history (what the other clients were shown this
// tick), and — with Server.TickTransactions — each gets a Ping, numbering
// the tick so a later action can be matched to the state the client saw.
func (i *Instance) anticheatTick(tick uint64) {
	ticking := i.Server != nil && i.Server.TickTransactions
	i.Players.Range(func(c *ClientConnection) {
		if c.player == nil {
			return
		}
		s := c.player.Snapshot()
		c.ac.History.Record(anticheat.Sample{
			Tick: tick, X: s.X, Y: s.Y, Z: s.Z, Sneaking: c.ac.Sneaking.Load(),
		})
		if ticking {
			c.sendTransaction(nil)
		}
	})
}
