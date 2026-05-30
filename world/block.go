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

// Vanilla 1.20.1 block-state IDs. Source: vanilla server --reports JSON
// dump. We have a tiny subset — enough to import a typical small build
// from a .schem file. Anything unknown falls back to Air via BlockByName.
var (
	Air         = Block{StateID: 0, Name: "minecraft:air"}
	Stone       = Block{StateID: 1, Name: "minecraft:stone"}
	Granite     = Block{StateID: 2, Name: "minecraft:granite"}
	Diorite     = Block{StateID: 4, Name: "minecraft:diorite"}
	Andesite    = Block{StateID: 6, Name: "minecraft:andesite"}
	GrassBlock  = Block{StateID: 9, Name: "minecraft:grass_block"}
	Dirt        = Block{StateID: 10, Name: "minecraft:dirt"}
	CoarseDirt  = Block{StateID: 11, Name: "minecraft:coarse_dirt"}
	Podzol      = Block{StateID: 12, Name: "minecraft:podzol"}
	Cobblestone = Block{StateID: 14, Name: "minecraft:cobblestone"}

	OakPlanks     = Block{StateID: 15, Name: "minecraft:oak_planks"}
	SprucePlanks  = Block{StateID: 16, Name: "minecraft:spruce_planks"}
	BirchPlanks   = Block{StateID: 17, Name: "minecraft:birch_planks"}
	JunglePlanks  = Block{StateID: 18, Name: "minecraft:jungle_planks"}
	AcaciaPlanks  = Block{StateID: 19, Name: "minecraft:acacia_planks"}
	DarkOakPlanks = Block{StateID: 20, Name: "minecraft:dark_oak_planks"}

	Bedrock = Block{StateID: 79, Name: "minecraft:bedrock"}

	Sand       = Block{StateID: 102, Name: "minecraft:sand"}
	Gravel     = Block{StateID: 104, Name: "minecraft:gravel"}
	GoldOre    = Block{StateID: 105, Name: "minecraft:gold_ore"}
	IronOre    = Block{StateID: 107, Name: "minecraft:iron_ore"}
	CoalOre    = Block{StateID: 109, Name: "minecraft:coal_ore"}
	DiamondOre = Block{StateID: 145, Name: "minecraft:diamond_ore"}

	OakLog    = Block{StateID: 116, Name: "minecraft:oak_log"}
	SpruceLog = Block{StateID: 119, Name: "minecraft:spruce_log"}
	BirchLog  = Block{StateID: 122, Name: "minecraft:birch_log"}

	Glass = Block{StateID: 231, Name: "minecraft:glass"}

	// Furniture-style blocks below use approximate state IDs from 1.20.1's
	// blocks.json. Properties (stair facing, slab half, glass color) aren't
	// applied — we always use the default state. The blocks render with
	// correct kind (wood / glass) but may not have the exact orientation
	// or color the schematic intended.
	OakStairs         = Block{StateID: 3194, Name: "minecraft:oak_stairs"}
	OakSlab           = Block{StateID: 8404, Name: "minecraft:oak_slab"}
	Beacon            = Block{StateID: 5821, Name: "minecraft:beacon"}
	BrownStainedGlass = Block{StateID: 7273, Name: "minecraft:brown_stained_glass"}

	WhiteWool     = Block{StateID: 1440, Name: "minecraft:white_wool"}
	OrangeWool    = Block{StateID: 1441, Name: "minecraft:orange_wool"}
	MagentaWool   = Block{StateID: 1442, Name: "minecraft:magenta_wool"}
	LightBlueWool = Block{StateID: 1443, Name: "minecraft:light_blue_wool"}
	YellowWool    = Block{StateID: 1444, Name: "minecraft:yellow_wool"}
	LimeWool      = Block{StateID: 1445, Name: "minecraft:lime_wool"}
	PinkWool      = Block{StateID: 1446, Name: "minecraft:pink_wool"}
	GrayWool      = Block{StateID: 1447, Name: "minecraft:gray_wool"}
	LightGrayWool = Block{StateID: 1448, Name: "minecraft:light_gray_wool"}
	CyanWool      = Block{StateID: 1449, Name: "minecraft:cyan_wool"}
	PurpleWool    = Block{StateID: 1450, Name: "minecraft:purple_wool"}
	BlueWool      = Block{StateID: 1451, Name: "minecraft:blue_wool"}
	BrownWool     = Block{StateID: 1452, Name: "minecraft:brown_wool"}
	GreenWool     = Block{StateID: 1453, Name: "minecraft:green_wool"}
	RedWool       = Block{StateID: 1454, Name: "minecraft:red_wool"}
	BlackWool     = Block{StateID: 1455, Name: "minecraft:black_wool"}
)

// byName indexes the hand-rolled set above. Populated by init() so adding
// a new var above automatically extends the lookup.
var byName map[string]Block

func init() {
	all := []Block{
		Air, Stone, Granite, Diorite, Andesite,
		GrassBlock, Dirt, CoarseDirt, Podzol, Cobblestone,
		OakPlanks, SprucePlanks, BirchPlanks, JunglePlanks, AcaciaPlanks, DarkOakPlanks,
		Bedrock,
		Sand, Gravel, GoldOre, IronOre, CoalOre, DiamondOre,
		OakLog, SpruceLog, BirchLog,
		Glass,
		WhiteWool, OrangeWool, MagentaWool, LightBlueWool,
		YellowWool, LimeWool, PinkWool, GrayWool,
		LightGrayWool, CyanWool, PurpleWool, BlueWool,
		BrownWool, GreenWool, RedWool, BlackWool,
		OakStairs, OakSlab, Beacon, BrownStainedGlass,
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
