package server

import (
	"math"
)

// drop.go turns the two ways a player gets rid of an item into dropped-item
// entities (item_entity.go):
//
//   - Q / ctrl+Q while holding something: Player Action 4 (one unit) or 3
//     (the whole stack) from the selected hotbar slot.
//   - Inside an inventory or chest window: clicking outside the window
//     (slot -999) drops the cursor stack, the drop key over a slot drops
//     from that slot (both simulated in click.go), and closing the window
//     with something on the cursor drops it.
//
// Thrown items leave from eye height along the look direction with the
// vanilla speed, and can't be picked back up for itemThrowDelay ticks.

// eyeHeight is shared with projectile.go.
const (
	dropSpeed = 0.3 // blocks/tick along the look direction (vanilla item toss)
	dropLift  = 0.1 // extra upward component
)

// dropHeld throws one unit (all=false) or the whole stack (all=true) from
// the selected hotbar slot and syncs the slot to the client.
func (c *ClientConnection) dropHeld(all bool) {
	slot := int16(hotbarStart) + int16(c.heldSlot.Load())
	st := c.inv.get(slot)
	if st.empty() || st.Name == navigatorName || st.Name == selectorName {
		return // hub UI items aren't droppable
	}
	n := 1
	if all {
		n = int(st.Count)
	}
	thrown := st
	thrown.Count = byte(n)
	st.Count -= byte(n)
	if st.Count == 0 {
		st = itemStack{}
	}
	c.inv.set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
	c.throwStack(thrown)
}

// throwStack spawns the stack flying from the player's eyes in the direction
// they look (vanilla drop velocity), keeping its NBT.
func (c *ClientConnection) throwStack(st itemStack) {
	if st.empty() || c.instance == nil || c.player == nil {
		return
	}
	s := c.player.Snapshot()
	yaw := float64(s.Yaw) * math.Pi / 180
	pitch := float64(s.Pitch) * math.Pi / 180
	vx := -math.Sin(yaw) * math.Cos(pitch) * dropSpeed
	vz := math.Cos(yaw) * math.Cos(pitch) * dropSpeed
	vy := -math.Sin(pitch)*dropSpeed + dropLift
	c.instance.ThrowStack(st, s.X, s.Y+eyeHeight-0.3, s.Z, vx, vy, vz)
}

// dropCursor throws whatever the client is carrying on its cursor (window
// closed with an item picked up) and clears it.
func (c *ClientConnection) dropCursor() {
	if c.cursor.empty() {
		return
	}
	st := c.cursor
	c.cursor = itemStack{}
	c.throwStack(st)
}
