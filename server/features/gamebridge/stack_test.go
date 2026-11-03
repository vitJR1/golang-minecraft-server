package gamebridge

import (
	"testing"

	"minecraft-server/game"
)

func TestStackRoundTrip(t *testing.T) {
	in := game.ItemStack{
		Item: "minecraft:iron_sword", Count: 1, Name: "§cBlade", Lore: []string{"sharp"},
		Enchantments: map[string]int{"minecraft:sharpness": 2, "minecraft:knockback": 0},
		Damage:       3, NoDrop: true, Slot: 36,
	}
	st, ok := FromGame(in)
	if !ok {
		t.Fatal("known item refused")
	}
	if _, kept := st.Enchantments["minecraft:knockback"]; kept {
		t.Error("a level-0 enchantment must be dropped")
	}
	out := ToGame(st, 36)
	if out.Item != in.Item || out.Count != 1 || out.Name != in.Name || out.Damage != 3 ||
		!out.NoDrop || out.Slot != 36 || out.Enchantments["minecraft:sharpness"] != 2 || len(out.Lore) != 1 {
		t.Fatalf("round trip: %+v", out)
	}
	if _, ok := FromGame(game.ItemStack{Item: "minecraft:nope", Count: 1}); ok {
		t.Error("unknown item accepted")
	}
	if st, _ := FromGame(game.ItemStack{Item: "minecraft:stone", Count: 200}); st.Count != 64 {
		t.Errorf("count clamp: %d", st.Count)
	}
}
