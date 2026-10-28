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
// packet budget and violation flags. Checks build on these; the reaction to
// a flag (kick / ban ladder) is a later layer — for now flags are logged.
// See anticheat.md in the repo root.

// flag records a violation of check on this connection and logs it. detail
// is the human-readable evidence kept for the punishment record.
func (c *ClientConnection) flag(check, detail string) {
	vl := c.ac.Violations.Flag(check, 1, detail, time.Now())
	slog.Warn("anticheat flag",
		"player", c.playerName, "check", check, "vl", fmt.Sprintf("%.1f", vl), "detail", detail)
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

// onTeleportConfirm handles Confirm Teleportation. A forged id is flagged;
// it settles nothing, so the movement gate stays closed.
func (c *ClientConnection) onTeleportConfirm(id int32) {
	if _, ok := c.ac.Teleports.Confirm(id); !ok {
		c.flag("badpackets.teleport", fmt.Sprintf("confirmed unknown teleport id %d", id))
	}
}

// awaitingTeleport reports whether a server teleport is still unconfirmed.
// Movement packets sent before the client saw it describe a position the
// server already overrode, so they're dropped (vanilla does the same).
func (c *ClientConnection) awaitingTeleport() bool {
	return c.ac.Teleports.Awaiting()
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
