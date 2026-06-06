package world

// Block-entity type registry IDs for protocol 763 (1.20.1), in registry
// order, from ViaVersion mappings. Needed in the Chunk Data packet so the
// client renders block-entity blocks (beds, chests, banners, skulls, …),
// which are drawn by a BlockEntityRenderer and stay invisible otherwise.
//
// Generated data — regenerate on version bump.
var blockEntityTypeIDs = map[string]int32{
	"minecraft:furnace":                 0,
	"minecraft:chest":                   1,
	"minecraft:trapped_chest":           2,
	"minecraft:ender_chest":             3,
	"minecraft:jukebox":                 4,
	"minecraft:dispenser":               5,
	"minecraft:dropper":                 6,
	"minecraft:sign":                    7,
	"minecraft:hanging_sign":            8,
	"minecraft:mob_spawner":             9,
	"minecraft:piston":                  10,
	"minecraft:brewing_stand":           11,
	"minecraft:enchanting_table":        12,
	"minecraft:end_portal":              13,
	"minecraft:beacon":                  14,
	"minecraft:skull":                   15,
	"minecraft:daylight_detector":       16,
	"minecraft:hopper":                  17,
	"minecraft:comparator":              18,
	"minecraft:banner":                  19,
	"minecraft:structure_block":         20,
	"minecraft:end_gateway":             21,
	"minecraft:command_block":           22,
	"minecraft:shulker_box":             23,
	"minecraft:bed":                     24,
	"minecraft:conduit":                 25,
	"minecraft:barrel":                  26,
	"minecraft:smoker":                  27,
	"minecraft:blast_furnace":           28,
	"minecraft:lectern":                 29,
	"minecraft:bell":                    30,
	"minecraft:jigsaw":                  31,
	"minecraft:campfire":                32,
	"minecraft:beehive":                 33,
	"minecraft:sculk_sensor":            34,
	"minecraft:calibrated_sculk_sensor": 35,
	"minecraft:sculk_catalyst":          36,
	"minecraft:sculk_shrieker":          37,
	"minecraft:chiseled_bookshelf":      38,
	"minecraft:brushable_block":         39,
	"minecraft:decorated_pot":           40,
}

// BlockEntityTypeID returns the registry id for a block-entity type name
// ("minecraft:bed") and whether it is known.
func BlockEntityTypeID(name string) (int32, bool) {
	id, ok := blockEntityTypeIDs[name]
	return id, ok
}
