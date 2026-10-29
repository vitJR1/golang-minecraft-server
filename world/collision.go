package world

import (
	"strconv"
	"strings"
)

// collision.go classifies blocks for movement checks (server/movement.go).
// Two predicates, each biased toward the side that can't wrong a vanilla
// player:
//
//   - FullCube: the block's collision box is the whole cell. Used to catch a
//     player inside a wall (phase), so it's a whitelist — an unlisted block
//     is "not full" and never trips the check.
//   - Collidable: the block has any collision at all. Used to decide that a
//     player stands on something, so it's a blacklist of passable blocks —
//     an unlisted block counts as ground, which only ever lets a move pass.

// partialMarkers are name fragments of blocks whose collision is smaller than
// the cell (or absent) even though the rest of the name says "stone/wood".
var partialMarkers = []string{
	"slab", "stairs", "wall", "fence", "pane", "door", "bars", "carpet",
	"_bed", "chest", "sign", "torch", "ladder", "button", "pressure_plate",
	"rail", "lantern", "chain", "rod", "head", "skull", "banner", "pot",
	"cake", "anvil", "hopper", "cauldron", "stand", "table", "frame",
	"lectern", "stonecutter", "bell", "campfire", "grindstone", "scaffolding",
	"candle", "cactus", "cobweb", "pickle", "lily", "kelp", "vine", "tripwire",
	"lever", "repeater", "comparator", "detector", "piston", "amethyst_cluster",
	"bud", "dripstone", "azalea", "chorus", "bamboo", "cane", "coral_fan",
	"egg", "sculk_sensor", "shrieker", "conduit", "plate", "string", "web",
}

// partialExact are whole names of partial blocks a marker can't catch.
var partialExact = map[string]bool{
	"minecraft:snow": true, "minecraft:soul_sand": true, "minecraft:mud": true,
	"minecraft:honey_block": true, "minecraft:farmland": true, "minecraft:dirt_path": true,
	"minecraft:enchanting_table": true, "minecraft:daylight_detector": true,
	"minecraft:end_portal_frame": true, "minecraft:composter": true,
	"minecraft:brewing_stand": true, "minecraft:dragon_egg": true,
}

// fullSuffixes / fullExact whitelist the full-cube blocks maps are built
// from.
var fullSuffixes = []string{
	"_planks", "_log", "_wood", "_wool", "_concrete", "_concrete_powder",
	"_terracotta", "_bricks", "_ore", "_stained_glass", "_leaves",
	"_block", "_mushroom_block", "sandstone", "_copper", "copper_block",
}

var fullExact = map[string]bool{
	"stone": true, "cobblestone": true, "mossy_cobblestone": true, "bricks": true,
	"granite": true, "polished_granite": true, "diorite": true, "polished_diorite": true,
	"andesite": true, "polished_andesite": true, "deepslate": true, "cobbled_deepslate": true,
	"polished_deepslate": true, "tuff": true, "calcite": true, "dirt": true, "coarse_dirt": true,
	"rooted_dirt": true, "grass_block": true, "podzol": true, "mycelium": true, "sand": true,
	"red_sand": true, "gravel": true, "clay": true, "obsidian": true, "crying_obsidian": true,
	"end_stone": true, "glass": true, "tinted_glass": true, "terracotta": true,
	"netherrack": true, "glowstone": true, "sea_lantern": true, "prismarine": true,
	"dark_prismarine": true, "smooth_quartz": true, "quartz_pillar": true, "chiseled_quartz_block": true,
	"purpur_pillar": true, "bookshelf": true, "furnace": true, "blast_furnace": true, "smoker": true,
	"melon": true, "pumpkin": true, "carved_pumpkin": true, "jack_o_lantern": true,
	"ice": true, "packed_ice": true, "blue_ice": true, "sponge": true, "wet_sponge": true,
	"tnt": true, "note_block": true, "jukebox": true, "target": true, "bedrock": true,
	"barrier": true, "spawner": true, "shroomlight": true, "basalt": true, "polished_basalt": true,
	"smooth_basalt": true, "blackstone": true, "polished_blackstone": true, "mushroom_stem": true,
	"crimson_stem": true, "warped_stem": true, "stripped_crimson_stem": true, "stripped_warped_stem": true,
	"crimson_nylium": true, "warped_nylium": true, "smooth_stone": true, "dispenser": true, "dropper": true,
	"observer": true, "beacon": true, "loom": true, "cartography_table": true, "fletching_table": true,
	"smithing_table": true, "crafting_table": true, "magma_block": true, "slime_block": true,
}

// FullCube reports whether b's collision box fills its whole cell. The
// exact whitelist wins (sea_lantern, crafting_table are full despite their
// names); then any partial marker rules a block out; then the suffixes.
func FullCube(b Block) bool { return shapeOf(b).full }

