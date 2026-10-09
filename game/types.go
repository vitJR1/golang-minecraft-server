// Package game is the plugin-API surface for mini-game implementations.
//
// A mini-game lives in its own Go package and registers itself in init()
// via Register. The server's matchmaker (or admin commands) then create
// Instances of that game, each with its own world cloned from the
// definition's template, and each driven by a fresh Logic value.
//
// game/ deliberately does NOT import server/. It exposes Instance and
// PlayerHandle as interfaces; server/ provides adapter types that
// implement them. This keeps the plugin surface small (callers can only
// see what the interfaces declare) and prevents plugins from reaching
// into wire-level concerns like raw connections, encryption, or packet
// framing.
package game

import (
	"minecraft-server/player"
	"minecraft-server/world"
	"time"
)

// Definition is the static metadata for a mini-game: how it's matched,
// what world it uses, and how to create a fresh Logic for each round.
// Registered once via Register(); the same Definition is reused for every
// instance of the game.
type Definition struct {
	// ID is the stable, lowercase identifier used in commands and matchmaker
	// queues ("skywars", "bedwars-doubles").
	ID string

	// Name is the human-readable name shown in UIs.
	Name string

	// MinPlayers and MaxPlayers bound the lobby size. Matchmaker waits for
	// at least MinPlayers; refuses past MaxPlayers.
	MinPlayers int
	MaxPlayers int

	// Template is the world snapshot every round starts from. Each
	// Instance gets its own MemoryWorld via Template.Instantiate().
	Template *world.Template

	// New constructs a fresh Logic value for one round. Called once per
	// Instance; the returned Logic owns that round's mutable state.
	New func() Logic
}

// Logic is the per-instance behavior a mini-game implements. Every method
// runs on the server's hot path; keep them fast. Long-running work goes
// in a goroutine the implementation spawns itself.
//
// Most methods have no return value; OnBlockBreak/OnBlockPlace return
// false to veto the change (client rolls back), and OnChat may rewrite
// the message or drop it entirely.
//
// Embed NoopLogic to satisfy the interface with default behavior, then
// override only the hooks you care about.
type Logic interface {
	// OnInstanceStart fires once after the instance is created and ready
	// to receive players. World is already populated from the template.
	OnInstanceStart(*Ctx)

	// OnInstanceEnd fires once after the last player leaves OR
	// Ctx.EndGame() is called. World may still be inspected here; after
	// the hook returns the instance is destroyed.
	OnInstanceEnd(*Ctx)

	// OnPlayerJoin fires after the player is fully visible in the
	// instance (tab list + spawned entity for others).
	OnPlayerJoin(*Ctx, PlayerHandle)

	// OnPlayerLeave fires before the player is removed from the
	// instance's player list. The player is still queryable via Ctx
	// during this hook.
	OnPlayerLeave(*Ctx, PlayerHandle)

	// OnTick fires once per game tick (20 Hz). Use the tick counter to
	// schedule periodic events ("if tick % 20 == 0", "if tick == start + 600").
	OnTick(*Ctx, uint64)

	// OnBlockBreak returns true to allow the break, false to veto.
	// Vetoed breaks send a corrective Block Update so the client rolls
	// back its prediction.
	OnBlockBreak(*Ctx, PlayerHandle, world.Position) bool

	// OnBlockPlace returns true to allow, false to veto.
	OnBlockPlace(*Ctx, PlayerHandle, world.Position, world.Block) bool

	// OnBlockInteract fires for every left- or right-click on a block,
	// before the core interprets it as a dig, a placement, or a container
	// open. Return false to consume the click (nothing else happens and
	// the client's prediction is rolled back); true lets it proceed to
	// OnBlockBreak/OnBlockPlace as usual. This is the hook for selection
	// wands and other tool items.
	OnBlockInteract(*Ctx, PlayerHandle, BlockInteraction) bool

	// OnEntityInteract fires when a player right-clicks a world entity
	// (villager NPC, item frame, …). Return false to consume the click so
	// the core's default (item-frame insert/rotate) doesn't run — this is
	// where shop NPCs open their menu.
	OnEntityInteract(*Ctx, PlayerHandle, EntityInteraction) bool

	// OnChat may rewrite the outgoing text and/or veto delivery. Return
	// (msg, true) for unchanged + allow, ("", false) for drop.
	OnChat(*Ctx, PlayerHandle, string) (string, bool)

	// OnPlayerAttack fires when one player sends an "attack" Interact
	// targeted at another player in the same instance. Return false to veto
	// the hit — no damage, knockback, or death results (use for teams,
	// spawn protection, spectators). Return true to let the core 1.9 combat
	// system resolve the hit (when the instance has PvP enabled).
	OnPlayerAttack(ctx *Ctx, attacker, target PlayerHandle) bool

	// OnPlayerDeath fires when the combat system kills a player. killer is
	// the attacker, or nil for an environmental/unknown death. Award kills,
	// drops, or scoreboards here. The respawn (death screen or instant, per
	// the instance's combat config) is handled by the server around this
	// call; a game may additionally Teleport the victim to a custom spawn.
	// With Instance.SetCustomRespawn(true) the server does nothing after
	// this hook: the player stays dead until the game calls
	// PlayerHandle.Respawn.
	OnPlayerDeath(ctx *Ctx, victim, killer PlayerHandle)

	// OnItemUse fires for a right-click in the air with the held item,
	// before the core's own item behaviours (buckets, throwables, eating).
	// Return false to consume the click. Games use it for custom items
	// recognised by their display name (bridge egg, pop-up tower).
	OnItemUse(ctx *Ctx, p PlayerHandle, use ItemUse) bool

	// OnItemConsume fires when a player finishes eating or drinking the
	// stack. Return true if the game handled the effect (the core then only
	// consumes the item); false lets the vanilla default run (golden apple
	// heal, potion effect from its potion id).
	OnItemConsume(ctx *Ctx, p PlayerHandle, st ItemStack) bool
}

