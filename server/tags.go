package server

import (
	"bytes"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// tags.go sends the registry tags the vanilla client needs for client-side
// decisions. Tags are NOT built into the client — they come from the server
// (Update Tags, Cb 0x6E) right after Login (Play). Without them every tag is
// empty, and the client then:
//
//   - renders every fluid with the water textures + biome tint, because
//     LiquidBlockRenderer checks FluidTags.LAVA to pick the lava sprites
//     (that is why lava "looked like water"),
//   - never treats the player as being in water (FluidTags.WATER): no
//     swimming, no water slowdown, no bubbles,
//   - doesn't let players climb ladders/vines (BlockTags.CLIMBABLE),
//   - can't draw a bow in survival: BowItem.use looks for ammo with
//     ItemTags.ARROWS, finds none and refuses (creative substitutes an
//     arrow, which is why bows "only worked in creative").
//
// We send just those: the fluid registry's two tags, the block registry's
// climbable tag and the item registry's arrows tag. Numeric ids are the
// 1.20.1 REGISTRY ids (not block-state ids), from minecraft-data 1.20.

// Registry ids for the fluid registry (order: empty, flowing_water, water,
// flowing_lava, lava).
const (
	fluidRegFlowingWater int32 = 1
	fluidRegWater        int32 = 2
	fluidRegFlowingLava  int32 = 3
	fluidRegLava         int32 = 4
)

// Block registry ids of the climbable blocks.
var climbableBlockIDs = []int32{
	196, // ladder
	317, // vine
	772, // scaffolding
	805, // weeping_vines
	806, // weeping_vines_plant
	807, // twisting_vines
	808, // twisting_vines_plant
	956, // cave_vines
	957, // cave_vines_plant
}

// arrowItemIDs is the item registry's minecraft:arrows tag (vanilla
// data/minecraft/tags/items/arrows.json).
var arrowItemIDs = itemIDs("minecraft:arrow", "minecraft:tipped_arrow", "minecraft:spectral_arrow")

// itemIDs resolves item names through the item registry, panicking at init
// on a typo so a missing tag entry can't ship silently.
func itemIDs(names ...string) []int32 {
	out := make([]int32, 0, len(names))
	for _, n := range names {
		id, ok := world.ItemByName(n)
		if !ok {
			panic("tags: unknown item " + n)
		}
		out = append(out, id)
	}
	return out
}

type tagSet struct {
	name    string
	entries []int32
}

type taggedRegistry struct {
	registry string
	tags     []tagSet
}

// serverTags is everything sendUpdateTags ships.
var serverTags = []taggedRegistry{
	{registry: "minecraft:fluid", tags: []tagSet{
		{name: "minecraft:water", entries: []int32{fluidRegWater, fluidRegFlowingWater}},
		{name: "minecraft:lava", entries: []int32{fluidRegLava, fluidRegFlowingLava}},
	}},
	{registry: "minecraft:block", tags: []tagSet{
		{name: "minecraft:climbable", entries: climbableBlockIDs},
	}},
	{registry: "minecraft:item", tags: []tagSet{
		{name: "minecraft:arrows", entries: arrowItemIDs},
	}},
}

// sendUpdateTags sends Update Tags with serverTags.
func (c *ClientConnection) sendUpdateTags() error {
	return c.safeWrite(CbPlayUpdateTags, updateTagsPayload(serverTags))
}

// updateTagsPayload encodes: VarInt registries; per registry: Identifier,
// VarInt tags; per tag: Identifier, VarInt count, VarInt entries.
func updateTagsPayload(regs []taggedRegistry) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(regs)))
	for _, r := range regs {
		buf.Write(protocol.WriteString(r.registry))
		protocol.WriteVarInt32ToBuffer(&buf, int32(len(r.tags)))
		for _, t := range r.tags {
			buf.Write(protocol.WriteString(t.name))
			protocol.WriteVarInt32ToBuffer(&buf, int32(len(t.entries)))
			for _, e := range t.entries {
				protocol.WriteVarInt32ToBuffer(&buf, e)
			}
		}
	}
	return buf.Bytes()
}
