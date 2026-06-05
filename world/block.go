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
	BlackConcrete         = Block{StateID: 12602, Name: "minecraft:black_concrete"}
	CryingObsidian        = Block{StateID: 19308, Name: "minecraft:crying_obsidian"}
	RedGlazedTerracotta   = Block{StateID: 12579, Name: "minecraft:red_glazed_terracotta"}
	BlackGlazedTerracotta = Block{StateID: 12583, Name: "minecraft:black_glazed_terracotta"}
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
		BlackConcrete,
	 	CryingObsidian,
		RedGlazedTerracotta, BlackGlazedTerracotta,
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
