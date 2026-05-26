package server

import (
	"bytes"
	"minecraft-server/chunk"
	"minecraft-server/utils"
)

// sendLoginPlay writes the clientbound Login (Play) packet (0x28) for
// protocol 763 (1.20.1). Field order matches wiki.vg.
func (c *ClientConnection) sendLoginPlay() error {
	var buf bytes.Buffer

	// Entity ID
	buf.Write(utils.WriteInt(c.playerID))
	// Is hardcore
	buf.WriteByte(0)
	// Gamemode (0=Survival, 1=Creative, 2=Adventure, 3=Spectator)
	buf.WriteByte(1)
	// Previous gamemode (-1 = none)
	buf.WriteByte(0xFF)

	// Dimension count + names
	utils.WriteVarInt32ToBuffer(&buf, 1)
	buf.Write(utils.WriteString("minecraft:overworld"))

	// Registry codec (NBT, already-encoded bytes — no length prefix)
	buf.Write(RegistryCodec())

	// Current dimension type + name
	buf.Write(utils.WriteString("minecraft:overworld"))
	buf.Write(utils.WriteString("minecraft:overworld"))

	// Hashed seed
	buf.Write(utils.WriteLong(0))
	// Max players (ignored by vanilla client, still required)
	utils.WriteVarInt32ToBuffer(&buf, 20)
	// View distance + simulation distance (chunks)
	utils.WriteVarInt32ToBuffer(&buf, 10)
	utils.WriteVarInt32ToBuffer(&buf, 10)
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
	utils.WriteVarInt32ToBuffer(&buf, 0)

	return c.safeWrite(CbPlayLogin, buf.Bytes())
}

// sendSyncPlayerPosition writes Synchronize Player Position (0x3C). The
// teleportID echoes back in the next Confirm Teleportation packet.
func (c *ClientConnection) sendSyncPlayerPosition(x, y, z float64, teleportID int32) error {
	var buf bytes.Buffer
	buf.Write(utils.WriteDouble(x))
	buf.Write(utils.WriteDouble(y))
	buf.Write(utils.WriteDouble(z))
	buf.Write(utils.WriteFloat(0)) // yaw
	buf.Write(utils.WriteFloat(0)) // pitch
	buf.WriteByte(0)               // flags (all absolute)
	utils.WriteVarInt32ToBuffer(&buf, teleportID)
	return c.safeWrite(CbPlaySyncPos, buf.Bytes())
}

// sendChunkData writes the Chunk Data and Update Light packet (0x24) for an
// empty chunk. All sections are air, all light masks are empty (the client
// fills in default full-bright light for missing data, which is acceptable
// for an Overworld empty chunk).
func (c *ClientConnection) sendChunkData(chunkX, chunkZ int32) error {
	var buf bytes.Buffer

	buf.Write(utils.WriteInt(chunkX))
	buf.Write(utils.WriteInt(chunkZ))

	// Heightmaps NBT (root Compound, no length prefix).
	buf.Write(chunk.BuildEmptyHeightmaps())

	// Chunk data (paletted sections) with VarInt size prefix.
	data := chunk.BuildEmptyChunkData()
	utils.WriteVarInt32ToBuffer(&buf, int32(len(data)))
	buf.Write(data)

	// Block entities count
	utils.WriteVarInt32ToBuffer(&buf, 0)

	// Light masks — empty (no sections have transmitted light arrays).
	// BitSet on the wire: VarInt(long count) followed by long(s). We send a
	// zero-length BitSet for each mask, which the client treats as all-zero.
	for i := 0; i < 4; i++ {
		utils.WriteVarInt32ToBuffer(&buf, 0)
	}
	// Sky light array count + Block light array count
	utils.WriteVarInt32ToBuffer(&buf, 0)
	utils.WriteVarInt32ToBuffer(&buf, 0)

	return c.safeWrite(CbPlayChunkData, buf.Bytes())
}
