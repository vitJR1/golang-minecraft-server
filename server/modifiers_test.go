package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/nbt"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/server/effect"
	"minecraft-server/server/enchant"
	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

func TestSlotNBTRoundTripsDamageAndEnchantments(t *testing.T) {
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe")
	st := inventory.Stack{ID: pick, Count: 1, Damage: 12, Enchantments: map[string]int{enchant.Efficiency: 4, enchant.Unbreaking: 3}}
	var buf bytes.Buffer
	inventory.WriteSlot(&buf, st)
	back, ok := inventory.ReadSlot(&buf)
	if !ok {
		t.Fatal("readSlot failed")
	}
	if back.ID != pick || back.Damage != 12 || back.EnchantLevel(enchant.Efficiency) != 4 || back.EnchantLevel(enchant.Unbreaking) != 3 {
		t.Errorf("round trip: %+v", back)
	}
	// A client-sent slot with a Short lvl (vanilla) parses too.
	tag := nbt.Compound{"Enchantments": nbt.List{ElemTag: nbt.TagCompound, Items: []nbt.Value{
		nbt.Compound{"id": nbt.String(enchant.Efficiency), "lvl": nbt.Short(5)},
	}}}
	buf.Reset()
	buf.Write(protocol.WriteSlotTagged(pick, 1, tag))
	back, _ = inventory.ReadSlot(&buf)
	if back.EnchantLevel(enchant.Efficiency) != 5 {
		t.Errorf("vanilla NBT: %+v", back)
	}
}

func TestEfficiencyAndHasteShortenTheDig(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Ench", player.Survival, 0, 64, 0)
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe")
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: pick, Count: 1})
	stone := world.BreakInfoFor("minecraft:stone")

	base := world.BreakTicksWith(stone, c.heldItemName(), c.digContext())
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: pick, Count: 1, Enchantments: map[string]int{enchant.Efficiency: 5}})
	withEff := world.BreakTicksWith(stone, c.heldItemName(), c.digContext())
	inst.Server.effects.apply(c, effect.Haste, 2, time.Minute)
	withBoth := world.BreakTicksWith(stone, c.heldItemName(), c.digContext())
	if !(withBoth < withEff && withEff < base) {
		t.Errorf("ticks base=%d efficiency=%d +haste=%d: each modifier must shorten the dig", base, withEff, withBoth)
	}
	if inst.Server.Effects.Level(c, effect.Haste) != 2 {
		t.Error("haste level not readable")
	}
	inst.Server.effects.remove(c, &effect.Haste)
	if inst.Server.Effects.Level(c, effect.Haste) != 0 {
		t.Error("haste should be removed")
	}
	inst.Server.effects.apply(c, effect.MiningFatigue, 1, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if inst.Server.Effects.Level(c, effect.MiningFatigue) != 0 {
		t.Error("expired effect must read as 0")
	}
}

func TestAirAndWaterSlowTheDig(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.World.SetBlock(world.Position{X: 0, Y: 63, Z: 0}, world.Stone) // floor
	c := offlineConn(inst, "Diver", player.Survival, 0.5, 64, 0.5)
	ctx := c.digContext()
	if !ctx.OnGround || ctx.InWater {
		t.Fatalf("baseline context: %+v", ctx)
	}
	// Airborne is the server's call from the blocks: a packet claiming
	// "on ground" doesn't matter.
	c.player.MoveTo(0.5, 66, 0.5, true)
	if c.digContext().OnGround {
		t.Error("airborne player should not be on ground")
	}
	c.player.MoveTo(0.5, 64, 0.5, false)
	inst.World.SetBlock(world.Position{X: 0, Y: 65, Z: 0}, world.Water) // eye level
	if !c.digContext().InWater {
		t.Error("head in water should be detected")
	}
}

func TestToolWearsAndBreaks(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Wearer", player.Survival, 0, 64, 0)
	axe, _ := world.ItemByName("minecraft:golden_axe") // durability 32
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: axe, Count: 1, Damage: 30})
	planks, _ := world.BlockByName("minecraft:oak_planks")

	pos := world.Position{X: 5, Y: 70, Z: 5}
	inst.World.SetBlock(pos, planks)
	c.breakBlock(pos, true)
	if st := c.inv.Get(inventory.HotbarStart); st.Damage != 31 {
		t.Fatalf("after one block: damage %d, want 31", st.Damage)
	}
	inst.World.SetBlock(pos, planks)
	c.breakBlock(pos, true)
	if st := c.inv.Get(inventory.HotbarStart); !st.Empty() {
		t.Errorf("axe should have broken, got %+v", st)
	}
	plankItem, _ := world.ItemByName("minecraft:oak_planks")
	if itemsIn(inst)[plankItem] != 2 {
		t.Errorf("both blocks should have dropped: %v", itemsIn(inst))
	}

	// Unbreaking III spares ~3/4 of the wear; blocks (non-tools) never wear.
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe") // durability 1561
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: pick, Count: 1, Enchantments: map[string]int{enchant.Unbreaking: 3}})
	for i := 0; i < 400; i++ {
		c.damageHeldTool(1)
		drain(c)
	}
	if d := c.inv.Get(inventory.HotbarStart).Damage; d < 50 || d > 160 {
		t.Errorf("unbreaking III should spare ~3/4 of 400 hits, damage=%d", d)
	}
	stoneItem, _ := world.ItemByName("minecraft:stone")
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: stoneItem, Count: 5})
	c.damageHeldTool(1)
	if st := c.inv.Get(inventory.HotbarStart); st.Damage != 0 || st.Count != 5 {
		t.Errorf("blocks must not wear: %+v", st)
	}
}
