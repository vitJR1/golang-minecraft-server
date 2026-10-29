package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

func TestGiveItemFillsHotbarBeforeMainInventory(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Picker", player.Survival, 0, 64, 0)

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	sword, _ := world.ItemByName("minecraft:iron_sword")

	// Empty inventory: the first pickup goes to hotbar slot 0.
	if left := c.giveItem(iron, 3); left != 0 {
		t.Fatalf("leftover: %d", left)
	}
	if st := c.inv.Get(inventory.HotbarStart); st.ID != iron || st.Count != 3 {
		t.Errorf("hotbar slot 0: %+v, want 3 iron", st)
	}
	if st := c.inv.Get(inventory.MainStart); !st.Empty() {
		t.Errorf("main inventory must stay empty, got %+v", st)
	}

	// A different item takes the next free hotbar slot.
	c.giveItem(sword, 1)
	if st := c.inv.Get(inventory.HotbarStart + 1); st.ID != sword {
		t.Errorf("hotbar slot 1: %+v, want sword", st)
	}

	// Existing partial stacks are topped up (hotbar stack first, then a
	// main-inventory stack) before any empty slot is used.
	c.inv.Set(inventory.MainStart+5, inventory.Stack{ID: iron, Count: 60})
	c.giveItem(iron, 65)
	if st := c.inv.Get(inventory.HotbarStart); st.Count != 64 {
		t.Errorf("hotbar stack should be topped up to 64 first, got %d", st.Count)
	}
	if st := c.inv.Get(inventory.MainStart + 5); st.Count != 64 {
		t.Errorf("main stack should be topped up to 64 next, got %d", st.Count)
	}
	if st := c.inv.Get(inventory.HotbarStart + 2); !st.Empty() {
		t.Errorf("no leftover iron expected in a new slot, got %+v", st)
	}

	// Hotbar full → a new item spills into the main inventory, first free slot.
	for slot := int16(inventory.HotbarStart + 2); slot < inventory.HotbarStart+9; slot++ {
		c.inv.Set(slot, inventory.Stack{ID: sword, Count: 1})
	}
	diamond, _ := world.ItemByName("minecraft:diamond")
	c.giveItem(diamond, 1)
	if st := c.inv.Get(inventory.MainStart); st.ID != diamond {
		t.Errorf("overflow should land in main slot 9, got %+v", st)
	}
}

func TestCountAndTakeItem(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Buyer", player.Survival, 0, 64, 0)

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: iron, Count: 10})
	c.inv.Set(inventory.MainStart+3, inventory.Stack{ID: iron, Count: 20})
	if got := c.countItem(iron); got != 30 {
		t.Fatalf("count: %d, want 30", got)
	}
	if c.takeItem(iron, 31) {
		t.Error("taking more than held must fail")
	}
	if got := c.countItem(iron); got != 30 {
		t.Errorf("failed take must not change inventory, got %d", got)
	}
	// Main inventory is drained before the hotbar.
	if !c.takeItem(iron, 25) {
		t.Fatal("take 25 should succeed")
	}
	if st := c.inv.Get(inventory.MainStart + 3); !st.Empty() {
		t.Errorf("main stack should be gone, got %+v", st)
	}
	if st := c.inv.Get(inventory.HotbarStart); st.Count != 5 {
		t.Errorf("hotbar stack: %d, want 5", st.Count)
	}
	if got := c.countItem(iron); got != 5 {
		t.Errorf("count after take: %d, want 5", got)
	}
}

