// Package world models the game world: blocks at integer positions, plus a
// minimal World interface so swappable storage backends (memory, region
// files) can live behind the same API.
package world

// Block is a single block state. The StateID is the global state index used
// in chunk-data palettes on the wire (vanilla 1.20.1 has ~26000 of them);
// Name is the namespaced identifier ("minecraft:stone").
//
// Today we hand-roll a handful of well-known constants. A future codegen
// step can dump the full registry from blocks.json.
type Block struct {
	StateID int32
	Name    string
}

// Vanilla 1.20.1 block-state IDs — every value is the block's `defaultState`
// from the PrismarineJS minecraft-data 1.20 dump (which 1.20.1 inherits via
// dataPaths.json). State IDs are sequential through the global block
// registry, so any single insert renumbers every block after it — never
// hand-edit these without re-checking against blocks.json. Values here
// supersede an earlier set that was off by 1.16-era offsets and was
// rendering wool/stairs/slabs/beacon as completely different blocks.
//
// Blocks with property variants (stairs, slabs, logs, glass with facing /
// half / waterlogged / axis) always render in their *default* property
// state because schem.stripProperties drops the "[…]" suffix before
// lookup. The block KIND will be correct; the orientation may not match
// the source schematic. Wiring properties end-to-end requires extending
// Block with a state map or doing palette-level rewrites in
// schem.ToTemplateAt.
var (
	Air              = Block{StateID: 0, Name: "minecraft:air"}
	Stone            = Block{StateID: 1, Name: "minecraft:stone"}
	Granite          = Block{StateID: 2, Name: "minecraft:granite"}
	PolishedGranite  = Block{StateID: 3, Name: "minecraft:polished_granite"}
	Diorite          = Block{StateID: 4, Name: "minecraft:diorite"}
	PolishedDiorite  = Block{StateID: 5, Name: "minecraft:polished_diorite"}
	Andesite         = Block{StateID: 6, Name: "minecraft:andesite"}
	PolishedAndesite = Block{StateID: 7, Name: "minecraft:polished_andesite"}
	GrassBlock       = Block{StateID: 9, Name: "minecraft:grass_block"}
	Dirt             = Block{StateID: 10, Name: "minecraft:dirt"}
	CoarseDirt       = Block{StateID: 11, Name: "minecraft:coarse_dirt"}
	Podzol           = Block{StateID: 13, Name: "minecraft:podzol"}
	Cobblestone      = Block{StateID: 14, Name: "minecraft:cobblestone"}

	OakPlanks     = Block{StateID: 15, Name: "minecraft:oak_planks"}
	SprucePlanks  = Block{StateID: 16, Name: "minecraft:spruce_planks"}
	BirchPlanks   = Block{StateID: 17, Name: "minecraft:birch_planks"}
	JunglePlanks  = Block{StateID: 18, Name: "minecraft:jungle_planks"}
	AcaciaPlanks  = Block{StateID: 19, Name: "minecraft:acacia_planks"}
	DarkOakPlanks = Block{StateID: 21, Name: "minecraft:dark_oak_planks"}

	Bedrock = Block{StateID: 79, Name: "minecraft:bedrock"}

	Sand       = Block{StateID: 112, Name: "minecraft:sand"}
	Gravel     = Block{StateID: 118, Name: "minecraft:gravel"}
	GoldOre    = Block{StateID: 123, Name: "minecraft:gold_ore"}
	IronOre    = Block{StateID: 125, Name: "minecraft:iron_ore"}
	CoalOre    = Block{StateID: 127, Name: "minecraft:coal_ore"}
	DiamondOre = Block{StateID: 4274, Name: "minecraft:diamond_ore"}

	// Mineral blocks + island materials — handy for BedWars-style maps
	// (generator markers, indestructible bases). default-state IDs from the
	// same minecraft-data 1.20 dump as the rest of this file.
	GoldBlock    = Block{StateID: 2091, Name: "minecraft:gold_block"}
	IronBlock    = Block{StateID: 2092, Name: "minecraft:iron_block"}
	Obsidian     = Block{StateID: 2354, Name: "minecraft:obsidian"}
	DiamondBlock = Block{StateID: 4276, Name: "minecraft:diamond_block"}
	EndStone     = Block{StateID: 7415, Name: "minecraft:end_stone"}
	EmeraldBlock = Block{StateID: 7665, Name: "minecraft:emerald_block"}

	OakLog    = Block{StateID: 131, Name: "minecraft:oak_log"}
	SpruceLog = Block{StateID: 134, Name: "minecraft:spruce_log"}
	BirchLog  = Block{StateID: 137, Name: "minecraft:birch_log"}

	Glass = Block{StateID: 519, Name: "minecraft:glass"}

	OakStairs         = Block{StateID: 2885, Name: "minecraft:oak_stairs"}
	OakSlab           = Block{StateID: 11024, Name: "minecraft:oak_slab"}
	Beacon            = Block{StateID: 7918, Name: "minecraft:beacon"}
	BrownStainedGlass = Block{StateID: 5958, Name: "minecraft:brown_stained_glass"}

	WhiteWool     = Block{StateID: 2047, Name: "minecraft:white_wool"}
	OrangeWool    = Block{StateID: 2048, Name: "minecraft:orange_wool"}
	MagentaWool   = Block{StateID: 2049, Name: "minecraft:magenta_wool"}
	LightBlueWool = Block{StateID: 2050, Name: "minecraft:light_blue_wool"}
	YellowWool    = Block{StateID: 2051, Name: "minecraft:yellow_wool"}
	LimeWool      = Block{StateID: 2052, Name: "minecraft:lime_wool"}
	PinkWool      = Block{StateID: 2053, Name: "minecraft:pink_wool"}
	GrayWool      = Block{StateID: 2054, Name: "minecraft:gray_wool"}
	LightGrayWool = Block{StateID: 2055, Name: "minecraft:light_gray_wool"}
	CyanWool      = Block{StateID: 2056, Name: "minecraft:cyan_wool"}
	PurpleWool    = Block{StateID: 2057, Name: "minecraft:purple_wool"}
	BlueWool      = Block{StateID: 2058, Name: "minecraft:blue_wool"}
	BrownWool     = Block{StateID: 2059, Name: "minecraft:brown_wool"}
	GreenWool     = Block{StateID: 2060, Name: "minecraft:green_wool"}
	RedWool       = Block{StateID: 2061, Name: "minecraft:red_wool"}
	BlackWool     = Block{StateID: 2062, Name: "minecraft:black_wool"}

	// Beds — the BedWars centrepiece. Like stairs/slabs above, a bed has
	// property variants (facing / part=head|foot / occupied), so these are
	// the *default* state only (facing=north, part=foot, occupied=false).
	// The block KIND is correct and breakable; rendering a full two-block
	// bed with matching facing needs per-block property support we don't
	// have yet, so a game places two adjacent bed blocks as "the bed".
	WhiteBed     = Block{StateID: 1691, Name: "minecraft:white_bed"}
	OrangeBed    = Block{StateID: 1707, Name: "minecraft:orange_bed"}
	MagentaBed   = Block{StateID: 1723, Name: "minecraft:magenta_bed"}
	LightBlueBed = Block{StateID: 1739, Name: "minecraft:light_blue_bed"}
	YellowBed    = Block{StateID: 1755, Name: "minecraft:yellow_bed"}
	LimeBed      = Block{StateID: 1771, Name: "minecraft:lime_bed"}
	PinkBed      = Block{StateID: 1787, Name: "minecraft:pink_bed"}
	GrayBed      = Block{StateID: 1803, Name: "minecraft:gray_bed"}
	LightGrayBed = Block{StateID: 1819, Name: "minecraft:light_gray_bed"}
	CyanBed      = Block{StateID: 1835, Name: "minecraft:cyan_bed"}
	PurpleBed    = Block{StateID: 1851, Name: "minecraft:purple_bed"}
	BlueBed      = Block{StateID: 1867, Name: "minecraft:blue_bed"}
	BrownBed     = Block{StateID: 1883, Name: "minecraft:brown_bed"}
	GreenBed     = Block{StateID: 1899, Name: "minecraft:green_bed"}
	RedBed       = Block{StateID: 1915, Name: "minecraft:red_bed"}
	BlackBed     = Block{StateID: 1931, Name: "minecraft:black_bed"}

	// Blocks used by schem/templates/bedwars/badwars_dota_map.schem (and
	// generally useful for decorated maps). default-state IDs pulled from the
	// same minecraft-data 1.20 blocks.json as the rest of this file. The
	// variant kinds here (stairs/slabs/walls/fences/panes/logs/clusters/…)
	// also have property tables in states.go, so they resolve their real
	// facing/half/type/connection state from the schematic — not just the
	// default. Purely decorative blocks without a states.go entry still
	// render in their default state.
	Water                           = Block{StateID: 80, Name: "minecraft:water"}
	Lava                            = Block{StateID: 96, Name: "minecraft:lava"}
	CherryLog                       = Block{StateID: 146, Name: "minecraft:cherry_log"}
	DarkOakLog                      = Block{StateID: 149, Name: "minecraft:dark_oak_log"}
	MangroveRoots                   = Block{StateID: 155, Name: "minecraft:mangrove_roots"}
	StrippedBambooBlock             = Block{StateID: 187, Name: "minecraft:stripped_bamboo_block"}
	StrippedSpruceWood              = Block{StateID: 217, Name: "minecraft:stripped_spruce_wood"}
	OakLeaves                       = Block{StateID: 264, Name: "minecraft:oak_leaves"}
	SpruceLeaves                    = Block{StateID: 292, Name: "minecraft:spruce_leaves"}
	CherryLeaves                    = Block{StateID: 404, Name: "minecraft:cherry_leaves"}
	Chest                           = Block{StateID: 2955, Name: "minecraft:chest"}
	RedstoneWire                    = Block{StateID: 4138, Name: "minecraft:redstone_wire"}
	StonePressurePlate              = Block{StateID: 5651, Name: "minecraft:stone_pressure_plate"}
	RedstoneWallTorch               = Block{StateID: 5740, Name: "minecraft:redstone_wall_torch"}
	PolishedBasalt                  = Block{StateID: 5857, Name: "minecraft:polished_basalt"}
	Glowstone                       = Block{StateID: 5864, Name: "minecraft:glowstone"}
	LightBlueStainedGlass           = Block{StateID: 5949, Name: "minecraft:light_blue_stained_glass"}
	CyanStainedGlass                = Block{StateID: 5955, Name: "minecraft:cyan_stained_glass"}
	BlueStainedGlass                = Block{StateID: 5957, Name: "minecraft:blue_stained_glass"}
	RedStainedGlass                 = Block{StateID: 5960, Name: "minecraft:red_stained_glass"}
	StoneBricks                     = Block{StateID: 6538, Name: "minecraft:stone_bricks"}
	MossyStoneBricks                = Block{StateID: 6539, Name: "minecraft:mossy_stone_bricks"}
	PackedMud                       = Block{StateID: 6542, Name: "minecraft:packed_mud"}
	MudBricks                       = Block{StateID: 6543, Name: "minecraft:mud_bricks"}
	InfestedStone                   = Block{StateID: 6544, Name: "minecraft:infested_stone"}
	Chain                           = Block{StateID: 6777, Name: "minecraft:chain"}
	StoneBrickStairs                = Block{StateID: 7120, Name: "minecraft:stone_brick_stairs"}
	NetherBrickStairs               = Block{StateID: 7316, Name: "minecraft:nether_brick_stairs"}
	EnderChest                      = Block{StateID: 7514, Name: "minecraft:ender_chest"}
	SpruceStairs                    = Block{StateID: 7677, Name: "minecraft:spruce_stairs"}
	SkeletonSkull                   = Block{StateID: 8827, Name: "minecraft:skeleton_skull"}
	LightWeightedPressurePlate      = Block{StateID: 9003, Name: "minecraft:light_weighted_pressure_plate"}
	RedstoneBlock                   = Block{StateID: 9083, Name: "minecraft:redstone_block"}
	QuartzBlock                     = Block{StateID: 9095, Name: "minecraft:quartz_block"}
	QuartzStairs                    = Block{StateID: 9111, Name: "minecraft:quartz_stairs"}
	LightBlueStainedGlassPane       = Block{StateID: 9359, Name: "minecraft:light_blue_stained_glass_pane"}
	BambooStairs                    = Block{StateID: 10075, Name: "minecraft:bamboo_stairs"}
	PrismarineBricks                = Block{StateID: 10323, Name: "minecraft:prismarine_bricks"}
	PrismarineBrickStairs           = Block{StateID: 10416, Name: "minecraft:prismarine_brick_stairs"}
	SeaLantern                      = Block{StateID: 10583, Name: "minecraft:sea_lantern"}
	CoalBlock                       = Block{StateID: 10604, Name: "minecraft:coal_block"}
	PackedIce                       = Block{StateID: 10605, Name: "minecraft:packed_ice"}
	RedWallBanner                   = Block{StateID: 10930, Name: "minecraft:red_wall_banner"}
	BlackWallBanner                 = Block{StateID: 10934, Name: "minecraft:black_wall_banner"}
	SpruceSlab                      = Block{StateID: 11030, Name: "minecraft:spruce_slab"}
	BambooSlab                      = Block{StateID: 11072, Name: "minecraft:bamboo_slab"}
	SmoothStoneSlab                 = Block{StateID: 11090, Name: "minecraft:smooth_stone_slab"}
	CutSandstoneSlab                = Block{StateID: 11102, Name: "minecraft:cut_sandstone_slab"}
	CobblestoneSlab                 = Block{StateID: 11114, Name: "minecraft:cobblestone_slab"}
	QuartzSlab                      = Block{StateID: 11144, Name: "minecraft:quartz_slab"}
	SmoothStone                     = Block{StateID: 11165, Name: "minecraft:smooth_stone"}
	SmoothSandstone                 = Block{StateID: 11166, Name: "minecraft:smooth_sandstone"}
	SmoothQuartz                    = Block{StateID: 11167, Name: "minecraft:smooth_quartz"}
	DarkOakFence                    = Block{StateID: 11616, Name: "minecraft:dark_oak_fence"}
	EndRod                          = Block{StateID: 12197, Name: "minecraft:end_rod"}
	MagmaBlock                      = Block{StateID: 12402, Name: "minecraft:magma_block"}
	BlackGlazedTerracotta           = Block{StateID: 12583, Name: "minecraft:black_glazed_terracotta"}
	OrangeConcrete                  = Block{StateID: 12588, Name: "minecraft:orange_concrete"}
	YellowConcrete                  = Block{StateID: 12591, Name: "minecraft:yellow_concrete"}
	LimeConcrete                    = Block{StateID: 12592, Name: "minecraft:lime_concrete"}
	BlackConcrete                   = Block{StateID: 12602, Name: "minecraft:black_concrete"}
	BlackConcretePowder             = Block{StateID: 12618, Name: "minecraft:black_concrete_powder"}
	BlueIce                         = Block{StateID: 12800, Name: "minecraft:blue_ice"}
	PolishedDioriteStairs           = Block{StateID: 13072, Name: "minecraft:polished_diorite_stairs"}
	SmoothSandstoneStairs           = Block{StateID: 13392, Name: "minecraft:smooth_sandstone_stairs"}
	SmoothQuartzStairs              = Block{StateID: 13472, Name: "minecraft:smooth_quartz_stairs"}
	DioriteStairs                   = Block{StateID: 13872, Name: "minecraft:diorite_stairs"}
	SmoothQuartzSlab                = Block{StateID: 13986, Name: "minecraft:smooth_quartz_slab"}
	DioriteSlab                     = Block{StateID: 14016, Name: "minecraft:diorite_slab"}
	MossyStoneBrickWall             = Block{StateID: 14994, Name: "minecraft:mossy_stone_brick_wall"}
	RedNetherBrickWall              = Block{StateID: 16938, Name: "minecraft:red_nether_brick_wall"}
	Lantern                         = Block{StateID: 18365, Name: "minecraft:lantern"}
	SoulLantern                     = Block{StateID: 18369, Name: "minecraft:soul_lantern"}
	Campfire                        = Block{StateID: 18373, Name: "minecraft:campfire"}
	SoulCampfire                    = Block{StateID: 18405, Name: "minecraft:soul_campfire"}
	WarpedTrapdoor                  = Block{StateID: 18686, Name: "minecraft:warped_trapdoor"}
	WarpedButton                    = Block{StateID: 18992, Name: "minecraft:warped_button"}
	CryingObsidian                  = Block{StateID: 19308, Name: "minecraft:crying_obsidian"}
	BlackstoneStairs                = Block{StateID: 19331, Name: "minecraft:blackstone_stairs"}
	BlackstoneWall                  = Block{StateID: 19403, Name: "minecraft:blackstone_wall"}
	BlackstoneSlab                  = Block{StateID: 19727, Name: "minecraft:blackstone_slab"}
	PolishedBlackstone              = Block{StateID: 19730, Name: "minecraft:polished_blackstone"}
	CrackedPolishedBlackstoneBricks = Block{StateID: 19732, Name: "minecraft:cracked_polished_blackstone_bricks"}
	PolishedBlackstoneBrickSlab     = Block{StateID: 19737, Name: "minecraft:polished_blackstone_brick_slab"}
	PolishedBlackstoneBrickStairs   = Block{StateID: 19751, Name: "minecraft:polished_blackstone_brick_stairs"}
	GildedBlackstone                = Block{StateID: 20144, Name: "minecraft:gilded_blackstone"}
	PolishedBlackstoneStairs        = Block{StateID: 20156, Name: "minecraft:polished_blackstone_stairs"}
	PolishedBlackstoneSlab          = Block{StateID: 20228, Name: "minecraft:polished_blackstone_slab"}
	PolishedBlackstoneButton        = Block{StateID: 20242, Name: "minecraft:polished_blackstone_button"}
	PolishedBlackstoneWall          = Block{StateID: 20260, Name: "minecraft:polished_blackstone_wall"}
	ChiseledNetherBricks            = Block{StateID: 20581, Name: "minecraft:chiseled_nether_bricks"}
	RedCandle                       = Block{StateID: 20827, Name: "minecraft:red_candle"}
	BlackCandle                     = Block{StateID: 20843, Name: "minecraft:black_candle"}
	AmethystCluster                 = Block{StateID: 20901, Name: "minecraft:amethyst_cluster"}
	Mud                             = Block{StateID: 22448, Name: "minecraft:mud"}
	CobbledDeepslate                = Block{StateID: 22452, Name: "minecraft:cobbled_deepslate"}
	CobbledDeepslateWall            = Block{StateID: 22542, Name: "minecraft:cobbled_deepslate_wall"}
	PolishedDeepslate               = Block{StateID: 22863, Name: "minecraft:polished_deepslate"}
	DeepslateTiles                  = Block{StateID: 23274, Name: "minecraft:deepslate_tiles"}
	DeepslateTileStairs             = Block{StateID: 23286, Name: "minecraft:deepslate_tile_stairs"}
	DeepslateTileSlab               = Block{StateID: 23358, Name: "minecraft:deepslate_tile_slab"}
	DeepslateBricks                 = Block{StateID: 23685, Name: "minecraft:deepslate_bricks"}
	DeepslateBrickStairs            = Block{StateID: 23697, Name: "minecraft:deepslate_brick_stairs"}
	DeepslateBrickWall              = Block{StateID: 23775, Name: "minecraft:deepslate_brick_wall"}
	CrackedDeepslateTiles           = Block{StateID: 24098, Name: "minecraft:cracked_deepslate_tiles"}
	SmoothBasalt                    = Block{StateID: 24102, Name: "minecraft:smooth_basalt"}
	RawGoldBlock                    = Block{StateID: 24105, Name: "minecraft:raw_gold_block"}
)

