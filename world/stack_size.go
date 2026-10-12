package world

import "strings"

// MaxStackSize is the vanilla stack ceiling for plain items (ingots,
// blocks, …). MaxStackSizeOf narrows it for the items that stack to 16 or 1.
const MaxStackSize = 64

// MaxStackSizeOf returns the vanilla max stack size of a namespaced item: 1
// for tools, weapons, armour, bows, shears, buckets and potions; 16 for
// ender pearls, eggs, snowballs and signs; 64 otherwise.
func MaxStackSizeOf(name string) int {
	short := strings.TrimPrefix(name, "minecraft:")
	switch short {
	case "ender_pearl", "egg", "snowball", "oak_sign", "armor_stand", "honey_bottle":
		return 16
	case "bow", "crossbow", "shears", "shield", "fishing_rod", "flint_and_steel", "potion",
		"splash_potion", "lingering_potion", "milk_bucket", "bucket", "water_bucket", "lava_bucket",
		"elytra", "trident", "totem_of_undying", "saddle", "enchanted_book", "written_book":
		return 1
	}
	if ToolDurability(name) > 0 {
		return 1 // every damageable item (tools, weapons, armour) is unstackable
	}
	return MaxStackSize
}