// Ctx carries the per-instance handles the Logic needs. Constructed by
// the server and passed into every hook call.
type Ctx struct {
	// InstanceID is the same as Instance.ID() — duplicated for cheap
	// access without a method call.
	InstanceID string

	// Instance gives the Logic access to its own world and players.
	Instance Instance
}

// Instance is the slice of *server.Instance the plugin is allowed to
// touch. Server provides an adapter that implements this interface.
// Note that Range on the world isn't exposed: read regions with GetBlock.
type Instance interface {
	// ID returns the instance's identifier (matches Definition.ID +
	// a uniquifier for each round).
	ID() string

	// SetBlock changes a block in this instance's world. The change is
	// broadcast to every player in the instance.
	SetBlock(p world.Position, b world.Block)

	// GetBlock reads a block from this instance's world.
	GetBlock(p world.Position) world.Block

	// SetBlocks applies many block changes at once and broadcasts them
	// as per-section Update Section Blocks packets instead of one Block
	// Update each — use it for fills, pastes, and other region edits.
	SetBlocks(changes []world.BlockChange)

	// DropItem spawns count of the namespaced item (e.g. "minecraft:iron_ingot")
	// as a dropped-item entity at (x, y, z) — the BedWars generator output.
	// The item falls onto the block below, merges with a same-item drop lying
	// within a block, is collected by the first living non-spectator player
	// who walks over it, and despawns after five minutes. Returns false for
	// an unknown item id or a non-positive count.
	DropItem(x, y, z float64, itemName string, count int) bool

	// DroppedItemsNear counts the units of itemName lying within radius of
	// (x, y, z). Generators use it to cap the pile at their spawn point.
	DroppedItemsNear(x, y, z, radius float64, itemName string) int

	// SpawnHologram creates floating text hovering at (x, y, z) — an
	// invisible marker armor stand whose name is always shown, rendering
	// about half a block above the point. Legacy §-colour codes work. Use
	// the returned handle to update or remove it; it is also re-streamed to
	// players on join/respawn automatically.
	SpawnHologram(x, y, z float64, text string) Hologram

	// BroadcastChat sends a chat line to every player. An empty sender
	// renders as a server announcement (no angle brackets).
	BroadcastChat(sender, message string)

	// PlayerCount returns the number of players currently in the instance.
	PlayerCount() int

	// Players returns a snapshot of every player currently in the
	// instance. The returned slice is safe to iterate without holding any
	// lock; new joiners/leavers are not reflected after the snapshot.
	Players() []PlayerHandle

	// PlayerByName looks up a player in this instance only.
	PlayerByName(name string) (PlayerHandle, bool)

	// EndGame signals the server to tear down this instance. All players
	// are moved to the hub, then OnInstanceEnd fires, then the instance
	// is removed.
	EndGame()

	// SetPvP enables or disables the 1.9 combat system (damage, knockback,
	// death) for this instance. Instances default to PvP enabled; the hub
	// defaults to disabled. Call from OnInstanceStart.
	SetPvP(enabled bool)

	// SetInstantRespawn controls death behavior. When true (suited to
	// arenas), a killed player is immediately healed and respawned at the
	// instance spawn with no death screen. When false (the default), the
	// vanilla death screen is shown and the player respawns on click.
	SetInstantRespawn(enabled bool)

	// SetCustomRespawn makes death leave the player dead (no death screen,
	// no teleport) so the game runs its own respawn flow via
	// PlayerHandle.Respawn. Off by default.
	SetCustomRespawn(enabled bool)

	// SetWeaponDamage switches melee damage from the flat CombatConfig
	// value to the held weapon's vanilla damage (+Sharpness) with armour
	// reduction (+Protection). Off by default.
	SetWeaponDamage(enabled bool)

	// SetTNTAutoPrime makes a placed TNT block ignite immediately
	// (Hypixel-style) instead of waiting for flint and steel.
	SetTNTAutoPrime(enabled bool)

	// ThrowProjectile launches item as a projectile from p's eyes along
	// their look direction at speed blocks/tick, driven by hooks.
	ThrowProjectile(p PlayerHandle, item string, speed float64, hooks ProjectileHooks)

	// PlaySound plays a positional sound to everyone in range.
	PlaySound(name string, x, y, z float64, volume, pitch float32)
}