// byName indexes the hand-rolled set above. Populated by init() so adding
// a new var above automatically extends the lookup.
var byName map[string]Block

func init() {
	all := []Block{
		Air, Stone, Granite, PolishedGranite, Diorite, PolishedDiorite,
		Andesite, PolishedAndesite,
		GrassBlock, Dirt, CoarseDirt, Podzol, Cobblestone,
		OakPlanks, SprucePlanks, BirchPlanks, JunglePlanks, AcaciaPlanks, DarkOakPlanks,
		Bedrock,
		Sand, Gravel, GoldOre, IronOre, CoalOre, DiamondOre,
		GoldBlock, IronBlock, Obsidian, DiamondBlock, EndStone, EmeraldBlock,
		OakLog, SpruceLog, BirchLog,
		Glass,
		OakStairs, OakSlab, Beacon, BrownStainedGlass,
		WhiteWool, OrangeWool, MagentaWool, LightBlueWool,
		YellowWool, LimeWool, PinkWool, GrayWool,
		LightGrayWool, CyanWool, PurpleWool, BlueWool,
		BrownWool, GreenWool, RedWool, BlackWool,
		WhiteBed, OrangeBed, MagentaBed, LightBlueBed,
		YellowBed, LimeBed, PinkBed, GrayBed,
		LightGrayBed, CyanBed, PurpleBed, BlueBed,
		BrownBed, GreenBed, RedBed, BlackBed,
		// badwars_dota_map.schem block kinds.
		Water, Lava, CherryLog, DarkOakLog, MangroveRoots,
		StrippedBambooBlock, StrippedSpruceWood, OakLeaves, SpruceLeaves,
		CherryLeaves, Chest, RedstoneWire, StonePressurePlate,
		RedstoneWallTorch, PolishedBasalt, Glowstone, LightBlueStainedGlass,
		CyanStainedGlass, BlueStainedGlass, RedStainedGlass, StoneBricks,
		MossyStoneBricks, PackedMud, MudBricks, InfestedStone, Chain,
		StoneBrickStairs, NetherBrickStairs, EnderChest, SpruceStairs,
		SkeletonSkull, LightWeightedPressurePlate, RedstoneBlock,
		QuartzBlock, QuartzStairs, LightBlueStainedGlassPane, BambooStairs,
		PrismarineBricks, PrismarineBrickStairs, SeaLantern, CoalBlock,
		PackedIce, RedWallBanner, BlackWallBanner, SpruceSlab, BambooSlab,
		SmoothStoneSlab, CutSandstoneSlab, CobblestoneSlab, QuartzSlab,
		SmoothStone, SmoothSandstone, SmoothQuartz, DarkOakFence, EndRod,
		MagmaBlock, BlackGlazedTerracotta, OrangeConcrete, YellowConcrete,
		LimeConcrete, BlackConcrete, BlackConcretePowder, BlueIce,
		PolishedDioriteStairs, SmoothSandstoneStairs, SmoothQuartzStairs,
		DioriteStairs, SmoothQuartzSlab, DioriteSlab, MossyStoneBrickWall,
		RedNetherBrickWall, Lantern, SoulLantern, Campfire, SoulCampfire,
		WarpedTrapdoor, WarpedButton, CryingObsidian, BlackstoneStairs,
		BlackstoneWall, BlackstoneSlab, PolishedBlackstone,
		CrackedPolishedBlackstoneBricks, PolishedBlackstoneBrickSlab,
		PolishedBlackstoneBrickStairs, GildedBlackstone,
		PolishedBlackstoneStairs, PolishedBlackstoneSlab,
		PolishedBlackstoneButton, PolishedBlackstoneWall,
		ChiseledNetherBricks, RedCandle, BlackCandle, AmethystCluster, Mud,
		CobbledDeepslate, CobbledDeepslateWall, PolishedDeepslate,
		DeepslateTiles, DeepslateTileStairs, DeepslateTileSlab,
		DeepslateBricks, DeepslateBrickStairs, DeepslateBrickWall,
		CrackedDeepslateTiles, SmoothBasalt, RawGoldBlock,
	}
	byName = make(map[string]Block, len(all))
	for _, b := range all {
		byName[b.Name] = b
	}
}

// BlockByName looks up a block by namespaced identifier ("minecraft:stone").
// Unknown names return (Air, false). Property suffixes like "[axis=y]" are
// the caller's responsibility to strip first.
func BlockByName(name string) (Block, bool) {
	b, ok := byName[name]
	return b, ok
}

// KnownBlockNames returns every name we have a Block constant for. Useful
// for tab-complete or diagnostic dumps.
func KnownBlockNames() []string {
	out := make([]string, 0, len(byName))
	for name := range byName {
		out = append(out, name)
	}
	return out
}
