package server

import (
	"bytes"
	"minecraft-server/chunk"
	"minecraft-server/protocol"
)

// sendLoginPlay writes the clientbound Login (Play) packet (0x28) for
// protocol 763 (1.20.1). Field order matches wiki.vg.
func (c *ClientConnection) sendLoginPlay() error {
	var buf bytes.Buffer

	p := c.player

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
