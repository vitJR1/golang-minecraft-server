package world

// replaceable.go: which blocks a placement may overwrite. Vanilla calls this
// BlockState.canBeReplaced — air, fluids and thin vegetation give way to a
// placed block; everything else must be dug first.

// replaceableBlocks are the names that give way to a placed block. Double
// plants (tall_grass, large_fern) are left out: overwriting one half would
// orphan the other. Snow layers are only replaceable at one layer, which a
// name can't tell, so snow is left out too.
var replaceableBlocks = map[string]bool{
	"minecraft:air":            true,
	"minecraft:cave_air":       true,
	"minecraft:void_air":       true,
	"minecraft:water":          true,
	"minecraft:lava":           true,
	"minecraft:grass":          true, // short grass (renamed short_grass in 1.20.3)
	"minecraft:fern":           true,
	"minecraft:dead_bush":      true,
	"minecraft:seagrass":       true,
	"minecraft:vine":           true,
	"minecraft:fire":           true,
	"minecraft:soul_fire":      true,
	"minecraft:light":          true,
	"minecraft:structure_void": true,
}

// Replaceable reports whether placing a block at b's position may overwrite
// b. A client that names any other target is either out of sync (someone
// else just built there) or forging the click.
func Replaceable(b Block) bool {
	return replaceableBlocks[b.Name]
}

// ReplacedInPlace reports whether a click on b places the new block into
// b's own position instead of against the clicked face: vanilla's
// "replacing clicked on block" (right-clicking grass puts the block where
// the grass was). Air and fluids are never the clicked block — the client's
// ray passes through them.
func ReplacedInPlace(b Block) bool {
	switch b.Name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air", "minecraft:water", "minecraft:lava":
		return false
	}
	return Replaceable(b)
}
