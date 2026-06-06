package server

import (
	"bytes"
	"encoding/json"
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

// sendSetCenterChunk (Cb 0x4E "update_view_position") tells the client
// which chunk the player is centred on. Without it the 1.20.1 vanilla
// client can't decide which chunks fall inside its view distance and
// keeps the "loading terrain" overlay open even after we've sent
// chunks.
func (c *ClientConnection) sendSetCenterChunk(chunkX, chunkZ int32) error {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, chunkX)
	protocol.WriteVarInt32ToBuffer(&buf, chunkZ)
	return c.safeWrite(CbPlaySetCenterChunk, buf.Bytes())
}

// sendSetDefaultSpawnPosition (Cb 0x50) seeds the world-spawn point used
// by the compass and as the respawn fallback. Vanilla expects this
// during the login sequence; without it the loading screen hangs.
// Angle is the yaw of the compass needle at the spawn (radians? float
// per protocol, vanilla uses 0).
func (c *ClientConnection) sendSetDefaultSpawnPosition(x, y, z int, angle float32) error {
	var buf bytes.Buffer
	buf.Write(protocol.WritePosition(x, y, z))
	buf.Write(protocol.WriteFloat(angle))
	return c.safeWrite(CbPlaySpawnPos, buf.Bytes())
}

// sendStartWaitingForChunks fires Game Event id 13 — added in 1.20.1
// specifically as the explicit "begin waiting for level chunks" signal.
// Sent right after Login(Play) so the client knows that the chunks
// arriving next are the level data it should buffer before unlocking the
// loading-terrain overlay.
func (c *ClientConnection) sendStartWaitingForChunks() error {
	payload := make([]byte, 0, 5)
	payload = append(payload, 13) // event id: start waiting for chunks
	payload = append(payload, protocol.WriteFloat(0)...)
	return c.safeWrite(CbPlayGameEvent, payload)
}

// sendExperience updates the XP bar overlay (Cb 0x56). The bar takes a
// float in [0,1]; level is the big number in the middle; totalXP is the
// statistic the F3 screen shows. We use it to draw the auth countdown:
// level = seconds remaining, bar = fraction of total still left.
func (c *ClientConnection) sendExperience(bar float32, level, totalXP int32) error {
	var buf bytes.Buffer
	buf.Write(protocol.WriteFloat(bar))
	protocol.WriteVarInt32ToBuffer(&buf, level)
	protocol.WriteVarInt32ToBuffer(&buf, totalXP)
	return c.safeWrite(CbPlaySetExperience, buf.Bytes())
}

// sendPlayDisconnect tells the client we're closing the connection with a
// human-readable reason that the vanilla client renders on the disconnect
// screen. The reason string is wrapped in a JSON chat component since the
// packet carries Chat, not String. Returns the wire error, if any —
// callers typically follow up with cleanup() regardless.
func (c *ClientConnection) sendPlayDisconnect(reason string) error {
	payload, _ := json.Marshal(map[string]string{"text": reason})
	return c.safeWrite(CbPlayDisconnect, protocol.WriteString(string(payload)))
}

// sendWorldChunks bakes the instance's world into real chunk-data packets:
// every non-air block is bucketed into its (chunkX, chunkZ) column and
// 16-tall section, packed into paletted sections by chunk.BuildChunkData, and
// streamed. The rectangle sent spans the world's occupied columns (plus a
// one-chunk pad) unioned with the spawn ring, so the client never sees void
// at the origin. Columns with no blocks go out as empty chunks.
//
// This replaces the old "empty chunks + a Block Update per block" path, which
// couldn't render large maps (millions of packets, and only a tiny ring of
// chunks was ever loaded).
func (c *ClientConnection) sendWorldChunks() error {
	type colKey struct{ cx, cz int32 }
	cols := make(map[colKey][][]int32)

	haveBounds := false
	var minCx, maxCx, minCz, maxCz int32

	c.instance.World.Range(func(p world.Position, b world.Block) {
		if b.StateID == 0 { // air — sections default to air
			return
		}
		sec := floorDiv(p.Y-chunk.MinY, 16)
		if sec < 0 || sec >= chunk.SectionCount {
			return // outside the dimension's vertical range
		}
		cx := int32(floorDiv(p.X, 16))
		cz := int32(floorDiv(p.Z, 16))
		k := colKey{cx, cz}
		col, ok := cols[k]
		if !ok {
			col = make([][]int32, chunk.SectionCount)
			cols[k] = col
			if !haveBounds {
				minCx, maxCx, minCz, maxCz, haveBounds = cx, cx, cz, cz, true
			}
			minCx, maxCx = minI32(minCx, cx), maxI32(maxCx, cx)
			minCz, maxCz = minI32(minCz, cz), maxI32(maxCz, cz)
		}
		if col[sec] == nil {
			col[sec] = make([]int32, 4096)
		}
		lx := p.X - int(cx)*16
		lz := p.Z - int(cz)*16
		ly := (p.Y - chunk.MinY) - sec*16
		col[sec][ly*256+lz*16+lx] = b.StateID
	})

	// Union the build's bounding box (padded) with the spawn ring.
	if !haveBounds {
		minCx, maxCx, minCz, maxCz = -HubChunkRadius, HubChunkRadius-1, -HubChunkRadius, HubChunkRadius-1
	} else {
		minCx, maxCx = minI32(minCx-1, -HubChunkRadius), maxI32(maxCx+1, HubChunkRadius-1)
		minCz, maxCz = minI32(minCz-1, -HubChunkRadius), maxI32(maxCz+1, HubChunkRadius-1)
	}

	empty := chunk.BuildEmptyChunkData()
	for cx := minCx; cx <= maxCx; cx++ {
		for cz := minCz; cz <= maxCz; cz++ {
			data := empty
			if col := cols[colKey{cx, cz}]; col != nil {
				data = chunk.BuildChunkData(col)
			}
			if err := c.sendChunkColumn(cx, cz, data); err != nil {
				return err
			}
		}
	}
	return nil
}

