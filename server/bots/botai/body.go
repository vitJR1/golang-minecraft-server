package botai

import (
	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// Mob is a player as a bot sees it: itself, an enemy, a teammate. The server
// implements it over its connections; two Mobs of one player compare equal.
type Mob interface {
	// ID is the entity id.
	ID() int32
	// Snapshot is the player's position, look, health, gamemode.
	Snapshot() player.Snapshot
	// Sneaking: the client says it holds sneak (a lower hitbox).
	Sneaking() bool
	// Valid: still connected and in the game (logged in, not closed).
	Valid() bool
	// Handle is the player as the game's hooks see it.
	Handle() game.PlayerHandle
	// Bot is the player's brain when it is a bot, else nil.
	Bot() *Bot
}

// Body is what a bot acts through: the server's side of its connection. The
// bot calls it only from its tick (the instance's tick goroutine).
type Body interface {
	// Self is the bot as a Mob (what World.Mobs lists for it).
	Self() Mob
	// Closed: the bot was removed.
	Closed() bool
	// World is the instance the bot is in; nil when it's in none.
	World() World
	// SameWorld reports whether m is in the bot's instance.
	SameWorld(m Mob) bool

	// Feet and Eye are the bot's position and eye point.
	Feet() anticheat.Vec
	Eye() anticheat.Vec
	// Move puts the bot at (x, y, z) looking (yaw, pitch) and, when show,
	// tells the other players.
	Move(x, y, z float64, yaw, pitch float32, onGround, show bool)
	// Look turns the head without moving (before a shot leaves).
	Look(yaw, pitch float32, onGround bool)
	// SetSprinting is the bot's sprint key.
	SetSprinting(on bool)
	// SpeedLevel / JumpLevel are its Speed / Jump Boost effect levels.
	SpeedLevel() int
	JumpLevel() int
	// FlightExempt: it may fly (a lobby) — it doesn't jump then.
	FlightExempt() bool

	// Slot is a window-0 slot of its inventory; HeldSlot the selected
	// hotbar slot (0..8).
	Slot(i int16) inventory.Stack
	HeldSlot() int32
	// SelectSlot selects hotbar slot k (cancelling a bow draw / eating) and
	// shows the new item in hand.
	SelectSlot(k int32)
	// SwapSlots swaps two window-0 slots (the bot reselects afterwards).
	SwapSlots(a, b int16)
	// CountItem counts an item by id ("minecraft:white_wool").
	CountItem(item string) int
	// HeldItem is the id of the item in hand ("" for none).
	HeldItem() string
	// DigContext is what digging speed depends on besides the tool.
	DigContext() world.DigContext

	// Swing plays the arm swing.
	Swing()
	// Attack hits t, if the game allows it.
	Attack(t Mob)
	// Break breaks the block at pos like a survival dig (drops, the game's
	// veto).
	Break(pos world.Position)
	// Place puts the block in hand at pos through a player's placement
	// rules (replaceable spot, nobody in the way, the game's rewrite and
	// veto, TNT lit where the game wants). False when the hand holds no
	// block or the placement is refused.
	Place(pos world.Position) bool
	// StartDraw begins a bow draw with the bow in hand; false when none
	// started (no arrows).
	StartDraw() bool
	// Release lets go of a drawn bow (the arrow flies).
	Release()
	// CancelUse lowers a bow / stops eating.
	CancelUse()

	// RoleChanged: the bot's role changed — refresh everyone's name tags.
	RoleChanged()
}

// World is the instance a bot is in.
type World interface {
	// Block reads a block (safe off the tick too: route searches use it on
	// their own goroutine).
	Block(pos world.Position) world.Block
	// Tick is the instance's tick counter.
	Tick() uint64
	// Mobs lists the players in the instance, the bot included.
	Mobs() []Mob
	// Guide is the game's bot guide; Ctx the context its hooks take.
	Guide() game.BotGuide
	Ctx() *game.Ctx
	// BlockedByPlayer: a player's hitbox overlaps the block at pos.
	BlockedByPlayer(pos world.Position) bool
	// TNTAutoPrime: placed TNT lights itself.
	TNTAutoPrime() bool
	// DroppedItemsNear counts dropped items of an id within radius.
	DroppedItemsNear(x, y, z, radius float64, item string) int
	// Teams are the instance's team brains.
	Teams() *Teams
}
