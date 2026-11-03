// Package enchant is the enchantments the server understands and where
// their levels come from. Source is pluggable on the Server
// (Server.Enchantments); the default, NBT, reads them from the item's own
// NBT ("Enchantments" list) — what a creative-given or /give'd enchanted
// item carries. Efficiency speeds digging up, Unbreaking spares durability,
// Aqua Affinity (helmet) cancels the underwater dig penalty; Sharpness,
// Protection, Knockback, Power, Punch and Infinity are combat and bows.
package enchant

import (
	"math/rand"

	"minecraft-server/server/gameplay/inventory"
)

// Enchantment ids.
const (
	Efficiency   = "minecraft:efficiency"
	Unbreaking   = "minecraft:unbreaking"
	AquaAffinity = "minecraft:aqua_affinity"
	Sharpness    = "minecraft:sharpness"
	Protection   = "minecraft:protection"
	Knockback    = "minecraft:knockback"
	Power        = "minecraft:power"
	Punch        = "minecraft:punch"
	Infinity     = "minecraft:infinity"
)

// Source answers "what level of enchantment id does this stack have".
type Source interface {
	Level(st inventory.Stack, id string) int
}

// NBT is the default Source: levels parsed from the item's NBT.
type NBT struct{}

func (NBT) Level(st inventory.Stack, id string) int { return st.EnchantLevel(id) }

// UnbreakingSpares rolls the Unbreaking chance: a level-lvl item skips
// lvl/(lvl+1) of its wear.
func UnbreakingSpares(lvl int) bool { return rand.Intn(lvl+1) > 0 }
