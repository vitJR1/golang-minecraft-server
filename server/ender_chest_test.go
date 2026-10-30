package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"minecraft-server/protocol"
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
	e.SetSlot(4, inventory.Stack{ID: 764, Count: 5})
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
	got := s2.enderChestFor(owner).Contents()
	if got[4].ID != 764 || got[4].Count != 5 {
		t.Fatalf("reloaded slot 4 = %+v, want diamond×5", got[4])
	}
	for i, st := range s2.enderChestFor([16]byte{8}).Contents() {
		if !st.Empty() {
			t.Errorf("other player's chest slot %d should be empty: %+v", i, st)
		}
	}
	if raw, _ := fs2.LoadEnderChest(context.Background(), protocol.UUIDString([16]byte{8})); raw != nil {
		t.Errorf("unsaved player should have no record, got %s", raw)
	}
}
