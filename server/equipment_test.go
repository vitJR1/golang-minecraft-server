package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

// readEquipment parses a Set Equipment body into entity id + slot→stack.
func readEquipment(t *testing.T, body []byte) (int32, map[byte]inventory.Stack) {
	t.Helper()
	buf := bytes.NewBuffer(body)
	eid, _ := protocol.ReadVarInt(buf)
	out := map[byte]inventory.Stack{}
	for {
		slot, err := buf.ReadByte()
		if err != nil {
			t.Fatal("truncated equipment")
		}
		st, ok := inventory.ReadSlot(buf)
		if !ok {
			t.Fatal("bad slot")
		}
		out[slot&0x7F] = st
		if slot&0x80 == 0 {
			break
		}
	}
	return int32(eid), out
}

func TestEquipmentBroadcastToOthersOnly(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	alice := tntConn(inst, "Alice", player.Survival, 0, 64, 0)
	bob := tntConn(inst, "Bob", player.Survival, 2, 64, 0)

	sword, _ := world.ItemByName("minecraft:iron_sword")
	helmet, _ := world.ItemByName("minecraft:iron_helmet")
	alice.inv.Set(inventory.HotbarStart+2, inventory.Stack{ID: sword, Count: 1, Enchantments: map[string]int{"minecraft:sharpness": 2}})
	alice.inv.Set(armorHelmetSlot, inventory.Stack{ID: helmet, Count: 1})
	alice.heldSlot.Store(2)
	alice.equipmentChanged()

	body := lastPacket(t, bob, packetid.CbPlaySetEquipment)
	if body == nil {
		t.Fatal("Bob should receive Alice's equipment")
	}
	eid, eq := readEquipment(t, body)
	if eid != alice.player.EntityID {
		t.Errorf("entity id %d, want Alice's %d", eid, alice.player.EntityID)
	}
	if eq[equipMainHand].ID != sword || eq[equipMainHand].EnchantLevel("minecraft:sharpness") != 2 {
		t.Errorf("main hand: %+v", eq[equipMainHand])
	}
	if eq[equipHelmet].ID != helmet {
		t.Errorf("helmet: %+v", eq[equipHelmet])
	}
	if !eq[equipBoots].Empty() || !eq[equipOffHand].Empty() {
		t.Error("empty slots must be sent empty")
	}
	if len(eq) != 6 {
		t.Errorf("all six slots expected, got %d", len(eq))
	}
	if lastPacket(t, alice, packetid.CbPlaySetEquipment) != nil {
		t.Error("the player must not receive their own equipment packet")
	}
}

func TestOthersEquipmentSentOnJoin(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	alice := tntConn(inst, "Alice", player.Survival, 0, 64, 0)
	chest, _ := world.ItemByName("minecraft:diamond_chestplate")
	alice.inv.Set(armorChestplateSlot, inventory.Stack{ID: chest, Count: 1})

	bob := tntConn(inst, "Bob", player.Survival, 2, 64, 0)
	bob.sendOthersEquipment()
	body := lastPacket(t, bob, packetid.CbPlaySetEquipment)
	if body == nil {
		t.Fatal("joining player should get the others' equipment")
	}
	eid, eq := readEquipment(t, body)
	if eid != alice.player.EntityID || eq[equipChestplate].ID != chest {
		t.Errorf("got eid=%d eq=%+v", eid, eq)
	}
}

func TestHeldSlotChangeRebroadcastsEquipment(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	alice := tntConn(inst, "Alice", player.Survival, 0, 64, 0)
	bob := tntConn(inst, "Bob", player.Survival, 2, 64, 0)
	pick, _ := world.ItemByName("minecraft:iron_pickaxe")
	alice.inv.Set(inventory.HotbarStart+1, inventory.Stack{ID: pick, Count: 1})

	// Consuming the held stack and swapping the held slot both notify.
	alice.heldSlot.Store(1)
	alice.swapHeld("minecraft:bucket")
	_, eq := readEquipment(t, lastPacket(t, bob, packetid.CbPlaySetEquipment))
	bucket, _ := world.ItemByName("minecraft:bucket")
	if eq[equipMainHand].ID != bucket {
		t.Errorf("after swapHeld main hand = %+v, want bucket", eq[equipMainHand])
	}
}