func fullCube(b Block) bool {
	if partialExact[b.Name] || !strings.HasPrefix(b.Name, "minecraft:") {
		return false
	}
	n := strings.TrimPrefix(b.Name, "minecraft:")
	if fullExact[n] {
		return true
	}
	for _, m := range partialMarkers {
		if strings.Contains(n, m) {
			return false
		}
	}
	for _, s := range fullSuffixes {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

// passableSuffixes / passableExact are blocks with no collision box. Only
// suffixes and whole names: a substring like "light" would also match
// light_blue_wool and leave a player standing on it "in the air".
var passableSuffixes = []string{
	"torch", "_sign", "_sapling", "rail", "_button", "_pressure_plate",
	"_banner", "_tulip", "_coral_fan", "_coral", "_fungus",
}

var passableExact = map[string]bool{
	"lever": true, "tripwire": true, "tripwire_hook": true, "redstone_wire": true,
	"cobweb": true, "dandelion": true, "poppy": true, "blue_orchid": true, "allium": true,
	"azure_bluet": true, "oxeye_daisy": true, "cornflower": true, "lily_of_the_valley": true,
	"wither_rose": true, "sunflower": true, "lilac": true, "rose_bush": true, "peony": true,
	"tall_grass": true, "large_fern": true, "red_mushroom": true, "brown_mushroom": true,
	"wheat": true, "carrots": true, "potatoes": true, "beetroots": true, "nether_wart": true,
	"sugar_cane": true, "kelp": true, "kelp_plant": true, "nether_sprouts": true,
	"tall_seagrass": true, "twisting_vines": true, "weeping_vines": true, "glow_lichen": true,
	"pumpkin_stem": true, "melon_stem": true, "sweet_berry_bush": true, "string": true,
	"crimson_roots": true, "warped_roots": true, "hanging_roots": true, // mangrove_roots is solid
}

// Collidable reports whether b has any collision box: air, fluids,
// replaceable vegetation and the passable lists don't.
func Collidable(b Block) bool { return shapeOf(b).collidable }

func collidable(b Block) bool {
	if b == Air || Replaceable(b) {
		return false
	}
	n := strings.TrimPrefix(b.Name, "minecraft:")
	if passableExact[n] {
		return false
	}
	for _, s := range passableSuffixes {
		if strings.HasSuffix(n, s) {
			return false
		}
	}
	return true
}

// CollisionHeight is the vertical extent of b's collision box inside its
// cell, as offsets from the cell's floor: lo..hi (a bottom slab 0..0.5, a
// top slab 0.5..1, a bed 0..0.5625, a fence 0..1.5). ok is false for a
// block without collision. Horizontally the box is taken as the whole cell
// — exact for the blocks maps are built from, a fair wall for the rest.
// Used by the server-side physics of bots (server/bot_physics.go).
func CollisionHeight(b Block) (lo, hi float64, ok bool) {
	sh := shapeOf(b)
	return sh.lo, sh.hi, sh.hasBox
}

func collisionHeight(b Block) (lo, hi float64, ok bool) {
	if !collidable(b) {
		return 0, 0, false
	}
	if fullCube(b) {
		return 0, 1, true
	}
	n := strings.TrimPrefix(b.Name, "minecraft:")
	props := StateProperties(b.Name, b.StateID)
	switch {
	case strings.HasSuffix(n, "_slab"):
		switch props["type"] {
		case "top":
			return 0.5, 1, true
		case "double":
			return 0, 1, true
		}
		return 0, 0.5, true
	case strings.HasSuffix(n, "_bed"):
		return 0, 0.5625, true
	case strings.HasSuffix(n, "carpet"):
		return 0, 0.0625, true
	case n == "snow":
		layers := 1
		if l, err := strconv.Atoi(props["layers"]); err == nil {
			layers = l
		}
		if layers <= 1 {
			return 0, 0, false // one layer has no collision
		}
		return 0, float64(layers-1) / 8, true
	case strings.HasSuffix(n, "_fence"), strings.HasSuffix(n, "_fence_gate"), strings.HasSuffix(n, "_wall"):
		return 0, 1.5, true
	case strings.Contains(n, "chest"):
		return 0, 0.875, true
	case n == "soul_sand", n == "mud":
		return 0, 0.875, true
	case n == "farmland", n == "dirt_path":
		return 0, 0.9375, true
	case strings.Contains(n, "pressure_plate"), n == "ladder", strings.HasSuffix(n, "_trapdoor"):
		return 0, 0, false // too thin to stand in the way of a walk
	}
	return 0, 1, true
}
