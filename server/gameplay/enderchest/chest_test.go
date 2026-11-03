package enderchest

import (
	"context"
	"os"
	"testing"

	"minecraft-server/server/gameplay/inventory"
)

// TestEnderChestLoadFailureBlocksSave: when the store can't be read, the
// chest is unavailable — not editable, never written back (so saved data
// isn't lost).
func TestEnderChestLoadFailureBlocksSave(t *testing.T) {
	fail := &failingEnderStore{fail: true}
	var cs Chests
	e := cs.For(fail, [16]byte{9})
	if e.Available() {
		t.Fatal("failed load should make the chest unavailable")
	}
	e.SetSlot(0, inventory.Stack{ID: 1, Count: 1})
	ran := false
	e.Update(func(*[inventory.ChestSlots]inventory.Stack) { ran = true })
	if ran || !e.Contents()[0].Empty() {
		t.Error("an unavailable chest must refuse edits")
	}
	Save(fail, e)
	if fail.saves != 0 {
		t.Errorf("save went through after a failed load: %d", fail.saves)
	}
}

// TestEnderChestLoadFailureNotCached: a failed load isn't remembered; once
// the store is back the real contents load (review #18).
func TestEnderChestLoadFailureNotCached(t *testing.T) {
	data, err := Encode([inventory.ChestSlots]inventory.Stack{3: {ID: 1, Count: 5}})
	if err != nil {
		t.Fatal(err)
	}
	st := &failingEnderStore{fail: true, data: data}
	var cs Chests
	if cs.For(st, [16]byte{7}).Available() {
		t.Fatal("first load failed")
	}
	st.fail = false
	e := cs.For(st, [16]byte{7})
	if !e.Available() || e.Contents()[3].Count != 5 {
		t.Fatalf("retry should load the stored chest: available %v, slot %+v", e.Available(), e.Contents()[3])
	}
	if cs.For(st, [16]byte{7}) != e {
		t.Error("a successful load is cached")
	}
	e.SetSlot(0, inventory.Stack{ID: 1, Count: 1})
	Save(st, e)
	if st.saves != 1 {
		t.Errorf("saves = %d, want 1", st.saves)
	}
}

type failingEnderStore struct {
	fail  bool
	data  []byte
	saves int
}

func (f *failingEnderStore) LoadEnderChest(context.Context, string) ([]byte, error) {
	if f.fail {
		return nil, os.ErrPermission
	}
	return f.data, nil
}

func (f *failingEnderStore) SaveEnderChest(context.Context, string, []byte) error {
	f.saves++
	return nil
}
