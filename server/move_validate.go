package server

import (
	"log/slog"
	"math"
)

// Movement sanity limits. The client is fully trusted about physics
// otherwise (no collision or speed simulation server-side), so these are
// deliberately generous — the goal is stopping garbage and teleport hacks,
// not catching subtle speed cheats.
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
// False positives are possible right after a server-side teleport (the
// client's in-flight packets still reference the old position) — the
// rubber-band converges to the server position either way, so that window
// resolves itself within one round-trip.
func (c *ClientConnection) acceptMove(x, y, z float64) bool {
	snap := c.player.Snapshot()
	dx, dy, dz := x-snap.X, y-snap.Y, z-snap.Z

	outOfBounds := math.Abs(x) > maxWorldXZ || math.Abs(z) > maxWorldXZ || math.Abs(y) > maxWorldY
	tooFast := dx*dx+dy*dy+dz*dz > maxMoveSqPerPacket
	if !outOfBounds && !tooFast {
		return true
	}

	slog.Debug("movement rejected",
		"player", c.playerName,
		"from", [3]float64{snap.X, snap.Y, snap.Z},
		"to", [3]float64{x, y, z},
		"out_of_bounds", outOfBounds)
	_ = c.sendSyncPlayerPosition(snap.X, snap.Y, snap.Z, 1)
	return false
}
