package server

import (
	"math/rand"

	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/server/bots/botai"
	"minecraft-server/server/gameplay/effect"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// bot_body.go: the server's side of a bot — botai.Body, World and Mob over a
// socketless ClientConnection and its Instance.

// botTick runs every bot in the instance (registered by NewInstance), in a
// fresh random order each tick: the player list is a small Go map, whose
// "random" order puts the first-added player first nearly every time, and
// the bot acting first in a tick lands its hit first. With swings on a
// fixed rhythm that decided fights between equal bots (≈ 3 to 1); the
// reaction jitter evens it out, the shuffle keeps it so.
func (i *Instance) botTick(tick uint64) {
	var bots []*botai.Bot
	i.Players.Range(func(c *ClientConnection) {
		if c.isBot() && c.bot.Ready() {
			bots = append(bots, c.bot)
		}
	})
	rand.Shuffle(len(bots), func(a, b int) { bots[a], bots[b] = bots[b], bots[a] })
	for _, b := range bots {
		b.Tick(tick)
	}
}

// feetCellNow is the cell a bot's feet are in (its physics' position).
func (c *ClientConnection) feetCellNow() world.Position { return c.bot.FeetCell() }

// countItemName counts an item by id ("" or unknown → 0).
func (c *ClientConnection) countItemName(item string) int {
	id, ok := world.ItemByName(item)
	if !ok {
		return 0
	}
	return c.countItem(id)
}

// connMob is a player as a bot sees it.
type connMob struct{ c *ClientConnection }

func (m connMob) ID() int32                 { return m.c.player.EntityID }
func (m connMob) Snapshot() player.Snapshot { return m.c.player.Snapshot() }
func (m connMob) Sneaking() bool            { return m.c.ac.Sneaking.Load() }
func (m connMob) Valid() bool               { return !m.c.isClosed() && m.c.player != nil }
func (m connMob) Handle() game.PlayerHandle { return handle(m.c) }
func (m connMob) Bot() *botai.Bot           { return m.c.bot }

// mobOf is c as a Mob.
func mobOf(c *ClientConnection) botai.Mob { return connMob{c} }

// botBody is a bot's connection as its body.
type botBody struct{ c *ClientConnection }

func (b botBody) Self() botai.Mob { return connMob{b.c} }
func (b botBody) Closed() bool    { return b.c.player == nil || b.c.isClosed() }

func (b botBody) World() botai.World {
	if b.c.instance == nil {
		return nil
	}
	return instWorld{b.c.instance}
}

func (b botBody) SameWorld(m botai.Mob) bool {
	o, ok := m.(connMob)
	return ok && o.c.instance == b.c.instance
}

func (b botBody) Feet() anticheat.Vec { return b.c.feet() }
func (b botBody) Eye() anticheat.Vec  { return b.c.eye() }

func (b botBody) Move(x, y, z float64, yaw, pitch float32, onGround, show bool) {
	// The move as the server's movement state sees it (what onMove records
	// for a player): a crit needs a falling last move (LastDY < 0), so
	// without it a bot never landed one.
	from := b.c.feet()
	b.c.player.MoveAndLook(x, y, z, yaw, pitch, onGround)
	b.c.ac.Move.Moved(from, anticheat.Vec{X: x, Y: y, Z: z})
	if show {
		b.c.broadcastEntityTeleport()
		b.c.broadcastHeadRotation()
	}
}

func (b botBody) Look(yaw, pitch float32, onGround bool) { b.c.player.LookAt(yaw, pitch, onGround) }
func (b botBody) SetSprinting(on bool)                   { b.c.ac.Sprinting.Store(on) }
func (b botBody) SpeedLevel() int                        { return b.c.effectLevel(effect.Speed) }
func (b botBody) JumpLevel() int                         { return b.c.effectLevel(effect.JumpBoost) }
func (b botBody) FlightExempt() bool                     { return b.c.flightExempt() }

func (b botBody) Slot(i int16) inventory.Stack { return b.c.inv.Get(i) }
func (b botBody) HeldSlot() int32              { return b.c.heldSlot.Load() }

func (b botBody) SelectSlot(k int32) {
	b.c.cancelUse()
	b.c.heldSlot.Store(k)
	b.c.equipmentChanged()
}

func (b botBody) SwapSlots(x, y int16) {
	b.c.inv.Slots[x], b.c.inv.Slots[y] = b.c.inv.Slots[y], b.c.inv.Slots[x]
}

func (b botBody) CountItem(item string) int    { return b.c.countItemName(item) }
func (b botBody) HeldItem() string             { return b.c.heldItemName() }
func (b botBody) DigContext() world.DigContext { return b.c.digContext() }
func (b botBody) Swing()                       { b.c.broadcastEntityAnimation(0) }
func (b botBody) Break(pos world.Position)     { b.c.breakBlock(pos, true) }
func (b botBody) Release()                     { b.c.releaseUse() }
func (b botBody) CancelUse()                   { b.c.cancelUse() }

func (b botBody) Attack(t botai.Mob) {
	o, ok := t.(connMob)
	if ok && b.c.instance.allowAttack(b.c, o.c) {
		b.c.handleAttack(o.c)
	}
}

func (b botBody) StartDraw() bool {
	b.c.startDraw()
	return b.c.using.Load() != nil
}

func (b botBody) RoleChanged() {
	inst := b.c.instance
	inst.Players.Range(func(c *ClientConnection) { c.syncNameTags(inst) })
}

// Place puts the held block at pos through the same rules as a player's
// placement; a placed TNT lights itself where the game wants it to (like a
// player's).
func (b botBody) Place(pos world.Position) bool {
	c := b.c
	block, ok := world.BlockByName(c.heldItemName())
	inst := c.instance
	if !ok || !world.Replaceable(inst.World.GetBlock(pos)) || inst.blockedByPlayer(pos) {
		return false
	}
	block = inst.rewritePlacedBlock(c, pos, block)
	if !inst.allowBlockPlace(c, pos, block) {
		return false
	}
	inst.SetBlock(pos, block)
	c.consumeHeld()
	c.broadcastEntityAnimation(0)
	if block == world.TNT && inst.TNTAutoPrime() {
		inst.primeTNT(pos, tntAutoFuse, c)
	}
	return true
}

// instWorld is an instance as a bot's world.
type instWorld struct{ i *Instance }

func (w instWorld) Block(pos world.Position) world.Block    { return w.i.World.GetBlock(pos) }
func (w instWorld) Tick() uint64                            { return w.i.Tick() }
func (w instWorld) Guide() game.BotGuide                    { return w.i.botGuide }
func (w instWorld) Ctx() *game.Ctx                          { return w.i.pluginCtx() }
func (w instWorld) BlockedByPlayer(pos world.Position) bool { return w.i.blockedByPlayer(pos) }
func (w instWorld) TNTAutoPrime() bool                      { return w.i.TNTAutoPrime() }
func (w instWorld) Teams() *botai.Teams                     { return &w.i.botTeams }

func (w instWorld) DroppedItemsNear(x, y, z, radius float64, item string) int {
	return w.i.DroppedItemsNear(x, y, z, radius, item)
}

func (w instWorld) Mobs() []botai.Mob {
	ps := w.i.Players.snapshot()
	out := make([]botai.Mob, 0, len(ps))
	for _, c := range ps {
		out = append(out, connMob{c})
	}
	return out
}
