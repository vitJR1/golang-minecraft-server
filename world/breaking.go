package world

import (
	"math"
	"strings"
)

// breaking.go: vanilla-style block hardness, tool requirements and drops,
// used by the server to time survival digging and decide whether a broken
// block yields its item.
//
// The numbers follow the 1.20.1 block data (hardness, "requires correct
// tool for drops", preferred tool) for the blocks a mini-game server
// actually meets — building blocks, ores, wood, terrain, furniture. Lookup
// is by exact name first, then by name patterns (suffix/contains), then a
// mild default, so an unlisted block is still breakable by hand.

// ToolType is the class of tool that mines a block fastest.
type ToolType int

const (
	NoTool ToolType = iota
	Pickaxe
	Axe
	Shovel
	Hoe
	Shears
	Sword
)

// ToolTier is a tool's material rank; a block that requires a tool also
// requires at least a certain tier for its drop.
type ToolTier int

const (
	TierNone ToolTier = iota
	TierWood          // also gold: same mining level
	TierStone
	TierIron
	TierDiamond
	TierNetherite
)

// BreakInfo describes how a block breaks.
type BreakInfo struct {
	Hardness float64  // vanilla hardness; <0 = unbreakable, 0 = instant
	Tool     ToolType // the tool class that speeds the dig up
	MinTier  ToolTier // the tier needed for a drop when RequiresTool
	// RequiresTool: without a Tool of at least MinTier the block still
	// breaks (slowly) but drops nothing.
	RequiresTool bool
}

// Unbreakable is the BreakInfo of bedrock & co.
var Unbreakable = BreakInfo{Hardness: -1}

