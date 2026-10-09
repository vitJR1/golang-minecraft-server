package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"minecraft-server/world"
)

func TestEnderSlotsCodecRoundTrip(t *testing.T) {
	diamond, _ := world.ItemByName("minecraft:diamond")
	pick, _ := world.ItemByName("minecraft:diamond_pickaxe")
	var slots [chestSlotCount]itemStack
	slots[0] = itemStack{ID: diamond, Count: 3}
	slots[26] = itemStack{ID: pick, Count: 1, Damage: 12, Enchantments: map[string]int{"minecraft:efficiency": 2}, Name: "Digger"}

	data, err := encodeEnderSlots(slots)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeEnderSlots(data)
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
		if !back[i].empty() {
			t.Errorf("slot %d should be empty: %+v", i, back[i])
		}
	}
	// Unknown items and out-of-range slots are skipped, not fatal.
	odd, err := decodeEnderSlots([]byte(`[{"slot":40,"item":"minecraft:diamond","count":1},{"slot":1,"item":"minecraft:nope","count":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range odd {
		if !st.empty() {
			t.Errorf("slot %d unexpectedly set: %+v", i, st)
		}
	}
	if _, err := decodeEnderSlots([]byte(`garbage`)); err == nil {
		t.Error("corrupt data should error")
	}
	empty, err := decodeEnderSlots(nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range empty {
		if !st.empty() {
			t.Errorf("nil data: slot %d set: %+v", i, st)
		}
	}
}

// TestEnderChestPersistsThroughFileStore: a click saves to the file store,
// and a fresh Server with a store built from the same path loads it back.
func TestEnderChestPersistsThroughFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enderchests.json")
	fs, err := NewFileEnderChestStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	s.EnderChests = fs
	owner := [16]byte{7}
	c := enderTestConn(s, s.Hub, "Owner", owner)
	e := s.enderChestFor(owner)
	c.applyChestClick(changedSlots(t, map[int16]itemStack{4: {ID: 764, Count: 5}}), e)
	s.saveEnderChest(e)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	fs2, err := NewFileEnderChestStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s2 := New()
	s2.EnderChests = fs2
	got := s2.enderChestFor(owner).contents()
	if got[4].ID != 764 || got[4].Count != 5 {
		t.Fatalf("reloaded slot 4 = %+v, want diamond×5", got[4])
	}
	for i, st := range s2.enderChestFor([16]byte{8}).contents() {
		if !st.empty() {
			t.Errorf("other player's chest slot %d should be empty: %+v", i, st)
		}
	}
	if raw, _ := fs2.LoadEnderChest(context.Background(), uuidString([16]byte{8})); raw != nil {
		t.Errorf("unsaved player should have no record, got %s", raw)
	}
}

// TestEnderChestLoadFailureBlocksSave: when the store can't be read, the
// chest opens empty and is never written back (so saved data isn't lost).
func TestEnderChestLoadFailureBlocksSave(t *testing.T) {
	s := New()
	fail := &failingEnderStore{}
	s.EnderChests = fail
	e := s.enderChestFor([16]byte{9})
	if !e.noSave {
		t.Fatal("failed load should mark the chest noSave")
	}
	e.setSlot(0, itemStack{ID: 1, Count: 1})
	s.saveEnderChest(e)
	if fail.saves != 0 {
		t.Errorf("save went through after a failed load: %d", fail.saves)
	}
}

type failingEnderStore struct{ saves int }

func (f *failingEnderStore) LoadEnderChest(context.Context, string) ([]byte, error) {
	return nil, os.ErrPermission
}

func (f *failingEnderStore) SaveEnderChest(context.Context, string, []byte) error {
	f.saves++
	return nil
}

func TestUUIDString(t *testing.T) {
	u := [16]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}
	if got := uuidString(u); got != "11223344-5566-7788-99aa-bbccddeeff00" {
		t.Errorf("uuidString = %q", got)
	}
}
