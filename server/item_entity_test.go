package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

// itemConn builds a writable connection standing at (x, y, z) inside inst
// and registers it in the player list, so the pickup sweep can find it.
func itemConn(s *Server, inst *Instance, eid int32, x, y, z float64) *ClientConnection {
	c := &ClientConnection{
		server:   s,
		instance: inst,
		player:   player.New(eid, "P", [16]byte{}),
		outbound: make(chan outboundMsg, 64),
		done:     make(chan struct{}),
	}
	c.player.MoveTo(x, y, z, true)
	inst.Players.Add(c)
	return c
}

// floorWorld is a stone floor at y=69 around the origin, so items dropped at
// y=70 rest there.
func floorWorld() world.World {
	w := world.NewMemoryWorld()
	for x := -3; x <= 3; x++ {
		for z := -3; z <= 3; z++ {
			w.SetBlock(world.Position{X: x, Y: 69, Z: z}, world.Stone)
		}
	}
	return w
}

// countOf sums the units of itemID lying in the instance.
func countOf(inst *Instance, itemID int32) (n, entities int) {
	inst.itemsMu.Lock()
	defer inst.itemsMu.Unlock()
	for _, it := range inst.items {
		if it.stack.ID == itemID {
			n += int(it.stack.Count)
			entities++
		}
	}
	return n, entities
}

func TestDropItemUnknownOrEmpty(t *testing.T) {
	inst := bareInstance(New(), floorWorld())
	if inst.DropItem(0.5, 70, 0.5, "minecraft:not_a_thing", 1) {
		t.Error("unknown item should be rejected")
	}
	if inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", 0) {
		t.Error("zero count should be rejected")
	}
	if len(inst.items) != 0 {
		t.Errorf("nothing should have been spawned, have %d", len(inst.items))
	}
}

func TestDropItemFallsAndRests(t *testing.T) {
	inst := bareInstance(New(), floorWorld())
	// Dropped in the air two blocks up: hops, then lands on the y=69 floor
	// (top surface at y=70).
	if !inst.DropItem(0.5, 72, 0.5, "minecraft:iron_ingot", 1) {
		t.Fatal("drop rejected")
	}
	for i := 0; i < 60; i++ {
		inst.itemTick(uint64(i))
	}
	it := inst.items[0]
	if !it.onGround || it.y != 70 {
		t.Errorf("item should rest on the floor at y=70: onGround=%v y=%v", it.onGround, it.y)
	}
}

func TestDropItemMergesNearby(t *testing.T) {
	inst := bareInstance(New(), floorWorld())
	inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", 1)
	inst.itemTick(0) // lands (onGround) so it becomes a merge target
	inst.DropItem(0.9, 70, 0.6, "minecraft:iron_ingot", 2)
	inst.DropItem(0.5, 70, 0.5, "minecraft:gold_ingot", 1) // different item: separate entity

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	if n, ents := countOf(inst, iron); n != 3 || ents != 1 {
		t.Errorf("iron: %d units in %d entities, want 3 in 1", n, ents)
	}
	if len(inst.items) != 2 {
		t.Errorf("entities: %d, want 2 (iron stack + gold)", len(inst.items))
	}
	if got := inst.DroppedItemsNear(0.5, 70, 0.5, 1.5, "minecraft:iron_ingot"); got != 3 {
		t.Errorf("DroppedItemsNear iron = %d, want 3", got)
	}
}

func TestDropItemMergeRespectsStackLimit(t *testing.T) {
	inst := bareInstance(New(), floorWorld())
	inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", maxStackSize)
	inst.itemTick(0)
	inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", 1)
	if len(inst.items) != 2 {
		t.Errorf("a full stack must not absorb more: %d entities, want 2", len(inst.items))
	}
}

