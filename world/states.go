package world

// Property-state resolution for blocks that have variants (stairs facing,
// slab half, log axis, …). Only blocks listed in blockStates participate;
// everything else falls back to its plain-default StateID via BlockByName.
//
// Wire encoding: each block reserves a contiguous range [MinStateID ..
// MinStateID + product(num_values) - 1]. Within the range, the offset
// from MinStateID is computed by treating the property list as a
// multi-dimensional index — properties iterated in declaration order,
// rightmost property being the fastest-varying. That matches the layout
// the vanilla client expects, and matches `defaultState` from
// minecraft-data's blocks.json (which we cross-checked when generating
// this table).
//
// Bools encode as ["true", "false"] — Minecraft's BlockState orders the
// "true" branch first.
//
// To add a block: read its entry from blocks.json, paste
//   MinStateID = block.minStateId
//   DefaultStateID = block.defaultState
//   Properties = block.states (preserving order)

type stateProperty struct {
	Name   string
	Values []string
}

type blockStateInfo struct {
	MinStateID     int32
	DefaultStateID int32
	Properties     []stateProperty
}

var blockStates = map[string]blockStateInfo{
	"minecraft:grass_block": {MinStateID: 8, DefaultStateID: 9, Properties: []stateProperty{
		{Name: "snowy", Values: []string{"true", "false"}},
	}},
	"minecraft:podzol": {MinStateID: 12, DefaultStateID: 13, Properties: []stateProperty{
		{Name: "snowy", Values: []string{"true", "false"}},
	}},
	"minecraft:oak_log": {MinStateID: 130, DefaultStateID: 131, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:spruce_log": {MinStateID: 133, DefaultStateID: 134, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:birch_log": {MinStateID: 136, DefaultStateID: 137, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:oak_stairs": {MinStateID: 2874, DefaultStateID: 2885, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:oak_slab": {MinStateID: 11021, DefaultStateID: 11024, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:red_glazed_terracotta": {MinStateID: 12579, DefaultStateID: 12579, Properties: []stateProperty{
    {Name: "facing", Values: []string{"north", "south", "west", "east"}},
	"minecraft:amethyst_cluster": {MinStateID: 20892, DefaultStateID: 20901, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "east", "south", "west", "up", "down"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:bamboo_slab": {MinStateID: 11069, DefaultStateID: 11072, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:bamboo_stairs": {MinStateID: 10064, DefaultStateID: 10075, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:black_candle": {MinStateID: 20840, DefaultStateID: 20843, Properties: []stateProperty{
		{Name: "candles", Values: []string{"1", "2", "3", "4"}},
		{Name: "lit", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:black_glazed_terracotta": {MinStateID: 12583, DefaultStateID: 12583, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
	}},
	"minecraft:black_wall_banner": {MinStateID: 10934, DefaultStateID: 10934, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
	}},
	"minecraft:blackstone_slab": {MinStateID: 19724, DefaultStateID: 19727, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:blackstone_stairs": {MinStateID: 19320, DefaultStateID: 19331, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:blackstone_wall": {MinStateID: 19400, DefaultStateID: 19403, Properties: []stateProperty{
		{Name: "east", Values: []string{"none", "low", "tall"}},
		{Name: "north", Values: []string{"none", "low", "tall"}},
		{Name: "south", Values: []string{"none", "low", "tall"}},
		{Name: "up", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"none", "low", "tall"}},
	}},
	"minecraft:campfire": {MinStateID: 18370, DefaultStateID: 18373, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "lit", Values: []string{"true", "false"}},
		{Name: "signal_fire", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:chain": {MinStateID: 6774, DefaultStateID: 6777, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:cherry_leaves": {MinStateID: 377, DefaultStateID: 404, Properties: []stateProperty{
		{Name: "distance", Values: []string{"1", "2", "3", "4", "5", "6", "7"}},
		{Name: "persistent", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:cherry_log": {MinStateID: 145, DefaultStateID: 146, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:chest": {MinStateID: 2954, DefaultStateID: 2955, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "type", Values: []string{"single", "left", "right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:cobbled_deepslate_wall": {MinStateID: 22539, DefaultStateID: 22542, Properties: []stateProperty{
		{Name: "east", Values: []string{"none", "low", "tall"}},
		{Name: "north", Values: []string{"none", "low", "tall"}},
		{Name: "south", Values: []string{"none", "low", "tall"}},
		{Name: "up", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"none", "low", "tall"}},
	}},
	"minecraft:cobblestone_slab": {MinStateID: 11111, DefaultStateID: 11114, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:cut_sandstone_slab": {MinStateID: 11099, DefaultStateID: 11102, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:dark_oak_fence": {MinStateID: 11585, DefaultStateID: 11616, Properties: []stateProperty{
		{Name: "east", Values: []string{"true", "false"}},
		{Name: "north", Values: []string{"true", "false"}},
		{Name: "south", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"true", "false"}},
	}},
	"minecraft:dark_oak_log": {MinStateID: 148, DefaultStateID: 149, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:deepslate_brick_stairs": {MinStateID: 23686, DefaultStateID: 23697, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:deepslate_brick_wall": {MinStateID: 23772, DefaultStateID: 23775, Properties: []stateProperty{
		{Name: "east", Values: []string{"none", "low", "tall"}},
		{Name: "north", Values: []string{"none", "low", "tall"}},
		{Name: "south", Values: []string{"none", "low", "tall"}},
		{Name: "up", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"none", "low", "tall"}},
	}},
	"minecraft:deepslate_tile_slab": {MinStateID: 23355, DefaultStateID: 23358, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:deepslate_tile_stairs": {MinStateID: 23275, DefaultStateID: 23286, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:diorite_slab": {MinStateID: 14013, DefaultStateID: 14016, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:diorite_stairs": {MinStateID: 13861, DefaultStateID: 13872, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:end_rod": {MinStateID: 12193, DefaultStateID: 12197, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "east", "south", "west", "up", "down"}},
	}},
	"minecraft:ender_chest": {MinStateID: 7513, DefaultStateID: 7514, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:lantern": {MinStateID: 18362, DefaultStateID: 18365, Properties: []stateProperty{
		{Name: "hanging", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:lava": {MinStateID: 96, DefaultStateID: 96, Properties: []stateProperty{
		{Name: "level", Values: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}},
	}},
	"minecraft:light_blue_stained_glass_pane": {MinStateID: 9328, DefaultStateID: 9359, Properties: []stateProperty{
		{Name: "east", Values: []string{"true", "false"}},
		{Name: "north", Values: []string{"true", "false"}},
		{Name: "south", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"true", "false"}},
	}},
	"minecraft:light_weighted_pressure_plate": {MinStateID: 9003, DefaultStateID: 9003, Properties: []stateProperty{
		{Name: "power", Values: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}},
	}},
	"minecraft:mangrove_roots": {MinStateID: 154, DefaultStateID: 155, Properties: []stateProperty{
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:mossy_stone_brick_wall": {MinStateID: 14991, DefaultStateID: 14994, Properties: []stateProperty{
		{Name: "east", Values: []string{"none", "low", "tall"}},
		{Name: "north", Values: []string{"none", "low", "tall"}},
		{Name: "south", Values: []string{"none", "low", "tall"}},
		{Name: "up", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"none", "low", "tall"}},
	}},
	"minecraft:nether_brick_stairs": {MinStateID: 7305, DefaultStateID: 7316, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:oak_leaves": {MinStateID: 237, DefaultStateID: 264, Properties: []stateProperty{
		{Name: "distance", Values: []string{"1", "2", "3", "4", "5", "6", "7"}},
		{Name: "persistent", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:polished_basalt": {MinStateID: 5856, DefaultStateID: 5857, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:polished_blackstone_brick_slab": {MinStateID: 19734, DefaultStateID: 19737, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:polished_blackstone_brick_stairs": {MinStateID: 19740, DefaultStateID: 19751, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:polished_blackstone_button": {MinStateID: 20233, DefaultStateID: 20242, Properties: []stateProperty{
		{Name: "face", Values: []string{"floor", "wall", "ceiling"}},
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "powered", Values: []string{"true", "false"}},
	}},
	"minecraft:polished_blackstone_slab": {MinStateID: 20225, DefaultStateID: 20228, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:polished_blackstone_stairs": {MinStateID: 20145, DefaultStateID: 20156, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:polished_blackstone_wall": {MinStateID: 20257, DefaultStateID: 20260, Properties: []stateProperty{
		{Name: "east", Values: []string{"none", "low", "tall"}},
		{Name: "north", Values: []string{"none", "low", "tall"}},
		{Name: "south", Values: []string{"none", "low", "tall"}},
		{Name: "up", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"none", "low", "tall"}},
	}},
	"minecraft:polished_diorite_stairs": {MinStateID: 13061, DefaultStateID: 13072, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:prismarine_brick_stairs": {MinStateID: 10405, DefaultStateID: 10416, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:quartz_slab": {MinStateID: 11141, DefaultStateID: 11144, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:quartz_stairs": {MinStateID: 9100, DefaultStateID: 9111, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:red_bed": {MinStateID: 1912, DefaultStateID: 1915, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "occupied", Values: []string{"true", "false"}},
		{Name: "part", Values: []string{"head", "foot"}},
	}},
	"minecraft:red_candle": {MinStateID: 20824, DefaultStateID: 20827, Properties: []stateProperty{
		{Name: "candles", Values: []string{"1", "2", "3", "4"}},
		{Name: "lit", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:red_nether_brick_wall": {MinStateID: 16935, DefaultStateID: 16938, Properties: []stateProperty{
		{Name: "east", Values: []string{"none", "low", "tall"}},
		{Name: "north", Values: []string{"none", "low", "tall"}},
		{Name: "south", Values: []string{"none", "low", "tall"}},
		{Name: "up", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
		{Name: "west", Values: []string{"none", "low", "tall"}},
	}},
	"minecraft:red_wall_banner": {MinStateID: 10930, DefaultStateID: 10930, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
	}},
	"minecraft:redstone_wall_torch": {MinStateID: 5740, DefaultStateID: 5740, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "lit", Values: []string{"true", "false"}},
	}},
	"minecraft:redstone_wire": {MinStateID: 2978, DefaultStateID: 4138, Properties: []stateProperty{
		{Name: "east", Values: []string{"up", "side", "none"}},
		{Name: "north", Values: []string{"up", "side", "none"}},
		{Name: "power", Values: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}},
		{Name: "south", Values: []string{"up", "side", "none"}},
		{Name: "west", Values: []string{"up", "side", "none"}},
	}},
	"minecraft:skeleton_skull": {MinStateID: 8827, DefaultStateID: 8827, Properties: []stateProperty{
		{Name: "rotation", Values: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}},
	}},
	"minecraft:smooth_quartz_slab": {MinStateID: 13983, DefaultStateID: 13986, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:smooth_quartz_stairs": {MinStateID: 13461, DefaultStateID: 13472, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:smooth_sandstone_stairs": {MinStateID: 13381, DefaultStateID: 13392, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:smooth_stone_slab": {MinStateID: 11087, DefaultStateID: 11090, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:soul_campfire": {MinStateID: 18402, DefaultStateID: 18405, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "lit", Values: []string{"true", "false"}},
		{Name: "signal_fire", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:soul_lantern": {MinStateID: 18366, DefaultStateID: 18369, Properties: []stateProperty{
		{Name: "hanging", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:spruce_leaves": {MinStateID: 265, DefaultStateID: 292, Properties: []stateProperty{
		{Name: "distance", Values: []string{"1", "2", "3", "4", "5", "6", "7"}},
		{Name: "persistent", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:spruce_slab": {MinStateID: 11027, DefaultStateID: 11030, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:spruce_stairs": {MinStateID: 7666, DefaultStateID: 7677, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:stone_brick_stairs": {MinStateID: 7109, DefaultStateID: 7120, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:stone_pressure_plate": {MinStateID: 5650, DefaultStateID: 5651, Properties: []stateProperty{
		{Name: "powered", Values: []string{"true", "false"}},
	}},
	"minecraft:stripped_bamboo_block": {MinStateID: 186, DefaultStateID: 187, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:stripped_spruce_wood": {MinStateID: 216, DefaultStateID: 217, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:warped_button": {MinStateID: 18983, DefaultStateID: 18992, Properties: []stateProperty{
		{Name: "face", Values: []string{"floor", "wall", "ceiling"}},
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "powered", Values: []string{"true", "false"}},
	}},
	"minecraft:warped_trapdoor": {MinStateID: 18671, DefaultStateID: 18686, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "open", Values: []string{"true", "false"}},
		{Name: "powered", Values: []string{"true", "false"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:water": {MinStateID: 80, DefaultStateID: 80, Properties: []stateProperty{
		{Name: "level", Values: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}},
	}},
}

// ResolveStateID returns the wire-level state ID for a block name plus the
// given property map. The name is expected with the namespace prefix
// ("minecraft:oak_stairs"). Unknown property names are ignored; unknown
// values for a known property fall back to whatever index that property
// has in the block's defaultState. Missing properties also fall back to
// the default.
//
// If the block has no entry in blockStates (i.e. no variants registered),
// returns its default StateID via the plain byName lookup. Returns 0 for
// completely unknown blocks (which the caller should treat as "skip" /
// air).
func ResolveStateID(name string, props map[string]string) int32 {
	info, hasStates := blockStates[name]
	if !hasStates {
		if b, ok := byName[name]; ok {
			return b.StateID
		}
		return 0
	}
	if len(info.Properties) == 0 {
		return info.DefaultStateID
	}

	// Strides right-to-left so the last property is fastest-varying.
	strides := make([]int32, len(info.Properties))
	strides[len(strides)-1] = 1
	for i := len(strides) - 2; i >= 0; i-- {
		strides[i] = strides[i+1] * int32(len(info.Properties[i+1].Values))
	}

	// Precompute per-property index of the block's default state so we can
	// fall back property-by-property when the caller hasn't supplied one.
	diff := info.DefaultStateID - info.MinStateID
	var offset int32
	for i, p := range info.Properties {
		n := int32(len(p.Values))
		defaultIdx := diff / strides[i] % n

		idx := defaultIdx
		if val, ok := props[p.Name]; ok {
			for j, v := range p.Values {
				if v == val {
					idx = int32(j)
					break
				}
			}
		}
		offset += idx * strides[i]
	}
	return info.MinStateID + offset
}