func TestNavigatorStrippedOnGameJoinAndKeptInHub(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	t.Cleanup(arena.Stop)
	s.AddInstance(arena)

	c := offlineHub(s, "Gamer")
	if c.inv.Get(inventory.HotbarStart).Name != inventory.NavigatorName {
		t.Fatal("hub join should hand out the navigator")
	}

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.giveItem(iron, 5) // lands in hotbar slot 1 (slot 0 holds the rod)

	if err := s.MovePlayer(c, arena, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	if st := c.inv.Get(inventory.HotbarStart); !st.Empty() {
		t.Errorf("navigator should be stripped in a game instance, got %+v", st)
	}
	if st := c.inv.Get(inventory.HotbarStart + 1); st.ID != iron || st.Count != 5 {
		t.Errorf("real items must survive the move, got %+v", st)
	}

	// Back to the hub: the game is over — the loot is gone and the rod is
	// handed out again.
	if err := s.MovePlayer(c, s.Hub, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	if c.inv.Get(inventory.HotbarStart).Name != inventory.NavigatorName {
		t.Error("hub join should hand out the navigator again")
	}
	if st := c.inv.Get(inventory.HotbarStart + 1); !st.Empty() {
		t.Errorf("game loot must not follow the player into the hub: %+v", st)
	}
}

func TestReturnToHubResetsSpectatorAndState(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	t.Cleanup(arena.Stop)
	s.AddInstance(arena)

	c := offlineHub(s, "Loser")
	if err := s.MovePlayer(c, arena, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	// What a lost BedWars round leaves behind: spectator, kit, effects, low HP.
	c.player.SetGamemode(player.Spectator)
	sword, _ := world.ItemByName("minecraft:diamond_sword")
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: sword, Count: 1})
	c.applyEffect(EffectSpeed, 2, time.Minute)
	c.player.SetHealth(3)

	if err := s.MovePlayer(c, s.Hub, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	pkts := drainPackets(t, c)
	if gm := c.gamemode(); gm != player.Adventure {
		t.Errorf("gamemode in hub %v, want Adventure", gm)
	}
	if h := c.player.Health(); h != player.MaxHealth {
		t.Errorf("health %v, want full", h)
	}
	if c.effectLevel(EffectSpeed) != 0 {
		t.Error("effects must be cleared")
	}
	if st := c.inv.Get(inventory.HotbarStart); st.Name != inventory.NavigatorName {
		t.Errorf("hotbar 0 should be the navigator, got %+v", st)
	}
	for slot := int16(0); slot < inventory.PlayerSlots; slot++ {
		if st := c.inv.Get(slot); !st.Empty() && st.Name != inventory.NavigatorName {
			t.Errorf("slot %d still holds %+v", slot, st)
		}
	}
	// The Respawn packet must already carry Adventure, and the hearts are re-sent.
	var respawns int
	for _, p := range pkts {
		if p.id == packetid.CbPlayRespawn {
			respawns++
		}
	}
	if respawns == 0 || countIDs(pkts, packetid.CbPlaySetHealth) == 0 {
		t.Errorf("respawn=%d setHealth=%d", respawns, countIDs(pkts, packetid.CbPlaySetHealth))
	}

	// A dead player (custom respawn left them Dead) comes back alive too.
	if err := s.MovePlayer(c, arena, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	c.player.ApplyDamage(player.MaxHealth*10, 0, 0)
	if !c.player.IsDead() {
		t.Fatal("setup: player should be dead")
	}
	if err := s.MovePlayer(c, s.Hub, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	if c.player.IsDead() || c.player.Health() != player.MaxHealth {
		t.Error("returning to the hub must revive the player")
	}
}

func TestWindowZeroClickUpdatesModel(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Mover", player.Survival, 0, 64, 0)

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: iron, Count: 1})
	c.inv.Set(inventory.HotbarStart+1, inventory.Stack{ID: iron, Count: 63})

	// Pick up the single iron, then left-click the 63 stack: they merge.
	clickSlot(t, c, int16(inventory.HotbarStart), 0, 0,
		map[int16]inventory.Stack{int16(inventory.HotbarStart): {}}, inventory.Stack{ID: iron, Count: 1})
	clickSlot(t, c, int16(inventory.HotbarStart+1), 0, 0,
		map[int16]inventory.Stack{int16(inventory.HotbarStart + 1): {ID: iron, Count: 64}}, inventory.Stack{})
	if !c.inv.Get(inventory.HotbarStart).Empty() || c.inv.Get(inventory.HotbarStart+1).Count != 64 {
		t.Errorf("model after merge: %+v / %+v", c.inv.Get(inventory.HotbarStart), c.inv.Get(inventory.HotbarStart+1))
	}
	if got := c.countItem(iron); got != 64 {
		t.Errorf("count after merge: %d", got)
	}
}

func TestMenuWindowMirrorsRealInventory(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Shopper", player.Survival, 0, 64, 0)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.Set(inventory.MainStart+2, inventory.Stack{ID: iron, Count: 7})
	c.inv.Set(inventory.HotbarStart+4, inventory.Stack{ID: iron, Count: 9})

	var buf bytes.Buffer
	c.writeInventoryMirror(&buf)
	// 36 wire slots: parse them back and check the two stacks are present
	// at the expected positions (main index 2, hotbar index 4 → 27+4).
	for i := 0; i < 36; i++ {
		st, ok := inventory.ReadSlot(&buf)
		if !ok {
			t.Fatalf("slot %d unreadable", i)
		}
		switch i {
		case 2:
			if st.ID != iron || st.Count != 7 {
				t.Errorf("main slot 2: %+v", st)
			}
		case 27 + 4:
			if st.ID != iron || st.Count != 9 {
				t.Errorf("hotbar slot 4: %+v", st)
			}
		default:
			if !st.Empty() {
				t.Errorf("slot %d should be empty, got %+v", i, st)
			}
		}
	}
}
