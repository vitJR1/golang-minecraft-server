package server

import (
	"fmt"
	"log/slog"

	"minecraft-server/anticheat"
)

// kickInvalidMove disconnects a client whose movement packet carried
// NaN/Inf values. That never happens with a vanilla client — no point
// rubber-banding, it's a hacked client or a fuzzer.
func (c *ClientConnection) kickInvalidMove() {
	slog.Warn("kicking: invalid movement packet (NaN/Inf)",
		"player", c.playerName)
	_ = c.sendPlayDisconnect("Invalid move player packet received")
	go c.cleanup()
}

// acceptMove validates a movement packet's target position against the
// player's current server-side position. True = apply the move. False =
// the packet was rejected and a corrective Synchronize Player Position was
// sent, snapping the client back (vanilla's "rubber-band"); the caller
// must NOT apply the move.
//
// Packets sent before the client confirmed a server teleport never get here
// (the handler drops them while c.awaitingTeleport()), so the old position
// in flight can't trip this check.
func (c *ClientConnection) acceptMove(x, y, z float64) bool {
	snap := c.player.Snapshot()
	from, to := anticheat.Vec{X: snap.X, Y: snap.Y, Z: snap.Z}, anticheat.Vec{X: x, Y: y, Z: z}
	outOfBounds, tooFast := anticheat.MoveSane(from, to)
	if !outOfBounds && !tooFast {
		check, detail := "", ""
		if c.phased(from, to) {
			// Into or through a full block (anticheat.Phased): vanilla's
			// "moved wrongly".
			check, detail = "movement.phase", fmt.Sprintf("%.2f,%.2f,%.2f → %.2f,%.2f,%.2f", snap.X, snap.Y, snap.Z, x, y, z)
		} else {
			check, detail = c.checkMotion(from, to) // speed, flight
		}
		if check == "" {
			c.ac.Move.Moved(from, to)
			return true
		}
		// Back to where the player was.
		c.flag(check, detail)
		_ = c.sendSetback(snap.X, snap.Y, snap.Z)
		return false
	}

	slog.Debug("movement rejected",
		"player", c.playerName,
		"from", [3]float64{snap.X, snap.Y, snap.Z},
		"to", [3]float64{x, y, z},
		"out_of_bounds", outOfBounds)
	_ = c.sendSetback(snap.X, snap.Y, snap.Z)
	return false
}

// acceptLook validates a rotation packet's pitch: the vanilla client clamps
// it to ±90°, so anything beyond is a modified client (derp / spin modules).
// The packet is dropped and flagged; yaw is free to wrap.
func (c *ClientConnection) acceptLook(pitch float32) bool {
	if anticheat.ValidPitch(pitch) {
		return true
	}
	c.flag("badpackets.pitch", fmt.Sprintf("pitch %.1f", pitch))
	return false
}
