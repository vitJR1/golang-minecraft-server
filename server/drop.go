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
//     from that slot, and closing the window with something on the cursor
//     drops it. The client reports the slots it changed plus the new cursor
//     after every click; the server models all of those, so whatever is
//     missing afterwards (per item id) is what left the player's hands.
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
	itemID := st.ID
	st.Count -= byte(n)
	if st.Count == 0 {
		st = itemStack{}
	}
	c.inv.set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
	c.throwItem(itemID, n)
}

// throwItem spawns count units of itemID flying from the player's eyes in
// the direction they look (vanilla drop velocity).
func (c *ClientConnection) throwItem(itemID int32, count int) {
	if count <= 0 || c.instance == nil || c.player == nil {
		return
	}
	s := c.player.Snapshot()
	yaw := float64(s.Yaw) * math.Pi / 180
	pitch := float64(s.Pitch) * math.Pi / 180
	vx := -math.Sin(yaw) * math.Cos(pitch) * dropSpeed
	vz := math.Cos(yaw) * math.Cos(pitch) * dropSpeed
	vy := -math.Sin(pitch)*dropSpeed + dropLift
	c.instance.ThrowItem(itemID, count, s.X, s.Y+eyeHeight-0.3, s.Z, vx, vy, vz)
}

// dropCursor throws whatever the client is carrying on its cursor (window
// closed with an item picked up) and clears it.
func (c *ClientConnection) dropCursor() {
	if c.cursor.empty() {
		return
	}
	st := c.cursor
	c.cursor = itemStack{}
	c.throwItem(st.ID, int(st.Count))
}

// itemTotals sums the player's modelled items — inventory slots + cursor,
// plus the open chest's slots when chest is non-nil — per item id.
func (c *ClientConnection) itemTotals(chest chestStore) map[int32]int {
	totals := map[int32]int{}
	for _, st := range c.inv.slots {
		if !st.empty() {
			totals[st.ID] += int(st.Count)
		}
	}
	if !c.cursor.empty() {
		totals[c.cursor.ID] += int(c.cursor.Count)
	}
	if chest != nil {
		for _, st := range chest.contents() {
			if !st.empty() {
				totals[st.ID] += int(st.Count)
			}
		}
	}
	return totals
}

// dropDeficit throws every unit that was in before but isn't in after: the
// client's view of a click where items left all modelled containers.
func (c *ClientConnection) dropDeficit(before, after map[int32]int) {
	for id, n := range before {
		if missing := n - after[id]; missing > 0 {
			c.throwItem(id, missing)
		}
	}
}
