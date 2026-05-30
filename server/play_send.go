package server

import (
	"bytes"
	"minecraft-server/chunk"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// sendLoginPlay writes the clientbound Login (Play) packet (0x28) for
// protocol 763 (1.20.1). Field order matches wiki.vg.
func (c *ClientConnection) sendLoginPlay() error {
	var buf bytes.Buffer

	p := c.player.Snapshot()

	// Entity ID
	buf.Write(protocol.WriteInt(p.EntityID))
	// Is hardcore
	buf.WriteByte(0)
	// Gamemode (0=Survival, 1=Creative, 2=Adventure, 3=Spectator)
	buf.WriteByte(byte(p.Gamemode))
	// Previous gamemode (-1 = none)
	buf.WriteByte(0xFF)

	// Dimension count + names
	protocol.WriteVarInt32ToBuffer(&buf, 1)
	buf.Write(protocol.WriteString("minecraft:overworld"))

	// Registry codec (NBT, already-encoded bytes — no length prefix)
	buf.Write(RegistryCodec())

	// Current dimension type + name
	buf.Write(protocol.WriteString("minecraft:overworld"))
	buf.Write(protocol.WriteString("minecraft:overworld"))

	// Hashed seed
	buf.Write(protocol.WriteLong(0))
	// Max players (ignored by vanilla client, still required)
	protocol.WriteVarInt32ToBuffer(&buf, 20)
	// View distance + simulation distance (chunks)
	protocol.WriteVarInt32ToBuffer(&buf, 10)
	protocol.WriteVarInt32ToBuffer(&buf, 10)
	// Reduced debug info
	buf.WriteByte(0)
	// Enable respawn screen
	buf.WriteByte(1)
	// Is debug
	buf.WriteByte(0)
	// Is flat
	buf.WriteByte(0)
	// Has death location (no)
	buf.WriteByte(0)
	// Portal cooldown (1.20+)
	protocol.WriteVarInt32ToBuffer(&buf, 0)

	return c.safeWrite(CbPlayLogin, buf.Bytes())
}

// sendSyncPlayerPosition writes Synchronize Player Position (0x3C). The
// teleportID echoes back in the next Confirm Teleportation packet.
func (c *ClientConnection) sendSyncPlayerPosition(x, y, z float64, teleportID int32) error {
	var buf bytes.Buffer
	buf.Write(protocol.WriteDouble(x))
	buf.Write(protocol.WriteDouble(y))
	buf.Write(protocol.WriteDouble(z))
	buf.Write(protocol.WriteFloat(0)) // yaw
	buf.Write(protocol.WriteFloat(0)) // pitch
	buf.WriteByte(0)                  // flags (all absolute)
	protocol.WriteVarInt32ToBuffer(&buf, teleportID)
	return c.safeWrite(CbPlaySyncPos, buf.Bytes())
}

// sendAckBlockChange echoes the client's block-change sequence number back
// to confirm its prediction. Without this the client visually rolls back
// the place/break it just performed.
func (c *ClientConnection) sendAckBlockChange(sequence int32) error {
	return c.safeWrite(CbPlayAckBlockChange, protocol.WriteVarInt32(sequence))
}

// sendRespawn drives a dimension swap on the client: clears its world,
// despawns entities, and switches the player's reference frame. Server
// follows up with chunks, Sync Position, and (via JoinAndAnnounce) the
// fresh tab list + Spawn Player packets.
//
// We use it for cross-instance teleport even when source and destination
// are nominally the same dimension type — the client only knows that its
// world got reset, which is what we need.
func (c *ClientConnection) sendRespawn() error {
	gm := c.player.Snapshot().Gamemode

	var buf bytes.Buffer
	buf.Write(protocol.WriteString("minecraft:overworld")) // dimension type
	buf.Write(protocol.WriteString("minecraft:overworld")) // dimension name
	buf.Write(protocol.WriteLong(0))                       // hashed seed
	buf.WriteByte(byte(gm))                                // current game mode
	buf.WriteByte(0xFF)                                    // previous game mode = -1 (none)
	buf.WriteByte(0)                                       // is debug
	buf.WriteByte(0)                                       // is flat
	buf.WriteByte(0x03)                                    // copy metadata: keep status + equipment
	buf.WriteByte(0)                                       // has death location = false
	protocol.WriteVarInt32ToBuffer(&buf, 0)                // portal cooldown
	return c.safeWrite(CbPlayRespawn, buf.Bytes())
}

// sendBlockUpdate writes Block Update (0x09): a single block change at the
// given position to the given block state.
func (c *ClientConnection) sendBlockUpdate(p world.Position, b world.Block) error {
	var buf bytes.Buffer
	buf.Write(protocol.WritePosition(p.X, p.Y, p.Z))
	protocol.WriteVarInt32ToBuffer(&buf, b.StateID)
	return c.safeWrite(CbPlayBlockUpdate, buf.Bytes())
}

// sendCurrentWorldState replays every non-Air block in the world to this
// client as Block Update packets. Used at login until we have a real chunk
// streamer that bakes blocks into chunk-data palettes.
func (c *ClientConnection) sendCurrentWorldState() error {
	var firstErr error
	c.instance.World.Range(func(p world.Position, b world.Block) {
		if firstErr != nil {
			return
		}
		if err := c.sendBlockUpdate(p, b); err != nil {
			firstErr = err
		}
	})
	return firstErr
}

// HubChunkRadius controls how many empty chunks are streamed around the
// spawn point. The square covers chunks [-R .. R-1] on both axes — so
// R=2 sends 4×4 = 16 chunks total, enough for the 1×1-chunk schematic
// at the origin plus a ring of loaded terrain around it (no void edges
// in the client's view).
const HubChunkRadius int32 = 2

// sendInitialChunks streams a 2R × 2R square of empty chunks centred on
// world (0, 0). Used at login and after cross-instance teleport so the
// client has loaded terrain to render before sendCurrentWorldState
// replays the actual blocks via Block Update packets.
func (c *ClientConnection) sendInitialChunks() error {
	for cx := -HubChunkRadius; cx < HubChunkRadius; cx++ {
		for cz := -HubChunkRadius; cz < HubChunkRadius; cz++ {
			if err := c.sendChunkData(cx, cz); err != nil {
				return err
			}
		}
	}
	return nil
}

// sendChunkData writes the Chunk Data and Update Light packet (0x24) for an
// empty chunk. All sections are air, all light masks are empty (the client
// fills in default full-bright light for missing data, which is acceptable
// for an Overworld empty chunk).
func (c *ClientConnection) sendChunkData(chunkX, chunkZ int32) error {
	var buf bytes.Buffer

	buf.Write(protocol.WriteInt(chunkX))
	buf.Write(protocol.WriteInt(chunkZ))

	// Heightmaps NBT (root Compound, no length prefix).
	buf.Write(chunk.BuildEmptyHeightmaps())

	// Chunk data (paletted sections) with VarInt size prefix.
	data := chunk.BuildEmptyChunkData()
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(data)))
	buf.Write(data)

	// Block entities count
	protocol.WriteVarInt32ToBuffer(&buf, 0)

	// Light masks — empty (no sections have transmitted light arrays).
	// BitSet on the wire: VarInt(long count) followed by long(s). We send a
	// zero-length BitSet for each mask, which the client treats as all-zero.
	for i := 0; i < 4; i++ {
		protocol.WriteVarInt32ToBuffer(&buf, 0)
	}
	// Sky light array count + Block light array count
	protocol.WriteVarInt32ToBuffer(&buf, 0)
	protocol.WriteVarInt32ToBuffer(&buf, 0)

	return c.safeWrite(CbPlayChunkData, buf.Bytes())
}
