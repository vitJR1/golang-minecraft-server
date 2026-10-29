package enderchest

import (
	"testing"

	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

func TestEnderSlotsCodecRoundTrip(t *testing.T) {
	diamond, _ := world.ItemByName("minecraft:diamond")
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe")
	var slots [inventory.ChestSlots]inventory.Stack
	slots[0] = inventory.Stack{ID: diamond, Count: 3}
	slots[26] = inventory.Stack{ID: pick, Count: 1, Damage: 12, Enchantments: map[string]int{"minecraft:efficiency": 2}, Name: "Digger"}

	data, err := Encode(slots)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if back[0].ID != diamond || back[0].Count != 3 || back[0].Damage != 0 || back[0].Name != "" || len(back[0].Enchantments) != 0 {
		t.Errorf("slot 0: %+v", back[0])
	}
	if back[26].ID != pick || back[26].Damage != 12 || back[26].Name != "Digger" || back[26].Enchantments["minecraft:efficiency"] != 2 {
		t.Errorf("slot 26: %+v", back[26])
	}
	for i := 1; i < 26; i++ {
		if !back[i].Empty() {
			t.Errorf("slot %d should be empty: %+v", i, back[i])
		}
	}
	// Unknown items and out-of-range slots are skipped, not fatal.
	odd, err := Decode([]byte(`[{"slot":40,"item":"minecraft:diamond","count":1},{"slot":1,"item":"minecraft:nope","count":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range odd {
		if !st.Empty() {
			t.Errorf("slot %d unexpectedly set: %+v", i, st)
		}
	}
	if _, err := Decode([]byte(`garbage`)); err == nil {
		t.Error("corrupt data should error")
	}
	empty, err := Decode(nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range empty {
		if !st.Empty() {
			t.Errorf("nil data: slot %d set: %+v", i, st)
		}
	}
}
