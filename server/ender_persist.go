package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"minecraft-server/world"
)

// ender_persist.go keeps ender chest contents across restarts. The chest
// model (chest.go) talks to an EnderChestStore holding opaque JSON per player
// UUID: store.EnderChestRepo under STORAGE=postgres, FileEnderChestStore
// (enderchests.json) under STORAGE=memory, or nil for RAM only.

// EnderChestStore is the persistence contract for per-player ender chests.
// Implementations must be safe for concurrent use.
type EnderChestStore interface {
	// LoadEnderChest returns the saved slots JSON, or nil when none exists.
	LoadEnderChest(ctx context.Context, uuid string) ([]byte, error)
	// SaveEnderChest replaces the saved slots JSON for uuid.
	SaveEnderChest(ctx context.Context, uuid string, data []byte) error
}

// FileEnderChestStore is the no-database backend: one JSON file mapping
// player UUID → slots, read once at construction and rewritten atomically on
// every save.
type FileEnderChestStore struct {
	mu   sync.Mutex
	path string
	data map[string]json.RawMessage
}

// NewFileEnderChestStore loads path (a missing file is fine).
func NewFileEnderChestStore(path string) (*FileEnderChestStore, error) {
	s := &FileEnderChestStore{path: path, data: map[string]json.RawMessage{}}
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

// LoadEnderChest implements EnderChestStore.
func (s *FileEnderChestStore) LoadEnderChest(_ context.Context, uuid string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.data[uuid]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), d...), nil
}

// SaveEnderChest implements EnderChestStore: update the map, rewrite the file.
func (s *FileEnderChestStore) SaveEnderChest(_ context.Context, uuid string, data []byte) error {
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

// encodeEnderSlots serialises the non-empty slots.
func encodeEnderSlots(slots [chestSlotCount]itemStack) ([]byte, error) {
	out := make([]enderSlotJSON, 0, chestSlotCount)
	for i, st := range slots {
		if st.empty() {
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

// decodeEnderSlots is the inverse; unknown items and bad slots are skipped.
func decodeEnderSlots(data []byte) ([chestSlotCount]itemStack, error) {
	var slots [chestSlotCount]itemStack
	if len(data) == 0 {
		return slots, nil
	}
	var in []enderSlotJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return slots, err
	}
	for _, e := range in {
		if e.Slot < 0 || e.Slot >= chestSlotCount || e.Count <= 0 {
			continue
		}
		id, ok := world.ItemByName(e.Item)
		if !ok {
			continue
		}
		slots[e.Slot] = itemStack{
			ID: id, Count: byte(min(e.Count, maxStackSize)),
			Name: e.Name, Damage: e.Damage, Enchantments: e.Enchantments,
		}
	}
	return slots, nil
}

// uuidString formats a binary UUID in the hyphenated form the stores key by.
func uuidString(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// loadEnderChest fills e from the store on first access. A failed load is
// logged and leaves the chest empty for this session; a later save would
// then overwrite the stored copy, so the chest is marked read-only until a
// load succeeds.
func (s *Server) loadEnderChest(e *enderChest) {
	if s.EnderChests == nil {
		return
	}
	data, err := s.EnderChests.LoadEnderChest(context.Background(), uuidString(e.uuid))
	if err != nil {
		slog.Warn("ender chest load failed; chest stays empty and unsaved this session",
			"uuid", uuidString(e.uuid), "err", err)
		e.noSave = true
		return
	}
	slots, err := decodeEnderSlots(data)
	if err != nil {
		slog.Warn("ender chest data corrupt; chest stays empty and unsaved this session",
			"uuid", uuidString(e.uuid), "err", err)
		e.noSave = true
		return
	}
	e.mu.Lock()
	e.slots = slots
	e.mu.Unlock()
}

// saveEnderChest writes e's current contents through the store.
func (s *Server) saveEnderChest(e *enderChest) {
	if s.EnderChests == nil || e.noSave {
		return
	}
	data, err := encodeEnderSlots(e.contents())
	if err != nil {
		slog.Error("ender chest encode failed", "err", err)
		return
	}
	if err := s.EnderChests.SaveEnderChest(context.Background(), uuidString(e.uuid), data); err != nil {
		slog.Error("ender chest save failed", "uuid", uuidString(e.uuid), "err", err)
	}
}
