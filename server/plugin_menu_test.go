package server

import (
	"bytes"
	"testing"

	"minecraft-server/game"
	"minecraft-server/protocol"
)

// menuClickPacket builds a Click Container on the menu window with the
// given slot, button and mode and no changed slots.
func menuClickPacket(slot int16, button byte, mode int32) []byte {
	var p bytes.Buffer
	p.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteShort(slot))
	p.WriteByte(button)
	protocol.WriteVarInt32ToBuffer(&p, mode)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteEmptySlot())
	return p.Bytes()
}

// TestPluginMenuClickModifiers: a plugin GUI's onClick receives the slot
// plus the vanilla click modifiers — shift-click arrives as mode 1, a
// right click as button 1 of mode 0; clicks on empty slots are dropped.
func TestPluginMenuClickModifiers(t *testing.T) {
	s := New()
	c := offlineHub(s, "Shopper")

	var got []game.MenuClick
	c.openPluginMenu("Shop", 1, []game.MenuItem{{Slot: 3, Item: "minecraft:white_wool", Name: "Wool"}},
		func(click game.MenuClick) { got = append(got, click) })
	drain(c)

	play(t, c, SbPlayClickContainer, menuClickPacket(3, 0, 0)) // plain left click
	play(t, c, SbPlayClickContainer, menuClickPacket(3, 0, 1)) // shift-click
	play(t, c, SbPlayClickContainer, menuClickPacket(3, 1, 0)) // right click
	play(t, c, SbPlayClickContainer, menuClickPacket(4, 0, 1)) // empty slot: ignored

	want := []game.MenuClick{
		{Slot: 3},
		{Slot: 3, Shift: true},
		{Slot: 3, Right: true},
	}
	if len(got) != len(want) {
		t.Fatalf("clicks: got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("click %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
