package world

import "strings"

// combat_items.go: vanilla 1.20.1 weapon damage, armor values and armor
// durability, keyed by item id. The server's combat code consults these
// when an instance runs with weapon damage enabled.

// AttackDamage is the raw melee damage of an item: fist 1; swords wood/gold
// 4, stone 5, iron 6, diamond 7, netherite 8; axes wood/gold 7, stone 9,
// iron 9, diamond 9, netherite 10; anything else 1.
func AttackDamage(item string) float32 {
	material, kind, ok := strings.Cut(strings.TrimPrefix(item, "minecraft:"), "_")
	if !ok {
		return 1
	}
	switch kind {
	case "sword":
		switch material {
		case "wooden", "golden":
			return 4
		case "stone":
			return 5
		case "iron":
			return 6
		case "diamond":
			return 7
		case "netherite":
			return 8
		}
	case "axe":
		switch material {
		case "wooden", "golden":
			return 7
		case "stone", "iron", "diamond":
			return 9
		case "netherite":
			return 10
		}
	}
	return 1
}

// armorPiece splits "minecraft:iron_chestplate" into ("iron", "chestplate").
// ok is false for anything that isn't armor.
func armorPiece(item string) (material, piece string, ok bool) {
	material, piece, ok = strings.Cut(strings.TrimPrefix(item, "minecraft:"), "_")
	if !ok {
		return "", "", false
	}
	switch piece {
	case "helmet", "chestplate", "leggings", "boots":
	default:
		return "", "", false
	}
	switch material {
	case "leather", "chainmail", "iron", "golden", "diamond", "netherite":
		return material, piece, true
	}
	return "", "", false
}

// ArmorPoints returns an armor item's defence points and toughness
// (helmet/chestplate/leggings/boots): leather 1/3/2/1, chainmail 2/5/4/1,
// iron 2/6/5/2, golden 2/5/3/1, diamond 3/8/6/3 (toughness 2), netherite
// 3/8/6/3 (toughness 3). Non-armor returns 0, 0.
func ArmorPoints(item string) (points, toughness float32) {
	material, piece, ok := armorPiece(item)
	if !ok {
		return 0, 0
	}
	table := map[string][4]float32{ // helmet, chestplate, leggings, boots
		"leather":   {1, 3, 2, 1},
		"chainmail": {2, 5, 4, 1},
		"iron":      {2, 6, 5, 2},
		"golden":    {2, 5, 3, 1},
		"diamond":   {3, 8, 6, 3},
		"netherite": {3, 8, 6, 3},
	}
	points = table[material][pieceIndex(piece)]
	switch material {
	case "diamond":
		toughness = 2
	case "netherite":
		toughness = 3
	}
	return points, toughness
}

func pieceIndex(piece string) int {
	switch piece {
	case "helmet":
		return 0
	case "chestplate":
		return 1
	case "leggings":
		return 2
	default:
		return 3
	}
}

// armorDurability returns the max damage of an armor item, or 0 for
// non-armor: leather 55/80/75/65, chainmail & iron 165/240/225/195, golden
// 77/112/105/91, diamond 363/528/495/429, netherite 407/592/555/481.
func armorDurability(item string) int {
	material, piece, ok := armorPiece(item)
	if !ok {
		return 0
	}
	table := map[string][4]int{
		"leather":   {55, 80, 75, 65},
		"chainmail": {165, 240, 225, 195},
		"iron":      {165, 240, 225, 195},
		"golden":    {77, 112, 105, 91},
		"diamond":   {363, 528, 495, 429},
		"netherite": {407, 592, 555, 481},
	}
	return table[material][pieceIndex(piece)]
}
