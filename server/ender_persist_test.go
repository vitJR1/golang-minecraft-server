package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"minecraft-server/server/enderchest"
	"minecraft-server/server/inventory"
)

// TestEnderChestPersistsThroughFileStore: a click saves to the file store,
// and a fresh Server with a store built from the same path loads it back.
func TestEnderChestPersistsThroughFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enderchests.json")
	fs, err := enderchest.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	s.EnderChests = fs
	owner := [16]byte{7}
	e := s.enderChestFor(owner)
	e.setSlot(4, inventory.Stack{ID: 764, Count: 5})
	s.saveEnderChest(e)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	fs2, err := enderchest.NewFileStore(path)
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
		if !st.Empty() {
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
	e.setSlot(0, inventory.Stack{ID: 1, Count: 1})
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
