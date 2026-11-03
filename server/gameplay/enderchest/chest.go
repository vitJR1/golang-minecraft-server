package enderchest

import (
	"context"
	"log/slog"
	"sync"

	"minecraft-server/protocol"
	"minecraft-server/server/gameplay/inventory"
)

// Chest is a player's private 27 slots. One record per player UUID follows
// them across instances; every ender chest block opens the same record. It
// is a chest.Store.
type Chest struct {
	uuid [16]byte
	// unavailable: the stored copy couldn't be loaded. Such a chest is never
	// cached, edited or saved — an empty editable chest would swallow what
	// the player puts in, and saving it would clobber the real contents.
	unavailable bool
	mu          sync.Mutex
	slots       [inventory.ChestSlots]inventory.Stack
}

func (e *Chest) Contents() [inventory.ChestSlots]inventory.Stack {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.slots
}

func (e *Chest) SetSlot(idx int, st inventory.Stack) {
	if idx < 0 || idx >= inventory.ChestSlots || e.unavailable {
		return
	}
	e.mu.Lock()
	e.slots[idx] = st
	e.mu.Unlock()
}

func (e *Chest) Title() string {
	if e.unavailable {
		return "Ender Chest (unavailable)"
	}
	return "Ender Chest"
}

// Available reports whether the chest's contents were loaded. An
// unavailable chest is empty and read-only; the server shouldn't open it.
func (e *Chest) Available() bool { return !e.unavailable }

// Update runs fn on the slots under the chest's lock. On an unavailable
// chest fn isn't run at all, so a click is refused (the caller resyncs the
// window).
func (e *Chest) Update(fn func(*[inventory.ChestSlots]inventory.Stack)) {
	if e.unavailable {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(&e.slots)
}

// Chests are every player's ender chest, keyed by UUID, loaded from a Store
// on first use. The zero value is ready.
type Chests struct {
	mu sync.Mutex
	m  map[[16]byte]*Chest
}

// For returns the player's ender chest, loading it from store (nil: RAM
// only) on first use. A failed load isn't cached: the result is an
// unavailable chest (see Available) and the next call tries again.
func (cs *Chests) For(store Store, uuid [16]byte) *Chest {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.m == nil {
		cs.m = make(map[[16]byte]*Chest)
	}
	e := cs.m[uuid]
	if e == nil {
		e = &Chest{uuid: uuid}
		if !load(store, e) {
			e.unavailable = true
			return e
		}
		cs.m[uuid] = e
	}
	return e
}

// load fills e from the store; false (logged) when it can't be read or
// decoded.
func load(store Store, e *Chest) bool {
	if store == nil {
		return true
	}
	id := protocol.UUIDString(e.uuid)
	data, err := store.LoadEnderChest(context.Background(), id)
	if err != nil {
		slog.Warn("ender chest load failed; chest unavailable until a load succeeds", "uuid", id, "err", err)
		return false
	}
	slots, err := Decode(data)
	if err != nil {
		slog.Warn("ender chest data corrupt; chest unavailable", "uuid", id, "err", err)
		return false
	}
	e.mu.Lock()
	e.slots = slots
	e.mu.Unlock()
	return true
}

// Save writes e's current contents through store (nil: RAM only).
func Save(store Store, e *Chest) {
	if store == nil || e.unavailable {
		return
	}
	data, err := Encode(e.Contents())
	if err != nil {
		slog.Error("ender chest encode failed", "err", err)
		return
	}
	id := protocol.UUIDString(e.uuid)
	if err := store.SaveEnderChest(context.Background(), id, data); err != nil {
		slog.Error("ender chest save failed", "uuid", id, "err", err)
	}
}
