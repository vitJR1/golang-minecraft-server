// Package chest is chest storage, independent of connections: the Store an
// open chest GUI reads from and writes to, and Chests, an instance's regular
// chests by block position. A player's ender chest is the other Store
// (enderchest.Chest). There's no item NBT model beyond what inventory.Stack
// tracks.
package chest

import (
	"strings"
	"sync"

	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

// Rows is the GUI row count of a single chest.
const Rows = 3

// Slots are one chest's contents.
type Slots = [inventory.ChestSlots]inventory.Stack

// Store is what an open chest GUI reads from and writes to: a block chest
// bound to a position, or a player's ender chest.
type Store interface {
	// Contents returns a copy of the 27 stored slots.
	Contents() Slots
	// SetSlot stores st at idx (0..26); out-of-range is ignored.
	SetSlot(idx int, st inventory.Stack)
	// Update runs fn on the stored slots under the store's lock: a click
	// reads and writes the chest atomically.
	Update(fn func(*Slots))
	// Title is the GUI window title.
	Title() string
}

// IsEnder reports whether b is an ender chest block.
func IsEnder(b world.Block) bool { return b.Name == "minecraft:ender_chest" }

// Chests are an instance's regular chests by block position, created empty
// on first use. Safe for concurrent use (several players may open chests at
// once). The zero value is ready.
type Chests struct {
	mu sync.Mutex
	m  map[world.Position]*Slots
}

// Contents returns (a copy of) the chest's slots at pos.
func (c *Chests) Contents(pos world.Position) Slots {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.m[pos]; s != nil {
		return *s
	}
	return Slots{}
}

// SetSlot stores st in chest pos's slot idx (0..26).
func (c *Chests) SetSlot(pos world.Position, idx int, st inventory.Stack) {
	if idx < 0 || idx >= inventory.ChestSlots {
		return
	}
	c.Update(pos, func(s *Slots) { s[idx] = st })
}

// Update runs fn on chest pos's slots under the lock.
func (c *Chests) Update(pos world.Position, fn func(*Slots)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[world.Position]*Slots)
	}
	s := c.m[pos]
	if s == nil {
		s = &Slots{}
		c.m[pos] = s
	}
	fn(s)
}

// At is the Store of the chest at pos.
func (c *Chests) At(pos world.Position) Block { return Block{chests: c, pos: pos} }

// Block is the Store of one chest block.
type Block struct {
	chests *Chests
	pos    world.Position
}

func (b Block) Contents() Slots                     { return b.chests.Contents(b.pos) }
func (b Block) SetSlot(idx int, st inventory.Stack) { b.chests.SetSlot(b.pos, idx, st) }
func (b Block) Update(fn func(*Slots))              { b.chests.Update(b.pos, fn) }
func (b Block) Title() string                       { return "Chest" }

// IsChestBlock reports whether b is a chest the player can open (chest /
// trapped_chest / ender_chest). Matched by name suffix so all chest
// variants count.
func IsChestBlock(b world.Block) bool { return strings.HasSuffix(b.Name, "chest") }