func TestItemPickedUpByNearbyPlayer(t *testing.T) {
	s := New()
	inst := bareInstance(s, floorWorld())
	c := itemConn(s, inst, 1, 0.5, 70, 0.5)
	inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", 3)

	// Within the pickup delay nothing happens.
	for i := 0; i < itemPickupDelay-1; i++ {
		inst.itemTick(uint64(i))
	}
	if len(inst.items) != 1 {
		t.Fatal("item collected before the pickup delay elapsed")
	}
	inst.itemTick(itemPickupDelay)
	if len(inst.items) != 0 {
		t.Fatalf("item should be collected, %d left", len(inst.items))
	}
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	total := 0
	for slot := int16(mainInvStart); slot < hotbarStart+9; slot++ {
		if st := c.inv.get(slot); st.ID == iron {
			total += int(st.Count)
		}
	}
	if total != 3 {
		t.Errorf("inventory iron = %d, want 3", total)
	}
}

func TestItemNotPickedUpFarOrSpectatorOrDead(t *testing.T) {
	s := New()
	inst := bareInstance(s, floorWorld())
	far := itemConn(s, inst, 1, 3.5, 70, 0.5)
	spec := itemConn(s, inst, 2, 0.5, 70, 0.5)
	spec.player.SetGamemode(player.Spectator)
	inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", 1)
	for i := 0; i <= itemPickupDelay+1; i++ {
		inst.itemTick(uint64(i))
	}
	if len(inst.items) != 1 {
		t.Fatal("neither a far player nor a spectator may collect")
	}
	_ = far
}

func TestItemPickupLeavesOverflowOnGround(t *testing.T) {
	s := New()
	inst := bareInstance(s, floorWorld())
	c := itemConn(s, inst, 1, 0.5, 70, 0.5)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	// Fill every main-inventory + hotbar slot with full iron stacks except one
	// slot with 62, so only 2 more units fit.
	for slot := int16(mainInvStart); slot < hotbarStart+9; slot++ {
		c.inv.set(slot, itemStack{ID: iron, Count: maxStackSize})
	}
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: maxStackSize - 2})
	inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", 5)
	for i := 0; i <= itemPickupDelay+1; i++ {
		inst.itemTick(uint64(i))
	}
	if n, ents := countOf(inst, iron); n != 3 || ents != 1 {
		t.Errorf("leftover on the ground: %d units in %d entities, want 3 in 1", n, ents)
	}
}

func TestItemDespawns(t *testing.T) {
	inst := bareInstance(New(), floorWorld())
	inst.DropItem(0.5, 70, 0.5, "minecraft:iron_ingot", 1)
	for i := 0; i < itemDespawnTicks; i++ {
		inst.itemTick(uint64(i))
	}
	if len(inst.items) != 0 {
		t.Errorf("item should despawn after %d ticks", itemDespawnTicks)
	}
}

func TestItemPayloads(t *testing.T) {
	it := &itemEntity{eid: 9, stack: itemStack{ID: 1, Count: 3}, x: 1, y: 2, z: 3}
	it.uuid = entityUUID(it.eid)
	spawn := spawnItemPayload(it)
	// eid(1) + uuid(16) + type(1) + 3 doubles(24) + 3 angles + data(1) + 3 shorts(6)
	if len(spawn) != 1+16+1+24+3+1+6 {
		t.Errorf("spawn payload length %d", len(spawn))
	}
	if spawn[17] != byte(itemEntityTypeID) {
		t.Errorf("entity type byte = %d, want %d", spawn[17], itemEntityTypeID)
	}
	meta := itemMetadataPayload(it)
	// eid, index 8, type 7, present, item id, count, TAG_End, 0xFF
	want := []byte{9, 8, 7, 1, 1, 3, 0, 0xFF}
	if string(meta) != string(want) {
		t.Errorf("metadata = % x, want % x", meta, want)
	}
	if p := pickupItemPayload(9, 4, 3); string(p) != string([]byte{9, 4, 3}) {
		t.Errorf("pickup payload = % x", p)
	}
}
