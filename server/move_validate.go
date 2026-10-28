package server

import (
	"fmt"
	"log/slog"
	"math"

	"minecraft-server/anticheat"
)

// Movement sanity limits — deliberately generous, they stop garbage and
// teleport hacks. Walking into or through walls is caught by the phase check
// (movement.go); speed and flight are the next stage there.
const (
	// maxMoveSqPerPacket is the squared per-packet travel cap: 10 blocks per
	// movement packet, vanilla's "moved too quickly" threshold. Sprinting,
	// knockback and post-teleport catch-up all stay far below it.
	maxMoveSqPerPacket = 100.0

	// maxWorldXZ / maxWorldY bound absolute coordinates (vanilla's world
	// border sits at ~3.0e7). Positions outside are garbage regardless of
	// travel speed — chunk math and other clients choke on them.
	maxWorldXZ = 3.0e7
	maxWorldY  = 2.0e4
)

// finite64 reports whether every value is a real number — NaN or ±Inf
// coordinates poison entity broadcasts (other clients crash-kick on a
// teleport packet carrying NaN), so they must never reach player state.
func finite64(vals ...float64) bool {
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// finite32 is finite64 for the float32 rotation fields.
func finite32(vals ...float32) bool {
	for _, v := range vals {
		if f := float64(v); math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

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
	dx, dy, dz := x-snap.X, y-snap.Y, z-snap.Z

	outOfBounds := math.Abs(x) > maxWorldXZ || math.Abs(z) > maxWorldXZ || math.Abs(y) > maxWorldY
	tooFast := dx*dx+dy*dy+dz*dz > maxMoveSqPerPacket
	if !outOfBounds && !tooFast {
		from, to := anticheat.Vec{X: snap.X, Y: snap.Y, Z: snap.Z}, anticheat.Vec{X: x, Y: y, Z: z}
		if !c.phased(from, to) {
			c.moved(from, to)
			return true
		}
		// Into or through a full block (movement.go): vanilla's "moved
		// wrongly" — back to where the player was.
		c.flag("movement.phase", fmt.Sprintf("%.2f,%.2f,%.2f → %.2f,%.2f,%.2f", snap.X, snap.Y, snap.Z, x, y, z))
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
