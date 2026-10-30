package enderchest

import (
	"context"
	"os"
	"testing"

	"minecraft-server/server/inventory"
)

// TestEnderChestLoadFailureBlocksSave: when the store can't be read, the
// chest opens empty and is never written back (so saved data isn't lost).
func TestEnderChestLoadFailureBlocksSave(t *testing.T) {
	fail := &failingEnderStore{}
	var cs Chests
	e := cs.For(fail, [16]byte{9})
	if !e.noSave {
		t.Fatal("failed load should mark the chest noSave")
	}
	e.SetSlot(0, inventory.Stack{ID: 1, Count: 1})
	Save(fail, e)
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