// floorDiv divides a by b rounding toward negative infinity (Go's / truncates
// toward zero), so negative world coordinates map to the correct chunk.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func minI32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxI32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// HubChunkRadius controls how many empty chunks are streamed around the
// spawn point. The square covers chunks [-R .. R-1] on both axes — so
// R=2 sends 4×4 = 16 chunks total, enough for the 1×1-chunk schematic
// at the origin plus a ring of loaded terrain around it (no void edges
// in the client's view).
const HubChunkRadius int32 = 2

// lightSections is the number of 16-tall sections the light data covers: the
// 24 world sections plus one padding section below and above (the client
// always expects sections [-1 .. SectionCount] for light).
const lightSections = chunk.SectionCount + 2

// skyLightMaskLong is a BitSet long with the low lightSections bits set — i.e.
// "every section carries a transmitted sky-light array". 26 bits fit in one
// long, so the masks are always a single-long BitSet.
const skyLightMaskLong int64 = (1 << lightSections) - 1

// fullSkyLight is one section's sky-light array: 4096 blocks × 4 bits = 2048
// bytes, every nibble 0xF (level 15). Shared read-only across all columns.
var fullSkyLight = bytes.Repeat([]byte{0xFF}, 2048)

// sendChunkColumn writes the Chunk Data and Update Light packet (0x24) for
// one column, using the caller-supplied paletted-section blob (see
// chunk.BuildChunkData / BuildEmptyChunkData). It sends a full sky-light array
// (level 15) for every section so the world renders in permanent daylight —
// no dark chunks regardless of which blocks occlude.
func (c *ClientConnection) sendChunkColumn(chunkX, chunkZ int32, data []byte) error {
	var buf bytes.Buffer

	buf.Write(protocol.WriteInt(chunkX))
	buf.Write(protocol.WriteInt(chunkZ))

	// Heightmaps NBT (root Compound, no length prefix).
	buf.Write(chunk.BuildEmptyHeightmaps())

	// Chunk data (paletted sections) with VarInt size prefix.
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(data)))
	buf.Write(data)

	// Block entities count
	protocol.WriteVarInt32ToBuffer(&buf, 0)

	writeFullDaylight(&buf)

	return c.safeWrite(CbPlayChunkData, buf.Bytes())
}

// writeFullDaylight appends the light section of the Chunk Data packet: a full
// level-15 sky-light array for every section, zero block light. Each mask is a
// BitSet (VarInt long-count + longs).
//
//	Sky Light Mask        = all sections (we send a sky array for each)
//	Block Light Mask      = empty (no block-light arrays)
//	Empty Sky Light Mask  = empty (none are all-zero — they're all full)
//	Empty Block Light Mask= all sections (block light is explicitly zero)
func writeFullDaylight(buf *bytes.Buffer) {
	// Sky Light Mask: one long with all section bits set.
	protocol.WriteVarInt32ToBuffer(buf, 1)
	buf.Write(protocol.WriteLong(skyLightMaskLong))
	// Block Light Mask: empty.
	protocol.WriteVarInt32ToBuffer(buf, 0)
	// Empty Sky Light Mask: empty.
	protocol.WriteVarInt32ToBuffer(buf, 0)
	// Empty Block Light Mask: one long with all section bits set.
	protocol.WriteVarInt32ToBuffer(buf, 1)
	buf.Write(protocol.WriteLong(skyLightMaskLong))

	// Sky Light arrays: one per section, each a length-prefixed 2048-byte
	// (all-0xF) nibble array.
	protocol.WriteVarInt32ToBuffer(buf, lightSections)
	for range lightSections {
		protocol.WriteVarInt32ToBuffer(buf, int32(len(fullSkyLight)))
		buf.Write(fullSkyLight)
	}
	// Block Light arrays: none.
	protocol.WriteVarInt32ToBuffer(buf, 0)
}
