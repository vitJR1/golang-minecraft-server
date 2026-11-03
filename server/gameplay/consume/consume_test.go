package consume

import "testing"

func TestConsumable(t *testing.T) {
	if !Consumable("minecraft:golden_apple") || !Consumable("minecraft:potion") || Consumable("minecraft:bread") {
		t.Error("Consumable")
	}
	if Sound("minecraft:potion") != "minecraft:entity.generic.drink" || Sound("minecraft:golden_apple") != "minecraft:entity.player.burp" {
		t.Error("Sound")
	}
}
