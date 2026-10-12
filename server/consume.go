package server

import (
	"bytes"
	"strings"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// consume.go: items that take time to use. Right-clicking a golden apple or
// a potion starts a use (useState); the instance's consumeTick finishes it
// after consumeTicks ticks if the player is still holding the same stack in
// the same slot and hasn't started digging, switched slots or let go
// (Player Action 5). The game gets first refusal through fireItemConsume;
// otherwise the vanilla defaults apply: a golden apple grants Regeneration
// II for 5 s and Absorption I for 2 min, a potion applies the effect of its Potion id
// and leaves a glass bottle. The same state carries a bow draw (arrow.go).

const (
	consumeTicks = 32 // vanilla eat/drink duration (1.6 s)
	tntAutoFuse  = 50 // ticks before a self-primed TNT block explodes

	// Living-entity metadata index 8 "hand states": 0x01 = a hand is
	// active (using an item), 0x02 = it's the off hand.
	handStateIndex  byte = 8
	handStateActive byte = 0x01
)

// useState is one use in progress.
type useState struct {
	slot      int32
	item      string
	startTick uint64
	bow       bool // a bow draw rather than a consume
}

// isConsumable reports whether item is eaten/drunk over time.
func isConsumable(item string) bool {
	return item == "minecraft:golden_apple" || item == "minecraft:potion"
}

// startUse records that the held item began being consumed.
func (c *ClientConnection) startUse(item string) {
	if c.instance == nil {
		return
	}
	c.beginUse(&useState{slot: c.heldSlot.Load(), item: item, startTick: c.instance.Tick()})
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
		flags = handStateActive
	}
	c.instance.Players.Broadcast(CbPlaySetEntityMetadata, handStatePayload(c.player.EntityID, flags), c.player.EntityID)
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
		if tick-u.startTick < consumeTicks {
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
	slot := int16(hotbarStart) + int16(u.slot)
	st := c.inv.get(slot)
	if st.empty() {
		return
	}
	if !c.instance.fireItemConsume(c, st) {
		switch u.item {
		case "minecraft:golden_apple":
			// Vanilla: no direct heal — Regeneration II for 5 s plus
			// Absorption I (two yellow hearts) for 2 minutes.
			c.applyEffect(EffectRegeneration, 2, 5*time.Second)
			c.applyEffect(EffectAbsorption, 1, 2*time.Minute)
		case "minecraft:potion":
			if e, lvl, d, ok := potionEffect(st.Potion); ok {
				c.applyEffect(e, lvl, d)
			}
		}
	}
	c.instance.playSound(consumeSound(u.item), soundCategoryPlayer, c.player.Snapshot().X, c.player.Snapshot().Y, c.player.Snapshot().Z, 1, 1)
	if u.item == "minecraft:potion" {
		// The bottle stays behind (not in creative).
		if c.gamemode() != player.Creative {
			bottle, _ := world.ItemByName("minecraft:glass_bottle")
			c.inv.set(slot, itemStack{ID: bottle, Count: 1})
			_ = c.sendSetSlot(0, slot, c.inv.get(slot))
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

func consumeSound(item string) string {
	if item == "minecraft:potion" {
		return "minecraft:entity.generic.drink"
	}
	return "minecraft:entity.player.burp"
}

// potionEffect maps a vanilla potion id (NBT "Potion") to the effect it
// applies. Only the kinds a mini-game server hands out are covered.
func potionEffect(potion string) (Effect, int, time.Duration, bool) {
	switch strings.TrimPrefix(potion, "minecraft:") {
	case "swiftness":
		return EffectSpeed, 1, 3 * time.Minute, true
	case "long_swiftness":
		return EffectSpeed, 1, 8 * time.Minute, true
	case "strong_swiftness":
		return EffectSpeed, 2, 90 * time.Second, true
	case "leaping":
		return EffectJumpBoost, 1, 3 * time.Minute, true
	case "long_leaping":
		return EffectJumpBoost, 1, 8 * time.Minute, true
	case "strong_leaping":
		return EffectJumpBoost, 2, 90 * time.Second, true
	case "invisibility":
		return EffectInvisibility, 1, 3 * time.Minute, true
	case "long_invisibility":
		return EffectInvisibility, 1, 8 * time.Minute, true
	case "regeneration":
		return EffectRegeneration, 1, 45 * time.Second, true
	case "strong_regeneration":
		return EffectRegeneration, 2, 22 * time.Second, true
	case "slowness":
		return EffectSlowness, 1, 90 * time.Second, true
	}
	return Effect{}, 0, 0, false
}

// handStatePayload builds Set Entity Metadata with just the hand-state byte.
func handStatePayload(eid int32, flags byte) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	buf.WriteByte(handStateIndex)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // type: Byte
	buf.WriteByte(flags)
	buf.WriteByte(0xFF)
	return buf.Bytes()
}
