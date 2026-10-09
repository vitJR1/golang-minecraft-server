package server

import (
	"time"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// game_bridge_items.go: the inventory / health / effect / title half of the
// game.PlayerHandle bridge plus the rule switches on game.Instance (see
// game_bridge.go for the original surface).

// toGameStack converts a model stack for plugins. slot is the window-0
// index (or -1 when unknown). Enchantments and Lore are copied.
func toGameStack(st itemStack, slot int) game.ItemStack {
	if st.empty() {
		return game.ItemStack{Slot: slot}
	}
	name, _ := world.ItemName(st.ID)
	out := game.ItemStack{
		Item: name, Count: int(st.Count), Name: st.Name, Color: st.Color,
		Potion: st.Potion, Damage: st.Damage, Slot: slot,
	}
	if len(st.Lore) > 0 {
		out.Lore = append([]string(nil), st.Lore...)
	}
	if len(st.Enchantments) > 0 {
		out.Enchantments = make(map[string]int, len(st.Enchantments))
		for k, v := range st.Enchantments {
			out.Enchantments[k] = v
		}
	}
	return out
}

// fromGameStack converts a plugin stack to the model. ok is false for an
// unknown item id.
func fromGameStack(gs game.ItemStack) (itemStack, bool) {
	if gs.Empty() {
		return itemStack{}, true
	}
	id, ok := world.ItemByName(gs.Item)
	if !ok {
		return itemStack{}, false
	}
	count := gs.Count
	if count > 64 {
		count = 64
	}
	st := itemStack{ID: id, Count: byte(count), Name: gs.Name, Color: gs.Color, Potion: gs.Potion, Damage: gs.Damage}
	if len(gs.Lore) > 0 {
		st.Lore = append([]string(nil), gs.Lore...)
	}
	if len(gs.Enchantments) > 0 {
		st.Enchantments = make(map[string]int, len(gs.Enchantments))
		for k, v := range gs.Enchantments {
			if v > 0 {
				st.Enchantments[k] = v
			}
		}
	}
	return st, true
}

func (b playerBridge) GiveStack(gs game.ItemStack) int {
	st, ok := fromGameStack(gs)
	if !ok || st.empty() {
		return gs.Count
	}
	// Stacks above the wire byte come in chunks.
	remaining := gs.Count
	for remaining > 0 {
		chunk := min(remaining, 64)
		st.Count = byte(chunk)
		left := b.conn.giveStack(st)
		remaining -= chunk - left
		if left > 0 {
			break
		}
	}
	return remaining
}

func (b playerBridge) SetSlot(slot int, gs game.ItemStack) {
	st, ok := fromGameStack(gs)
	if !ok {
		return
	}
	b.conn.setSlot(int16(slot), st)
}

func (b playerBridge) ClearInventory() { b.conn.clearInventory() }

func (b playerBridge) Inventory() []game.ItemStack {
	snap := b.conn.inventorySnapshot()
	out := make([]game.ItemStack, 0, len(snap))
	for _, is := range snap {
		out = append(out, toGameStack(is.stack, int(is.slot)))
	}
	return out
}

func (b playerBridge) Health() float32 { return b.conn.player.Health() }

func (b playerBridge) SetHealth(h float32) {
	b.conn.player.SetHealth(h)
	_ = b.conn.sendSetHealth(b.conn.player.Health())
}

func (b playerBridge) ApplyEffect(name string, level int, d time.Duration) {
	if e, ok := knownEffects[name]; ok {
		b.conn.applyEffect(e, level, d)
	}
}

func (b playerBridge) RemoveEffect(name string) {
	if e, ok := knownEffects[name]; ok {
		b.conn.removeEffect(e)
	}
}

func (b playerBridge) SendTitle(title, subtitle string, fadeIn, stay, fadeOut int) {
	_ = b.conn.sendTitle(title, subtitle, fadeIn, stay, fadeOut)
}

func (b playerBridge) PlaySound(name string, volume, pitch float32) {
	s := b.conn.player.Snapshot()
	_ = b.conn.safeWrite(CbPlaySoundEffect,
		soundEffectPayload(name, soundCategoryPlayer, s.X, s.Y, s.Z, volume, pitch))
}

// Respawn brings a dead (or alive) player back at (x, y, z) with full
// health: the game-driven counterpart of combat.go's respawn paths, for
// instances running SetCustomRespawn.
func (b playerBridge) Respawn(x, y, z float64) {
	c := b.conn
	c.player.Respawn()
	c.player.MoveTo(x, y, z, false)
	_ = c.sendSetHealth(player.MaxHealth)
	_ = c.sendSyncPlayerPosition(x, y, z, 1)
	c.broadcastEntityTeleport()
	c.resendEffects()
	c.equipmentChanged()
}

// Kill applies a lethal environmental hit and runs the death flow.
func (b playerBridge) Kill() {
	c := b.conn
	if c.player == nil || c.player.IsDead() || c.instance == nil {
		return
	}
	c.player.ApplyDamage(player.MaxHealth*10, c.instance.Tick(), 0)
	c.die(nil)
}

func (b instanceBridge) SetCustomRespawn(enabled bool) { b.inst.SetCustomRespawn(enabled) }
func (b instanceBridge) SetWeaponDamage(enabled bool)  { b.inst.SetWeaponDamage(enabled) }
func (b instanceBridge) SetTNTAutoPrime(enabled bool)  { b.inst.SetTNTAutoPrime(enabled) }

func (b instanceBridge) PlaySound(name string, x, y, z float64, volume, pitch float32) {
	b.inst.playSound(name, soundCategoryPlayer, x, y, z, volume, pitch)
}

func (b instanceBridge) ThrowProjectile(p game.PlayerHandle, item string, speed float64, hooks game.ProjectileHooks) {
	pb, ok := p.(playerBridge)
	if !ok {
		return
	}
	b.inst.launchProjectile(pb.conn, item, speed, hooks.OnTick, hooks.OnImpact)
}
