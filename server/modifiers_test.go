package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/nbt"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

func TestSlotNBTRoundTripsDamageAndEnchantments(t *testing.T) {
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe")
	st := itemStack{ID: pick, Count: 1, Damage: 12, Enchantments: map[string]int{EnchantEfficiency: 4, EnchantUnbreaking: 3}}
	var buf bytes.Buffer
	writeStack(&buf, st)
	back, ok := readSlot(&buf)
	if !ok {
		t.Fatal("readSlot failed")
	}
	if back.ID != pick || back.Damage != 12 || back.enchantLevel(EnchantEfficiency) != 4 || back.enchantLevel(EnchantUnbreaking) != 3 {
		t.Errorf("round trip: %+v", back)
	}
	// A client-sent slot with a Short lvl (vanilla) parses too.
	tag := nbt.Compound{"Enchantments": nbt.List{ElemTag: nbt.TagCompound, Items: []nbt.Value{
		nbt.Compound{"id": nbt.String(EnchantEfficiency), "lvl": nbt.Short(5)},
	}}}
	buf.Reset()
	buf.Write(protocol.WriteSlotTagged(pick, 1, tag))
	back, _ = readSlot(&buf)
	if back.enchantLevel(EnchantEfficiency) != 5 {
		t.Errorf("vanilla NBT: %+v", back)
	}
}

func TestEfficiencyAndHasteShortenTheDig(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Ench")
	cli.startDiscardDrain()
	c := findConn(t, s, "Ench")
	c.player.SetGamemode(player.Survival)
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe")
	c.inv.set(hotbarStart, itemStack{ID: pick, Count: 1})
	stone := world.BreakInfoFor("minecraft:stone")

	base := world.BreakTicksWith(stone, c.heldItemName(), c.digContext())
	c.inv.set(hotbarStart, itemStack{ID: pick, Count: 1, Enchantments: map[string]int{EnchantEfficiency: 5}})
	withEff := world.BreakTicksWith(stone, c.heldItemName(), c.digContext())
	s.effects.apply(c, EffectHaste, 2, time.Minute)
	withBoth := world.BreakTicksWith(stone, c.heldItemName(), c.digContext())
	if !(withBoth < withEff && withEff < base) {
		t.Errorf("ticks base=%d efficiency=%d +haste=%d: each modifier must shorten the dig", base, withEff, withBoth)
	}
	if s.Effects.Level(c, EffectHaste) != 2 {
		t.Error("haste level not readable")
	}
	s.effects.remove(c, &EffectHaste)
	if s.Effects.Level(c, EffectHaste) != 0 {
		t.Error("haste should be removed")
	}
	s.effects.apply(c, EffectMiningFatigue, 1, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if s.Effects.Level(c, EffectMiningFatigue) != 0 {
		t.Error("expired effect must read as 0")
	}
}

func TestEffectCommand(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Op")
	cli.startDiscardDrain()
	c := findConn(t, s, "Op")
	s.Ops.Add("Op")
	s.RunCommand(c, "effect Op haste 2 60")
	if s.Effects.Level(c, EffectHaste) != 2 {
		t.Errorf("/effect did not apply haste II: %d", s.Effects.Level(c, EffectHaste))
	}
	s.RunCommand(c, "effect Op clear")
	if s.Effects.Level(c, EffectHaste) != 0 {
		t.Error("/effect clear did not clear")
	}
}

func TestAirAndWaterSlowTheDig(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Diver")
	cli.startDiscardDrain()
	c := findConn(t, s, "Diver")
	c.player.MoveTo(0.5, 64, 0.5, true)
	ctx := c.digContext()
	if !ctx.OnGround || ctx.InWater {
		t.Fatalf("baseline context: %+v", ctx)
	}
	c.player.MoveTo(0.5, 64, 0.5, false)
	if c.digContext().OnGround {
		t.Error("airborne player should not be on ground")
	}
	s.Hub.World.SetBlock(world.Position{X: 0, Y: 65, Z: 0}, world.Water) // eye level
	if !c.digContext().InWater {
		t.Error("head in water should be detected")
	}
}

func TestToolWearsAndBreaks(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Wearer")
	cli.startDiscardDrain()
	c := findConn(t, s, "Wearer")
	c.player.SetGamemode(player.Survival)
	axe, _ := world.ItemByName("minecraft:golden_axe") // durability 32
	c.inv.set(hotbarStart, itemStack{ID: axe, Count: 1, Damage: 30})
	planks, _ := world.BlockByName("minecraft:oak_planks")

	pos := world.Position{X: 5, Y: 70, Z: 5}
	s.Hub.World.SetBlock(pos, planks)
	c.breakBlock(pos, true)
	if st := c.inv.get(hotbarStart); st.Damage != 31 {
		t.Fatalf("after one block: damage %d, want 31", st.Damage)
	}
	s.Hub.World.SetBlock(pos, planks)
	c.breakBlock(pos, true)
	if st := c.inv.get(hotbarStart); !st.empty() {
		t.Errorf("axe should have broken, got %+v", st)
	}
	plankItem, _ := world.ItemByName("minecraft:oak_planks")
	if itemsIn(s.Hub)[plankItem] != 2 {
		t.Errorf("both blocks should have dropped: %v", itemsIn(s.Hub))
	}

	// Unbreaking III spares ~3/4 of the wear; blocks (non-tools) never wear.
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe") // durability 1561
	c.inv.set(hotbarStart, itemStack{ID: pick, Count: 1, Enchantments: map[string]int{EnchantUnbreaking: 3}})
	for i := 0; i < 400; i++ {
		c.damageHeldTool(1)
	}
	if d := c.inv.get(hotbarStart).Damage; d < 50 || d > 160 {
		t.Errorf("unbreaking III should spare ~3/4 of 400 hits, damage=%d", d)
	}
	stoneItem, _ := world.ItemByName("minecraft:stone")
	c.inv.set(hotbarStart, itemStack{ID: stoneItem, Count: 5})
	c.damageHeldTool(1)
	if st := c.inv.get(hotbarStart); st.Damage != 0 || st.Count != 5 {
		t.Errorf("blocks must not wear: %+v", st)
	}
}
