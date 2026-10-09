package server

import (
	"bytes"
	"sort"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// block_bulk.go: the bulk half of the block API. Instance.SetBlock sends one
// Block Update per block, which is fine for a player placing cobblestone
// and hopeless for a region fill. SetBlocks applies a whole change list to
// the world, then broadcasts ONE Update Section Blocks (Cb 0x43) per 16³
// section touched — a WorldEdit //set over 50k blocks becomes a few dozen
// packets.

// sectionKey addresses one 16×16×16 chunk section.
type sectionKey struct {
	X, Y, Z int32 // chunk X, section Y (floor(y/16), may be negative), chunk Z
}

// SetBlocks applies changes to the world and broadcasts them per section.
// Later entries win when a position repeats. An empty list is a no-op.
func (i *Instance) SetBlocks(changes []world.BlockChange) {
	if len(changes) == 0 {
		return
	}
	for _, ch := range changes {
		i.World.SetBlock(ch.Pos, ch.Block)
		i.scheduleFluid(ch.Pos)
	}
	for _, payload := range sectionBlocksPayloads(changes) {
		i.Players.Broadcast(CbPlayUpdateSectionBlocks, payload, -1)
	}
}

// sectionBlocksPayloads groups changes by section and encodes one Update
// Section Blocks body per section. Sections are emitted in a stable
// (sorted) order and each position appears once (last write wins).
func sectionBlocksPayloads(changes []world.BlockChange) [][]byte {
	type entry struct {
		local int32 // packed x<<8 | z<<4 | y (4 bits each)
		state int32
	}
	sections := map[sectionKey]map[int32]int32{}
	for _, ch := range changes {
		key := sectionKey{X: int32(ch.Pos.X >> 4), Y: int32(ch.Pos.Y >> 4), Z: int32(ch.Pos.Z >> 4)}
		local := int32(ch.Pos.X&15)<<8 | int32(ch.Pos.Z&15)<<4 | int32(ch.Pos.Y&15)
		m := sections[key]
		if m == nil {
			m = map[int32]int32{}
			sections[key] = m
		}
		m[local] = ch.Block.StateID
	}

	keys := make([]sectionKey, 0, len(sections))
	for k := range sections {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		ka, kb := keys[a], keys[b]
		if ka.X != kb.X {
			return ka.X < kb.X
		}
		if ka.Z != kb.Z {
			return ka.Z < kb.Z
		}
		return ka.Y < kb.Y
	})

	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		m := sections[k]
		entries := make([]entry, 0, len(m))
		for local, state := range m {
			entries = append(entries, entry{local, state})
		}
		sort.Slice(entries, func(a, b int) bool { return entries[a].local < entries[b].local })

		var buf bytes.Buffer
		buf.Write(protocol.WriteLong(packSectionPos(k)))
		protocol.WriteVarInt32ToBuffer(&buf, int32(len(entries)))
		for _, e := range entries {
			// VarLong: state << 12 | local position.
			protocol.WriteVarLongToBuffer(&buf, int64(e.state)<<12|int64(e.local))
		}
		out = append(out, buf.Bytes())
	}
	return out
}

// packSectionPos encodes a chunk section position the way the protocol's
// Long field expects: x and z in 22 bits each, y in 20, all two's complement.
func packSectionPos(k sectionKey) int64 {
	return (int64(k.X)&0x3FFFFF)<<42 | (int64(k.Z)&0x3FFFFF)<<20 | (int64(k.Y) & 0xFFFFF)
}