// Hologram is a floating-text entity created by Instance.SpawnHologram.
type Hologram interface {
	// SetText replaces the text for every viewer (no-op if unchanged).
	SetText(text string)
	// Remove despawns the hologram. Safe to call more than once.
	Remove()
}

// PlayerHandle is the safe wrapper around a connected player. Plugins
// only see this — never *server.ClientConnection or raw network state.
type PlayerHandle interface {
	// Name is the player's chosen username (immutable for the session).
	Name() string

	// EntityID is the unique-per-server entity ID for this player.
	EntityID() int32

	// Pose returns a consistent snapshot of position, rotation, and
	// gamemode at the moment of the call.
	Pose() player.Snapshot

	// Teleport moves the player to (x, y, z) within their current
	// instance, sending Synchronize Player Position to the client and
	// broadcasting Teleport Entity to the rest.
	Teleport(x, y, z float64)

	// SendMessage delivers a system chat line just to this player. The
	// text is wrapped in a JSON chat component server-side.
	SendMessage(text string)

	// SetGamemode switches the player to gamemode g and tells the client.
	SetGamemode(g player.Gamemode)

	// GiveItem adds count of the namespaced item (e.g.
	// "minecraft:iron_ingot") to the player's inventory, merging into
	// existing stacks of the same item before filling empty main-inventory
	// and hotbar slots. Unknown item ids and overflow past inventory
	// capacity are silently dropped; use Instance.DropItem to put a stack on
	// the ground instead.
	GiveItem(itemName string, count int)

	// IsOp reports whether the player is a server operator.
	IsOp() bool

	// CountItem returns how many units of the namespaced item the player
	// carries (main inventory + hotbar).
	CountItem(itemName string) int

	// TakeItem removes count units of the item from the player's inventory
	// and syncs the changed slots. Returns false — and removes nothing — if
	// the player has fewer than count.
	TakeItem(itemName string, count int) bool

	// OpenMenu shows a chest-style GUI of rows×9 slots titled title. items
	// fill the slots; onClick fires with the clicked slot index (only for
	// slots that hold an item) and the menu stays open with its contents
	// re-sent, so a shop can sell repeatedly. Closing is up to the player.
	OpenMenu(title string, rows int, items []MenuItem, onClick func(slot int))

	// Kick closes the player's connection. The reason is logged but not
	// (yet) sent as a Disconnect message — that needs the Play Disconnect
	// packet, which we haven't wired up.
	Kick(reason string)

	// GiveStack adds a stack with display/enchantment data, merging only
	// into identical stacks and respecting per-item stack limits. Returns
	// how many units did not fit.
	GiveStack(st ItemStack) int

	// SetSlot overwrites one window-0 slot (see Slot* constants); an empty
	// stack clears it.
	SetSlot(slot int, st ItemStack)

	// ClearInventory empties every slot (armour, main, hotbar, offhand).
	ClearInventory()

	// Inventory returns a snapshot of the non-empty slots with Slot set.
	Inventory() []ItemStack

	// Health / SetHealth read and write hit points (0..20).
	Health() float32
	SetHealth(h float32)

	// ApplyEffect gives a status effect by vanilla name ("speed",
	// "regeneration", …) at level (1 = I) for d; RemoveEffect ends it.
	ApplyEffect(name string, level int, d time.Duration)
	RemoveEffect(name string)

	// SendTitle shows a title/subtitle; times are ticks (≤0 = vanilla
	// defaults).
	SendTitle(title, subtitle string, fadeIn, stay, fadeOut int)

	// PlaySound plays a sound only to this player, at their position.
	PlaySound(name string, volume, pitch float32)

	// Respawn brings a dead player back at (x, y, z) with full health,
	// re-sending effects and equipment. Used with SetCustomRespawn.
	Respawn(x, y, z float64)

	// Kill kills the player as an environmental death (void, game rule).
	Kill()
}
