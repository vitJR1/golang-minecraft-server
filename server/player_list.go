package server

import (
	"log/slog"

	"minecraft-server/server/instances/playerlist"
)

// PlayerList tracks the connections of every logged-in player in an
// instance (playerlist.List: lookups, snapshot, Range) and broadcasts to
// them. Stores *ClientConnection (rather than *player.Player) because almost
// every reason to look a player up — broadcasting a packet, kicking them,
// checking their wire state — needs the connection too.
type PlayerList struct {
	playerlist.List[*ClientConnection]
}

func NewPlayerList() *PlayerList { return &PlayerList{} }

// Add registers a connection. Caller must have c.player set already.
// Subsequent Add of the same entity ID overwrites the previous entry.
func (pl *PlayerList) Add(c *ClientConnection) {
	pl.List.Add(c.player.EntityID, c.player.Name, c.player.UUID, c)
}

// snapshot returns a copy of the current connection set.
func (pl *PlayerList) snapshot() []*ClientConnection { return pl.Snapshot() }

// BroadcastNear is Broadcast limited to players within radius blocks of
// (x, y, z) — for positional effects (sounds, explosions) that the client
// would attenuate to nothing anyway, so far players needn't get the packet.
// Players without a position yet are skipped.
func (pl *PlayerList) BroadcastNear(x, y, z, radius float64, packetID int32, payload []byte, exceptEntityID int32) {
	r2 := radius * radius
	for _, c := range pl.snapshot() {
		if c.player == nil || c.player.EntityID == exceptEntityID {
			continue
		}
		s := c.player.Snapshot()
		dx, dy, dz := s.X-x, s.Y-y, s.Z-z
		if dx*dx+dy*dy+dz*dz > r2 {
			continue
		}
		if err := c.safeWrite(packetID, payload); err != nil {
			slog.Warn("broadcast failed",
				"player", c.player.Name, "packet_id", packetID, "err", err)
		}
	}
}

// Broadcast sends a packet to every player. If exceptEntityID >= 0, the
// connection with that entity ID is skipped — typical for "tell everyone
// else what this player just did".
//
// Errors from individual writes are logged but not surfaced; one slow or
// dead client should not block the rest. Each safeWrite has its own
// 5-second write deadline.
func (pl *PlayerList) Broadcast(packetID int32, payload []byte, exceptEntityID int32) {
	for _, c := range pl.snapshot() {
		if c.player.EntityID == exceptEntityID {
			continue
		}
		if err := c.safeWrite(packetID, payload); err != nil {
			slog.Warn("broadcast failed",
				"player", c.player.Name, "packet_id", packetID, "err", err)
		}
	}
}