var exactBreakInfo = map[string]BreakInfo{
	"minecraft:bedrock":          Unbreakable,
	"minecraft:barrier":          Unbreakable,
	"minecraft:end_portal_frame": Unbreakable,
	"minecraft:command_block":    Unbreakable,
	"minecraft:water":            Unbreakable, // fluids can't be dug out
	"minecraft:lava":             Unbreakable,

	"minecraft:obsidian":          {Hardness: 50, Tool: Pickaxe, MinTier: TierDiamond, RequiresTool: true},
	"minecraft:crying_obsidian":   {Hardness: 50, Tool: Pickaxe, MinTier: TierDiamond, RequiresTool: true},
	"minecraft:ancient_debris":    {Hardness: 30, Tool: Pickaxe, MinTier: TierDiamond, RequiresTool: true},
	"minecraft:netherite_block":   {Hardness: 50, Tool: Pickaxe, MinTier: TierDiamond, RequiresTool: true},
	"minecraft:end_stone":         {Hardness: 3, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:stone":             {Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:cobblestone":       {Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:deepslate":         {Hardness: 3, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:cobbled_deepslate": {Hardness: 3.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:netherrack":        {Hardness: 0.4, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:glowstone":         {Hardness: 0.3},
	"minecraft:magma_block":       {Hardness: 0.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:iron_block":        {Hardness: 5, Tool: Pickaxe, MinTier: TierStone, RequiresTool: true},
	"minecraft:gold_block":        {Hardness: 3, Tool: Pickaxe, MinTier: TierIron, RequiresTool: true},
	"minecraft:diamond_block":     {Hardness: 5, Tool: Pickaxe, MinTier: TierIron, RequiresTool: true},
	"minecraft:emerald_block":     {Hardness: 5, Tool: Pickaxe, MinTier: TierIron, RequiresTool: true},
	"minecraft:lapis_block":       {Hardness: 3, Tool: Pickaxe, MinTier: TierStone, RequiresTool: true},
	"minecraft:redstone_block":    {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:coal_block":        {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:copper_block":      {Hardness: 3, Tool: Pickaxe, MinTier: TierStone, RequiresTool: true},
	"minecraft:coal_ore":          {Hardness: 3, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:iron_ore":          {Hardness: 3, Tool: Pickaxe, MinTier: TierStone, RequiresTool: true},
	"minecraft:copper_ore":        {Hardness: 3, Tool: Pickaxe, MinTier: TierStone, RequiresTool: true},
	"minecraft:lapis_ore":         {Hardness: 3, Tool: Pickaxe, MinTier: TierStone, RequiresTool: true},
	"minecraft:gold_ore":          {Hardness: 3, Tool: Pickaxe, MinTier: TierIron, RequiresTool: true},
	"minecraft:redstone_ore":      {Hardness: 3, Tool: Pickaxe, MinTier: TierIron, RequiresTool: true},
	"minecraft:diamond_ore":       {Hardness: 3, Tool: Pickaxe, MinTier: TierIron, RequiresTool: true},
	"minecraft:emerald_ore":       {Hardness: 3, Tool: Pickaxe, MinTier: TierIron, RequiresTool: true},
	"minecraft:nether_quartz_ore": {Hardness: 3, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:nether_gold_ore":   {Hardness: 3, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:quartz_block":      {Hardness: 0.8, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:sandstone":         {Hardness: 0.8, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:red_sandstone":     {Hardness: 0.8, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:terracotta":        {Hardness: 1.25, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:bricks":            {Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:nether_bricks":     {Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:blackstone":        {Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:basalt":            {Hardness: 1.25, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:tuff":              {Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:calcite":           {Hardness: 0.75, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:amethyst_block":    {Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:dripstone_block":   {Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:bone_block":        {Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:furnace":           {Hardness: 3.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:dispenser":         {Hardness: 3.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:dropper":           {Hardness: 3.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:hopper":            {Hardness: 3, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:cauldron":          {Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:anvil":             {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:enchanting_table":  {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:lantern":           {Hardness: 3.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:chain":             {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:iron_bars":         {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:iron_door":         {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:iron_trapdoor":     {Hardness: 5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:ice":               {Hardness: 0.5, Tool: Pickaxe},
	"minecraft:packed_ice":        {Hardness: 0.5, Tool: Pickaxe},
	"minecraft:blue_ice":          {Hardness: 2.8, Tool: Pickaxe},
	"minecraft:beacon":            {Hardness: 3},
	"minecraft:sea_lantern":       {Hardness: 0.3},

	"minecraft:dirt":        {Hardness: 0.5, Tool: Shovel},
	"minecraft:coarse_dirt": {Hardness: 0.5, Tool: Shovel},
	"minecraft:rooted_dirt": {Hardness: 0.5, Tool: Shovel},
	"minecraft:grass_block": {Hardness: 0.6, Tool: Shovel},
	"minecraft:podzol":      {Hardness: 0.5, Tool: Shovel},
	"minecraft:mycelium":    {Hardness: 0.6, Tool: Shovel},
	"minecraft:dirt_path":   {Hardness: 0.65, Tool: Shovel},
	"minecraft:farmland":    {Hardness: 0.6, Tool: Shovel},
	"minecraft:mud":         {Hardness: 0.5, Tool: Shovel},
	"minecraft:sand":        {Hardness: 0.5, Tool: Shovel},
	"minecraft:red_sand":    {Hardness: 0.5, Tool: Shovel},
	"minecraft:gravel":      {Hardness: 0.6, Tool: Shovel},
	"minecraft:clay":        {Hardness: 0.6, Tool: Shovel},
	"minecraft:soul_sand":   {Hardness: 0.5, Tool: Shovel},
	"minecraft:soul_soil":   {Hardness: 0.5, Tool: Shovel},
	"minecraft:snow_block":  {Hardness: 0.2, Tool: Shovel, MinTier: TierWood, RequiresTool: true},
	"minecraft:snow":        {Hardness: 0.1, Tool: Shovel, MinTier: TierWood, RequiresTool: true},

	"minecraft:chest":          {Hardness: 2.5, Tool: Axe},
	"minecraft:trapped_chest":  {Hardness: 2.5, Tool: Axe},
	"minecraft:ender_chest":    {Hardness: 22.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true},
	"minecraft:barrel":         {Hardness: 2.5, Tool: Axe},
	"minecraft:crafting_table": {Hardness: 2.5, Tool: Axe},
	"minecraft:bookshelf":      {Hardness: 1.5, Tool: Axe},
	"minecraft:ladder":         {Hardness: 0.4, Tool: Axe},
	"minecraft:melon":          {Hardness: 1, Tool: Axe},
	"minecraft:pumpkin":        {Hardness: 1, Tool: Axe},
	"minecraft:carved_pumpkin": {Hardness: 1, Tool: Axe},
	"minecraft:jack_o_lantern": {Hardness: 1, Tool: Axe},
	"minecraft:note_block":     {Hardness: 0.8, Tool: Axe},
	"minecraft:jukebox":        {Hardness: 2, Tool: Axe},
	"minecraft:hay_block":      {Hardness: 0.5, Tool: Hoe},
	"minecraft:sponge":         {Hardness: 0.6, Tool: Hoe},
	"minecraft:wet_sponge":     {Hardness: 0.6, Tool: Hoe},
	"minecraft:moss_block":     {Hardness: 0.1, Tool: Hoe},
	"minecraft:cactus":         {Hardness: 0.4},
	"minecraft:cobweb":         {Hardness: 4, Tool: Sword, MinTier: TierWood, RequiresTool: true},
	"minecraft:cake":           {Hardness: 0.5},
	"minecraft:slime_block":    {Hardness: 0},
	"minecraft:honey_block":    {Hardness: 0},
	"minecraft:tnt":            {Hardness: 0},
	"minecraft:scaffolding":    {Hardness: 0},
	"minecraft:torch":          {Hardness: 0},
	"minecraft:soul_torch":     {Hardness: 0},
	"minecraft:redstone_torch": {Hardness: 0},
	"minecraft:lever":          {Hardness: 0.5},
	"minecraft:vine":           {Hardness: 0.2, Tool: Shears},
	"minecraft:glow_lichen":    {Hardness: 0.2, Tool: Shears},
}

// patternBreakInfo is consulted in order after the exact table; the first
// match wins, so put the more specific patterns first.
var patternBreakInfo = []struct {
	suffix, contains string
	info             BreakInfo
}{
	{suffix: "_concrete_powder", info: BreakInfo{Hardness: 0.5, Tool: Shovel}},
	{suffix: "_concrete", info: BreakInfo{Hardness: 1.8, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{suffix: "_glazed_terracotta", info: BreakInfo{Hardness: 1.4, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{suffix: "_terracotta", info: BreakInfo{Hardness: 1.25, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{suffix: "_wool", info: BreakInfo{Hardness: 0.8, Tool: Shears}},
	{suffix: "_carpet", info: BreakInfo{Hardness: 0.1}},
	{suffix: "_bed", info: BreakInfo{Hardness: 0.2}},
	{suffix: "_glass_pane", info: BreakInfo{Hardness: 0.3}},
	{suffix: "_glass", info: BreakInfo{Hardness: 0.3}},
	{suffix: "glass_pane", info: BreakInfo{Hardness: 0.3}},
	{suffix: "glass", info: BreakInfo{Hardness: 0.3}},
	{suffix: "_leaves", info: BreakInfo{Hardness: 0.2, Tool: Hoe}},
	{suffix: "_planks", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{suffix: "_log", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{suffix: "_wood", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{suffix: "_stem", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{suffix: "_hyphae", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{suffix: "_door", info: BreakInfo{Hardness: 3, Tool: Axe}},
	{suffix: "_trapdoor", info: BreakInfo{Hardness: 3, Tool: Axe}},
	{suffix: "_fence_gate", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{suffix: "_sign", info: BreakInfo{Hardness: 1, Tool: Axe}},
	{suffix: "_banner", info: BreakInfo{Hardness: 1, Tool: Axe}},
	{suffix: "_pressure_plate", info: BreakInfo{Hardness: 0.5}},
	{suffix: "_button", info: BreakInfo{Hardness: 0.5}},
	{suffix: "_shulker_box", info: BreakInfo{Hardness: 2}},
	{suffix: "shulker_box", info: BreakInfo{Hardness: 2}},
	{suffix: "_sapling", info: BreakInfo{Hardness: 0}},
	{suffix: "_mushroom", info: BreakInfo{Hardness: 0}},
	{suffix: "_coral", info: BreakInfo{Hardness: 0}},
	{suffix: "_coral_fan", info: BreakInfo{Hardness: 0}},
	{suffix: "_candle", info: BreakInfo{Hardness: 0.1}},
	// Wooden stairs/slabs/fences vs. stone ones: wood kinds are prefixed by
	// a tree name; anything else with these suffixes is masonry.
	{suffix: "_fence", contains: "nether_brick", info: BreakInfo{Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{suffix: "_fence", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{contains: "brick", info: BreakInfo{Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "cobble", info: BreakInfo{Hardness: 2, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "deepslate", info: BreakInfo{Hardness: 3.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "sandstone", info: BreakInfo{Hardness: 0.8, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "quartz", info: BreakInfo{Hardness: 0.8, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "purpur", info: BreakInfo{Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "prismarine", info: BreakInfo{Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "blackstone", info: BreakInfo{Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "basalt", info: BreakInfo{Hardness: 1.25, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "andesite", info: BreakInfo{Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "diorite", info: BreakInfo{Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "granite", info: BreakInfo{Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "stone", info: BreakInfo{Hardness: 1.5, Tool: Pickaxe, MinTier: TierWood, RequiresTool: true}},
	{contains: "copper", info: BreakInfo{Hardness: 3, Tool: Pickaxe, MinTier: TierStone, RequiresTool: true}},
	// Remaining wooden kinds (stairs/slabs named after their tree).
	{suffix: "_stairs", info: BreakInfo{Hardness: 2, Tool: Axe}},
	{suffix: "_slab", info: BreakInfo{Hardness: 2, Tool: Axe}},
	// Plants and other hand-instant decor.
	{contains: "grass", info: BreakInfo{Hardness: 0}},
	{contains: "flower", info: BreakInfo{Hardness: 0}},
	{contains: "tulip", info: BreakInfo{Hardness: 0}},
	{contains: "fern", info: BreakInfo{Hardness: 0}},
	{contains: "redstone", info: BreakInfo{Hardness: 0}},
}

// defaultBreakInfo is used for blocks no rule knows: breakable by hand in a
// moment, always drops.
var defaultBreakInfo = BreakInfo{Hardness: 1}

// BreakInfoFor returns how a block breaks. Air is instant.
func BreakInfoFor(name string) BreakInfo {
	if name == "" || name == "minecraft:air" {
		return BreakInfo{}
	}
	if info, ok := exactBreakInfo[name]; ok {
		return info
	}
	short := strings.TrimPrefix(name, "minecraft:")
	for _, r := range patternBreakInfo {
		if r.suffix != "" && !strings.HasSuffix(short, r.suffix) {
			continue
		}
		if r.contains != "" && !strings.Contains(short, r.contains) {
			continue
		}
		return r.info
	}
	return defaultBreakInfo
}

// Tool describes the held tool, parsed from its item id.
type Tool struct {
	Type ToolType
	Tier ToolTier
}

// ToolFromItem parses "minecraft:iron_pickaxe" → {Pickaxe, TierIron}. Any
// other item is NoTool.
func ToolFromItem(item string) Tool {
	short := strings.TrimPrefix(item, "minecraft:")
	if short == "shears" {
		return Tool{Type: Shears, Tier: TierWood}
	}
	material, kind, ok := strings.Cut(short, "_")
	if !ok {
		return Tool{}
	}
	var tier ToolTier
	switch material {
	case "wooden", "golden":
		tier = TierWood
	case "stone":
		tier = TierStone
	case "iron":
		tier = TierIron
	case "diamond":
		tier = TierDiamond
	case "netherite":
		tier = TierNetherite
	default:
		return Tool{}
	}
	switch kind {
	case "pickaxe":
		return Tool{Pickaxe, tier}
	case "axe":
		return Tool{Axe, tier}
	case "shovel":
		return Tool{Shovel, tier}
	case "hoe":
		return Tool{Hoe, tier}
	case "sword":
		return Tool{Sword, tier}
	}
	return Tool{}
}

// toolSpeed is the vanilla dig-speed multiplier of a tool used on a block
// of its class.
func toolSpeed(t Tool, item string) float64 {
	if t.Type == Shears {
		return 15 // shears on wool: effectively instant-ish (vanilla 5/15 by block)
	}
	switch t.Tier {
	case TierWood:
		if strings.HasPrefix(strings.TrimPrefix(item, "minecraft:"), "golden_") {
			return 12
		}
		return 2
	case TierStone:
		return 4
	case TierIron:
		return 6
	case TierDiamond:
		return 8
	case TierNetherite:
		return 9
	}
	return 1
}

// CanHarvest reports whether breaking the block with item yields a drop.
func CanHarvest(info BreakInfo, item string) bool {
	if !info.RequiresTool {
		return true
	}
	t := ToolFromItem(item)
	return t.Type == info.Tool && t.Tier >= info.MinTier
}

// DigContext carries the modifiers the vanilla dig formula applies on top
// of block hardness and the bare tool: enchantments on the tool/helmet,
// potion effects on the player, and the player's situation.
type DigContext struct {
	Efficiency    int  // tool's Efficiency level (0 = none)
	Haste         int  // Haste amplifier+1 (0 = none)
	MiningFatigue int  // Mining Fatigue amplifier+1 (0 = none)
	InWater       bool // head submerged
	AquaAffinity  bool // helmet enchant cancelling the water penalty
	OnGround      bool // airborne players dig 5× slower
}

// BreakTicks returns how many ticks a survival player needs to break the
// block while holding item with no modifiers (on the ground, in air): 0
// means instant, -1 means unbreakable. See BreakTicksWith.
func BreakTicks(info BreakInfo, item string) int {
	return BreakTicksWith(info, item, DigContext{OnGround: true})
}

// BreakTicksWith is the full 1.20.1 formula:
//
//	speed  = tool speed if the tool matches the block's class, else 1
//	       + (efficiency² + 1) when the tool matches and is enchanted
//	       × (1 + 0.2·haste)   × 0.3^fatigue
//	       ÷ 5 when submerged without Aqua Affinity   ÷ 5 when airborne
//	damage = speed / hardness / (30 if harvestable else 100) per tick
//
// Progress reaches 1 after ceil(1/damage) ticks; damage ≥ 1 is instant.
func BreakTicksWith(info BreakInfo, item string, ctx DigContext) int {
	if info.Hardness < 0 {
		return -1
	}
	if info.Hardness == 0 {
		return 0
	}
	speed := 1.0
	t := ToolFromItem(item)
	matches := t.Type != NoTool && t.Type == info.Tool
	if matches {
		speed = toolSpeed(t, item)
		if ctx.Efficiency > 0 {
			e := float64(ctx.Efficiency)
			speed += e*e + 1
		}
	}
	if ctx.Haste > 0 {
		speed *= 1 + 0.2*float64(ctx.Haste)
	}
	if ctx.MiningFatigue > 0 {
		speed *= math.Pow(0.3, math.Min(float64(ctx.MiningFatigue), 4))
	}
	if ctx.InWater && !ctx.AquaAffinity {
		speed /= 5
	}
	if !ctx.OnGround {
		speed /= 5
	}
	damage := speed / info.Hardness
	if CanHarvest(info, item) {
		damage /= 30
	} else {
		damage /= 100
	}
	if damage >= 1 {
		return 0
	}
	return int(math.Ceil(1 / damage))
}

// ToolDurability returns the maximum damage a tool item takes before it
// breaks, or 0 for items that don't wear (blocks, ingots, …).
func ToolDurability(item string) int {
	short := strings.TrimPrefix(item, "minecraft:")
	if short == "shears" {
		return 238
	}
	if short == "flint_and_steel" {
		return 64
	}
	t := ToolFromItem(item)
	if t.Type == NoTool {
		return 0
	}
	if strings.HasPrefix(short, "golden_") {
		return 32
	}
	switch t.Tier {
	case TierWood:
		return 59
	case TierStone:
		return 131
	case TierIron:
		return 250
	case TierDiamond:
		return 1561
	case TierNetherite:
		return 2031
	}
	return 0
}

// BlockDrop returns the item (and count) a harvested block leaves behind:
// the block itself for most, the vanilla substitute for the few that drop
// something else, and "" for blocks that drop nothing without silk touch.
func BlockDrop(name string) (item string, count int) {
	switch name {
	case "minecraft:stone":
		return "minecraft:cobblestone", 1
	case "minecraft:deepslate":
		return "minecraft:cobbled_deepslate", 1
	case "minecraft:grass_block", "minecraft:mycelium", "minecraft:podzol", "minecraft:dirt_path", "minecraft:farmland":
		return "minecraft:dirt", 1
	case "minecraft:coal_ore", "minecraft:deepslate_coal_ore":
		return "minecraft:coal", 1
	case "minecraft:iron_ore", "minecraft:deepslate_iron_ore":
		return "minecraft:raw_iron", 1
	case "minecraft:copper_ore", "minecraft:deepslate_copper_ore":
		return "minecraft:raw_copper", 3
	case "minecraft:gold_ore", "minecraft:deepslate_gold_ore":
		return "minecraft:raw_gold", 1
	case "minecraft:diamond_ore", "minecraft:deepslate_diamond_ore":
		return "minecraft:diamond", 1
	case "minecraft:emerald_ore", "minecraft:deepslate_emerald_ore":
		return "minecraft:emerald", 1
	case "minecraft:lapis_ore", "minecraft:deepslate_lapis_ore":
		return "minecraft:lapis_lazuli", 6
	case "minecraft:redstone_ore", "minecraft:deepslate_redstone_ore":
		return "minecraft:redstone", 5
	case "minecraft:nether_quartz_ore":
		return "minecraft:quartz", 1
	case "minecraft:nether_gold_ore":
		return "minecraft:gold_nugget", 4
	case "minecraft:clay":
		return "minecraft:clay_ball", 4
	case "minecraft:glowstone":
		return "minecraft:glowstone_dust", 3
	case "minecraft:sea_lantern":
		return "minecraft:prismarine_crystals", 3
	case "minecraft:bookshelf":
		return "minecraft:book", 3
	case "minecraft:snow_block":
		return "minecraft:snowball", 4
	case "minecraft:melon":
		return "minecraft:melon_slice", 5
	case "minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice", "minecraft:glass", "minecraft:glass_pane",
		"minecraft:cobweb", "minecraft:infested_stone", "minecraft:spawner":
		return "", 0
	}
	short := strings.TrimPrefix(name, "minecraft:")
	switch {
	case strings.HasSuffix(short, "_leaves"),
		strings.HasSuffix(short, "_glass"),
		strings.HasSuffix(short, "_glass_pane"),
		strings.HasSuffix(short, "_coral"),
		strings.HasSuffix(short, "_coral_fan"):
		return "", 0
	case short == "grass", short == "tall_grass", short == "fern", short == "large_fern", short == "seagrass":
		return "", 0
	}
	return name, 1
}

// exactBlastResistance lists vanilla blast resistance for blocks where it
// differs noticeably from hardness (explosion.go). Unlisted blocks fall back
// to a name pattern, then to their hardness.
var exactBlastResistance = map[string]float64{
	"minecraft:obsidian":          1200,
	"minecraft:crying_obsidian":   1200,
	"minecraft:anvil":             1200,
	"minecraft:enchanting_table":  1200,
	"minecraft:ender_chest":       600,
	"minecraft:water":             100,
	"minecraft:lava":              100,
	"minecraft:end_stone":         9,
	"minecraft:stone":             6,
	"minecraft:cobblestone":       6,
	"minecraft:mossy_cobblestone": 6,
	"minecraft:deepslate":         6,
	"minecraft:cobbled_deepslate": 6,
	"minecraft:bricks":            6,
	"minecraft:nether_bricks":     6,
	"minecraft:stone_bricks":      6,
	"minecraft:gold_block":        6,
	"minecraft:iron_block":        6,
	"minecraft:diamond_block":     6,
	"minecraft:emerald_block":     6,
	"minecraft:netherite_block":   1200,
	"minecraft:coal_block":        6,
	"minecraft:redstone_block":    6,
	"minecraft:quartz_block":      0.8,
	"minecraft:smooth_stone":      6,
	"minecraft:glowstone":         0.3,
	"minecraft:sea_lantern":       0.3,
	"minecraft:chest":             2.5,
	"minecraft:beacon":            3,
	"minecraft:netherrack":        0.4,
	"minecraft:magma_block":       0.5,
	"minecraft:ice":               0.5,
	"minecraft:packed_ice":        0.5,
	"minecraft:blue_ice":          2.8,
	"minecraft:sand":              0.5,
	"minecraft:gravel":            0.6,
	"minecraft:dirt":              0.5,
	"minecraft:grass_block":       0.6,
	"minecraft:tnt":               0,
}

// BlastResistance returns the block's vanilla blast resistance, used by the
// explosion ray march: each ray loses (resistance + 0.3) * 0.3 per block it
// crosses. Unbreakable blocks report an effectively infinite value.
func BlastResistance(name string) float64 {
	if r, ok := exactBlastResistance[name]; ok {
		return r
	}
	info := BreakInfoFor(name)
	if info.Hardness < 0 {
		return 3600000 // bedrock & co.
	}
	short := strings.TrimPrefix(name, "minecraft:")
	switch {
	case strings.HasSuffix(short, "_wool"):
		return 0.8
	case strings.HasSuffix(short, "_planks"):
		return 3
	case strings.HasSuffix(short, "_log"), strings.HasSuffix(short, "_wood"), strings.HasPrefix(short, "stripped_"):
		return 2
	case strings.HasSuffix(short, "_glass"), strings.HasSuffix(short, "_glass_pane"), short == "glass", short == "glass_pane":
		return 0.3
	case strings.HasSuffix(short, "_leaves"):
		return 0.2
	case strings.HasSuffix(short, "_bed"):
		return 0.2
	case strings.HasSuffix(short, "_concrete_powder"):
		return 0.5
	case strings.HasSuffix(short, "_concrete"):
		return 1.8
	case strings.HasSuffix(short, "_terracotta"), short == "terracotta":
		return 4.2
	case strings.HasSuffix(short, "_ore"):
		return 3
	case strings.HasSuffix(short, "_stairs"), strings.HasSuffix(short, "_slab"), strings.HasSuffix(short, "_wall"),
		strings.HasSuffix(short, "_bricks"), strings.Contains(short, "stone"), strings.Contains(short, "deepslate"),
		strings.HasSuffix(short, "_block"):
		return 6
	}
	return info.Hardness
}
