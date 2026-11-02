// Package gamebridge adapts the server's connections to the plugin API:
// Player is the game.PlayerHandle games and listeners get, over the
// connection's exported, server-shaped methods (item ids, inventory
// stacks, effect values) — the conversions from the plugin's names and
// game.ItemStack live here. Server imports it; it never imports server.
package gamebridge

import (
	"time"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/server/effect"
	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

// Conn is what a Player needs of a connection (server.ClientConnection).
type Conn interface {
	Name() string
	Player() *player.Player
	IsOp() bool
	SendMessage(text string) error
	Kick(reason string)
	Teleport(x, y, z float64)
	SetGamemode(g player.Gamemode)
	SetFlight(allowed bool)

	GiveItem(itemID int32, n int) int
	CountItem(itemID int32) int
	TakeItem(itemID int32, n int) bool
	GiveStack(st inventory.Stack) int
	SetSlot(slot int16, st inventory.Stack)
	Slot(slot int16) inventory.Stack
	ClearInventory()
	OpenMenu(title string, rows int, items []game.MenuItem, onClick func(game.MenuClick))

	SetHealth(h float32)
	ApplyEffect(e effect.Effect, level int, d time.Duration)
	RemoveEffect(e effect.Effect)
	SendTitle(title, subtitle string, fadeIn, stay, fadeOut int) error
	PlaySound(name string, volume, pitch float32)
	SetSidebar(title string, lines []string)
	ClearSidebar()
	SetNameTag(tag game.NameTag)
	Respawn(x, y, z float64)
	Die()
}

// Player implements game.PlayerHandle over C. Comparable: two handles of
// one connection are equal.
type Player struct{ C Conn }

var _ game.PlayerHandle = Player{}

func (p Player) Name() string                  { return p.C.Player().Name }
func (p Player) EntityID() int32               { return p.C.Player().EntityID }
func (p Player) Pose() player.Snapshot         { return p.C.Player().Snapshot() }
func (p Player) IsOp() bool                    { return p.C.IsOp() }
func (p Player) SendMessage(text string)       { _ = p.C.SendMessage(text) }
func (p Player) Kick(reason string)            { p.C.Kick(reason) }
func (p Player) Teleport(x, y, z float64)      { p.C.Teleport(x, y, z) }
func (p Player) SetGamemode(g player.Gamemode) { p.C.SetGamemode(g) }

// SetFlight grants or revokes flight, with the anticheat aware.
func (p Player) SetFlight(allowed bool) { p.C.SetFlight(allowed) }

// GiveItem resolves the namespaced item id and hands it to the inventory
// model, which stacks it and syncs the changed slots to the client. An
// unknown id is a silent no-op.
func (p Player) GiveItem(itemName string, count int) {
	if id, ok := world.ItemByName(itemName); ok {
		p.C.GiveItem(id, count)
	}
}

func (p Player) CountItem(itemName string) int {
	id, ok := world.ItemByName(itemName)
	if !ok {
		return 0
	}
	return p.C.CountItem(id)
}

func (p Player) TakeItem(itemName string, count int) bool {
	id, ok := world.ItemByName(itemName)
	if !ok {
		return false
	}
	return p.C.TakeItem(id, count)
}

func (p Player) OpenMenu(title string, rows int, items []game.MenuItem, onClick func(click game.MenuClick)) {
	p.C.OpenMenu(title, rows, items, onClick)
}

func (p Player) GiveStack(gs game.ItemStack) int {
	st, ok := FromGame(gs)
	if !ok || st.Empty() {
		return gs.Count
	}
	// Stacks above the wire byte come in chunks.
	remaining := gs.Count
	for remaining > 0 {
		chunk := min(remaining, 64)
		st.Count = byte(chunk)
		left := p.C.GiveStack(st)
		remaining -= chunk - left
		if left > 0 {
			break
		}
	}
	return remaining
}

func (p Player) SetSlot(slot int, gs game.ItemStack) {
	if st, ok := FromGame(gs); ok {
		p.C.SetSlot(int16(slot), st)
	}
}

func (p Player) ClearInventory() { p.C.ClearInventory() }

// Inventory lists every non-empty window-0 slot.
func (p Player) Inventory() []game.ItemStack {
	var out []game.ItemStack
	for slot := int16(0); slot < inventory.PlayerSlots; slot++ {
		if st := p.C.Slot(slot); !st.Empty() {
			out = append(out, ToGame(st, int(slot)))
		}
	}
	return out
}

func (p Player) Health() float32     { return p.C.Player().Health() }
func (p Player) SetHealth(h float32) { p.C.SetHealth(h) }

func (p Player) ApplyEffect(name string, level int, d time.Duration) {
	if e, ok := effect.Known[name]; ok {
		p.C.ApplyEffect(e, level, d)
	}
}

func (p Player) RemoveEffect(name string) {
	if e, ok := effect.Known[name]; ok {
		p.C.RemoveEffect(e)
	}
}

func (p Player) SendTitle(title, subtitle string, fadeIn, stay, fadeOut int) {
	_ = p.C.SendTitle(title, subtitle, fadeIn, stay, fadeOut)
}

func (p Player) PlaySound(name string, volume, pitch float32) { p.C.PlaySound(name, volume, pitch) }
func (p Player) SetSidebar(title string, lines []string)      { p.C.SetSidebar(title, lines) }
func (p Player) ClearSidebar()                                { p.C.ClearSidebar() }
func (p Player) SetNameTag(tag game.NameTag)                  { p.C.SetNameTag(tag) }
func (p Player) Respawn(x, y, z float64)                      { p.C.Respawn(x, y, z) }

// Kill is a lethal hit straight into the death flow (Conn.Die).
func (p Player) Kill() { p.C.Die() }
