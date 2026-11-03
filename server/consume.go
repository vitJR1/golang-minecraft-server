package server

import (
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/consume"
	"minecraft-server/server/gameplay/effect"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/server/gameplay/sound"
	"minecraft-server/world"
)

// consume.go: items that take time to use. Right-clicking a golden apple or
// a potion starts a use (useState); the instance's consumeTick finishes it
// after consume.Ticks ticks if the player is still holding the same stack in
// the same slot and hasn't started digging, switched slots or let go
// (Player Action 5). The game gets first refusal through fireItemConsume;
// otherwise the vanilla defaults apply: a golden apple grants Regeneration
// II for 5 s and Absorption I for 2 min, a potion applies the effect of its Potion id
// (effect.FromPotion)
// and leaves a glass bottle. The same state carries a bow draw (arrow.go).

// useState is one use in progress.
type useState struct {
	slot      int32
	item      string
	startTick uint64
	inst      *Instance // whose clock startTick is on (every instance has its own)
	bow       bool      // a bow draw rather than a consume
}

// startUse records that the held item began being consumed.
func (c *ClientConnection) startUse(item string) {
	if c.instance == nil {
		return
	}
	c.beginUse(&useState{slot: c.heldSlot.Load(), item: item, startTick: c.instance.Tick(), inst: c.instance})
}

// beginUse stores a new use and shows the pose (drawn bow, raised food)
// to the other players.
func (c *ClientConnection) beginUse(u *useState) {
	c.using.Store(u)
	c.broadcastHandState(true)
}

// cancelUse forgets a use in progress (slot switch, dig, death).
func (c *ClientConnection) cancelUse() {
	if c.using.Swap(nil) != nil {
		c.broadcastHandState(false)
	}
}

// releaseUse handles Player Action 5 ("release use item"): a bow fires, a
// consumable is simply abandoned.
func (c *ClientConnection) releaseUse() {
	u := c.using.Swap(nil)
	if u == nil {
		return
	}
	c.broadcastHandState(false)
	if u.bow {
		c.shootBow(u)
	}
}

// broadcastHandState tells the other players in the instance whether this
// player's main hand is busy using an item: living-entity metadata index 8
// (bit 0x01), which the client renders as the bow-draw / eating pose. The
// player's own client tracks its own use and is skipped — a stale "idle"
// from the server would cut its draw animation short.
func (c *ClientConnection) broadcastHandState(active bool) {
	if c.player == nil || c.instance == nil {
		return
	}
	var flags byte
	if active {
		flags = consume.HandActive
	}
	c.instance.Players.Broadcast(packetid.CbPlaySetEntityMetadata, consume.HandStatePayload(c.player.EntityID, flags), c.player.EntityID)
}

// consumeTick finishes uses that have run their course. Registered on the
// instance tick (instance.go).
func (i *Instance) consumeTick(tick uint64) {
	for _, c := range i.Players.snapshot() {
		u := c.using.Load()
		if u == nil || u.bow || c.player == nil || c.player.IsDead() {
			continue
		}
		if c.heldSlot.Load() != u.slot || c.heldItemName() != u.item {
			if c.using.CompareAndSwap(u, nil) { // let go of the stack
				c.broadcastHandState(false)
			}
			continue
		}
		if elapsedOn(i, u.inst, u.startTick, tick) < consume.Ticks {
			continue
		}
		if !c.using.CompareAndSwap(u, nil) {
			continue
		}
		c.broadcastHandState(false)
		c.finishConsume(u)
	}
}

// finishConsume applies the item's effect and spends it.
func (c *ClientConnection) finishConsume(u *useState) {
	slot := int16(inventory.HotbarStart) + int16(u.slot)
	st := c.inv.Get(slot)
	if st.Empty() {
		return
	}
	if !c.instance.fireItemConsume(c, st) {
		switch u.item {
		case "minecraft:golden_apple":
			// Vanilla: no direct heal — Regeneration II for 5 s plus
			// Absorption I (two yellow hearts) for 2 minutes.
			c.applyEffect(effect.Regeneration, 2, 5*time.Second)
			c.applyEffect(effect.Absorption, 1, 2*time.Minute)
		case "minecraft:potion":
			if e, lvl, d, ok := effect.FromPotion(st.Potion); ok {
				c.applyEffect(e, lvl, d)
			}
		}
	}
	c.instance.playSound(consume.Sound(u.item), sound.CategoryPlayer, c.player.Snapshot().X, c.player.Snapshot().Y, c.player.Snapshot().Z, 1, 1)
	if u.item == "minecraft:potion" {
		// The bottle stays behind (not in creative).
		if c.gamemode() != player.Creative {
			bottle, _ := world.ItemByName("minecraft:glass_bottle")
			var now inventory.Stack
			c.inv.Edit(slot, func(cur inventory.Stack) inventory.Stack {
				if cur.ID == st.ID { // still the potion (not clicked away meanwhile)
					cur = inventory.Stack{ID: bottle, Count: 1}
				}
				now = cur
				return cur
			})
			_ = c.sendSetSlot(0, slot, now)
			c.equipmentChanged()
		}
		return
	}
	c.consumeHeld()
}

// heal adds hp up to MaxHealth and syncs the hearts bar.
func (c *ClientConnection) heal(hp float32) {
	p := c.player
	if p == nil || p.IsDead() {
		return
	}
	p.SetHealth(min(p.Health()+hp, player.MaxHealth))
	_ = c.sendSetHealth(p.Health())
}
