package server

import (
	"bytes"
	"testing"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

func TestSpawnEntityPayloadItemFrame(t *testing.T) {
	ie := instanceEntity{
		eid:  42,
		uuid: entityUUID(42),
		e:    world.Entity{Type: "minecraft:item_frame", X: 1.5, Y: 64, Z: -2.5, Frame: &world.FrameData{Facing: world.FaceWest}},
	}
	buf := bytes.NewBuffer(spawnEntityPayload(ie))

	eid, _ := protocol.ReadVarInt(buf)
	if eid != 42 {
		t.Fatalf("eid: got %d", eid)
	}
	if got := buf.Next(16); len(got) != 16 {
		t.Fatalf("uuid: got %d bytes", len(got))
	}
	typ, _ := protocol.ReadVarInt(buf)
	if int32(typ) != world.ItemFrameEntityID {
		t.Errorf("type: got %d, want %d", typ, world.ItemFrameEntityID)
	}
	x, _ := protocol.ReadDouble(buf)
	y, _ := protocol.ReadDouble(buf)
	z, _ := protocol.ReadDouble(buf)
	if x != 1.5 || y != 64 || z != -2.5 {
		t.Errorf("pos: got (%v,%v,%v)", x, y, z)
	}
	buf.Next(3) // pitch, yaw, head yaw
	data, _ := protocol.ReadVarInt(buf)
	if byte(data) != world.FaceWest {
		t.Errorf("facing/data: got %d, want %d", data, world.FaceWest)
	}
	if buf.Len() != 6 { // 3 × short velocity
		t.Errorf("trailing velocity bytes: got %d, want 6", buf.Len())
	}
}

func TestSpawnEntityGlowFrameType(t *testing.T) {
	ie := instanceEntity{eid: 1, e: world.Entity{Frame: &world.FrameData{Glowing: true}}}
	buf := bytes.NewBuffer(spawnEntityPayload(ie))
	_, _ = protocol.ReadVarInt(buf) // eid
	buf.Next(16)                    // uuid
	typ, _ := protocol.ReadVarInt(buf)
	if int32(typ) != world.GlowItemFrameEntityID {
		t.Errorf("glow type: got %d, want %d", typ, world.GlowItemFrameEntityID)
	}
}

func TestFrameMetadataPayload(t *testing.T) {
	// Frame with a known item + rotation → both metadata entries present.
	ie := instanceEntity{eid: 7, e: world.Entity{Frame: &world.FrameData{Item: "minecraft:diamond", Rotation: 3}}}
	wantItem, _ := world.ItemByName("minecraft:diamond")
	buf := bytes.NewBuffer(frameMetadataPayload(ie))

	eid, _ := protocol.ReadVarInt(buf)
	if eid != 7 {
		t.Fatalf("eid: got %d", eid)
	}
	idx, _ := buf.ReadByte()
	typ, _ := protocol.ReadVarInt(buf)
	if idx != 8 || typ != 7 {
		t.Fatalf("slot meta header: idx=%d type=%d", idx, typ)
	}
	present, _ := buf.ReadByte()
	itemID, _ := protocol.ReadVarInt(buf)
	count, _ := buf.ReadByte()
	nbtEnd, _ := buf.ReadByte()
	if present != 1 || int32(itemID) != wantItem || count != 1 || nbtEnd != 0 {
		t.Errorf("slot: present=%d id=%d count=%d nbtEnd=%d (wantID=%d)", present, itemID, count, nbtEnd, wantItem)
	}
	idx2, _ := buf.ReadByte()
	typ2, _ := protocol.ReadVarInt(buf)
	rot, _ := protocol.ReadVarInt(buf)
	if idx2 != 9 || typ2 != 1 || rot != 3 {
		t.Errorf("rotation meta: idx=%d type=%d rot=%d", idx2, typ2, rot)
	}
	end, _ := buf.ReadByte()
	if end != 0xFF {
		t.Errorf("terminator: got 0x%02X", end)
	}
}

func TestFrameMetadataEmptyIsNil(t *testing.T) {
	// Empty frame, no rotation → nothing to send.
	ie := instanceEntity{eid: 1, e: world.Entity{Frame: &world.FrameData{}}}
	if frameMetadataPayload(ie) != nil {
		t.Error("empty frame should produce no metadata packet")
	}
}

func TestLoadWorldEntitiesAssignsIDs(t *testing.T) {
	s := New()
	w := world.NewMemoryWorld()
	w.AddEntity(world.Entity{Type: "minecraft:item_frame", X: 1, Y: 2, Z: 3, Frame: &world.FrameData{}})
	w.AddEntity(world.Entity{Type: "minecraft:item_frame", X: 4, Y: 5, Z: 6, Frame: &world.FrameData{}})

	inst := NewInstance("frames", s, w)
	t.Cleanup(inst.Stop)
	if len(inst.worldEntities) != 2 {
		t.Fatalf("got %d world entities, want 2", len(inst.worldEntities))
	}
	if inst.worldEntities[0].eid == inst.worldEntities[1].eid || inst.worldEntities[0].eid == 0 {
		t.Errorf("entity IDs not assigned uniquely: %d, %d",
			inst.worldEntities[0].eid, inst.worldEntities[1].eid)
	}
}

func TestCreativeSlotHeldItem(t *testing.T) {
	c := &ClientConnection{}
	c.heldSlot.Store(0) // hotbar 0 → inventory slot 36

	frameID, ok := world.ItemByName("minecraft:item_frame")
	if !ok {
		t.Fatal("item_frame item not in registry")
	}

	var b bytes.Buffer
	b.Write(protocol.WriteShort(hotbarSlotBase)) // slot 36
	b.WriteByte(1)                               // present
	b.Write(protocol.WriteVarInt32(frameID))
	b.WriteByte(1) // count
	c.onSetCreativeSlot(&b)

	if got := c.heldItemName(); got != "minecraft:item_frame" {
		t.Errorf("heldItemName = %q, want minecraft:item_frame", got)
	}

	// Clearing the slot (present=false) drops the held item.
	var empty bytes.Buffer
	empty.Write(protocol.WriteShort(hotbarSlotBase))
	empty.WriteByte(0) // not present
	c.onSetCreativeSlot(&empty)
	if got := c.heldItemName(); got != "" {
		t.Errorf("after clear: heldItemName = %q, want empty", got)
	}
}

func TestFrameInteractInsertAndRotate(t *testing.T) {
	s := New()
	w := world.NewMemoryWorld()
	w.AddEntity(world.Entity{Type: "minecraft:item_frame", Frame: &world.FrameData{}})
	inst := NewInstance("frames-interact", s, w)
	t.Cleanup(inst.Stop)
	eid := inst.worldEntities[0].eid
	frame := inst.worldEntities[0].e.Frame

	// Empty held → no change.
	inst.FrameInteract(eid, "")
	if frame.Item != "" {
		t.Fatalf("empty hand should not fill frame: %q", frame.Item)
	}
	// Unknown item → rejected.
	inst.FrameInteract(eid, "minecraft:not_an_item")
	if frame.Item != "" {
		t.Fatalf("unknown item should be rejected: %q", frame.Item)
	}
	// Known item → inserted.
	inst.FrameInteract(eid, "minecraft:diamond")
	if frame.Item != "minecraft:diamond" || frame.Rotation != 0 {
		t.Fatalf("insert failed: %+v", frame)
	}
	// Full frame → right-click rotates, item unchanged.
	inst.FrameInteract(eid, "minecraft:stone")
	if frame.Rotation != 1 || frame.Item != "minecraft:diamond" {
		t.Errorf("rotate failed: %+v", frame)
	}
	// Rotation wraps 0..7.
	for range 7 {
		inst.FrameInteract(eid, "")
	}
	if frame.Rotation != 0 {
		t.Errorf("rotation should wrap to 0, got %d", frame.Rotation)
	}

	// Unknown eid is a no-op (no panic).
	inst.FrameInteract(999999, "minecraft:diamond")
}
