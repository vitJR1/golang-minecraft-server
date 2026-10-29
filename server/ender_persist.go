package server

import (
	"context"
	"fmt"
	"log/slog"

	"minecraft-server/server/enderchest"
)

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
	slots, err := enderchest.Decode(data)
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
	data, err := enderchest.Encode(e.contents())
	if err != nil {
		slog.Error("ender chest encode failed", "err", err)
		return
	}
	if err := s.EnderChests.SaveEnderChest(context.Background(), uuidString(e.uuid), data); err != nil {
		slog.Error("ender chest save failed", "uuid", uuidString(e.uuid), "err", err)
	}
}
