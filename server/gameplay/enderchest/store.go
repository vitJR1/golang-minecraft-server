// Package enderchest persists ender chest contents across restarts. The
// chest model (server/chest.go) talks to a Store holding opaque JSON per
// player UUID: store.EnderChestRepo under STORAGE=postgres, FileStore
// (enderchests.json) under STORAGE=memory, or nil for RAM only. Encode /
// Decode convert between that JSON and the 27 chest slots.
package enderchest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// Store is the persistence contract for per-player ender chests.
// Implementations must be safe for concurrent use.
type Store interface {
	// LoadEnderChest returns the saved slots JSON, or nil when none exists.
	LoadEnderChest(ctx context.Context, uuid string) ([]byte, error)
	// SaveEnderChest replaces the saved slots JSON for uuid.
	SaveEnderChest(ctx context.Context, uuid string, data []byte) error
}

// FileStore is the no-database backend: one JSON file mapping
// player UUID → slots, read once at construction and rewritten atomically on
// every save.
type FileStore struct {
	mu   sync.Mutex
	path string
	data map[string]json.RawMessage
}

// NewFileStore loads path (a missing file is fine).
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: map[string]json.RawMessage{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read ender chest file: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("parse ender chest file: %w", err)
		}
	}
	return s, nil
}

// LoadEnderChest implements Store.
func (s *FileStore) LoadEnderChest(_ context.Context, uuid string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.data[uuid]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), d...), nil
}

// SaveEnderChest implements Store: update the map, rewrite the file.
func (s *FileStore) SaveEnderChest(_ context.Context, uuid string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[uuid] = append(json.RawMessage(nil), data...)
	out, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// enderSlotJSON is one non-empty slot in the persisted form. Items go by
// namespaced name so the file survives item-ID table changes.
type enderSlotJSON struct {
	Slot         int            `json:"slot"`
	Item         string         `json:"item"`
	Count        int            `json:"count"`
	Name         string         `json:"name,omitempty"`
	Damage       int            `json:"damage,omitempty"`
	Enchantments map[string]int `json:"enchantments,omitempty"`
}

// Encode serialises the non-empty slots.
func Encode(slots [inventory.ChestSlots]inventory.Stack) ([]byte, error) {
	out := make([]enderSlotJSON, 0, inventory.ChestSlots)
	for i, st := range slots {
		if st.Empty() {
			continue
		}
		name, ok := world.ItemName(st.ID)
		if !ok {
			continue // unknown id: nothing stable to write
		}
		out = append(out, enderSlotJSON{
			Slot: i, Item: name, Count: int(st.Count),
			Name: st.Name, Damage: st.Damage, Enchantments: st.Enchantments,
		})
	}
	return json.Marshal(out)
}

// Decode is the inverse; unknown items and bad slots are skipped.
func Decode(data []byte) ([inventory.ChestSlots]inventory.Stack, error) {
	var slots [inventory.ChestSlots]inventory.Stack
	if len(data) == 0 {
		return slots, nil
	}
	var in []enderSlotJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return slots, err
	}
	for _, e := range in {
		if e.Slot < 0 || e.Slot >= inventory.ChestSlots || e.Count <= 0 {
			continue
		}
		id, ok := world.ItemByName(e.Item)
		if !ok {
			continue
		}
		slots[e.Slot] = inventory.Stack{
			ID: id, Count: byte(min(e.Count, inventory.MaxStack)),
			Name: e.Name, Damage: e.Damage, Enchantments: e.Enchantments,
		}
	}
	return slots, nil
}
