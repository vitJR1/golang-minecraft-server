package server

import (
	"fmt"
	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/server/chest"
	"minecraft-server/server/consume"
	"minecraft-server/server/inventory"
	"minecraft-server/server/lobby"
	"minecraft-server/server/packets"
	"minecraft-server/server/projectile"
	"minecraft-server/world"
)

// handler_play_world.go: the play packets that act on the world — block
// clicks and placement, digging, entity clicks, item use.

// onUseItemOn handles Use Item On Block: a right click on a block — plugin
// clicks, TNT, fire charges, chests, item frames, beds and block placement.
func (c *ClientConnection) onUseItemOn(u packets.UseItemOn) {
	// Acknowledge the client's prediction first so it doesn't roll back.
	_ = c.sendAckBlockChange(u.Sequence)

	clickedPos := u.Pos
	placePos := clickedPos.Neighbor(u.Face)
	// Right-clicking grass & co. puts the block where the plant was.
	if world.ReplacedInPlace(c.instance.World.GetBlock(clickedPos)) {
		placePos = clickedPos
	}
	held := c.heldItemName()

	if !anticheat.CursorValid(u.CursorX, u.CursorY, u.CursorZ) {
		c.flag("badpackets.place", fmt.Sprintf("cursor %.3f,%.3f,%.3f outside the block", u.CursorX, u.CursorY, u.CursorZ))
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}
	// Dead players and spectators click nothing, and the block must be
	// within vanilla's use range (reach.go).
	if !c.mayAct() || !c.useInReach(clickedPos) {
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}

	// Plugins get first refusal on the click (selection wands, tools).
	// A consumed click places nothing: roll back the client's predicted
	// placement at the target block.
	click := game.BlockInteraction{Pos: clickedPos, Face: u.Face, Action: game.RightClick, Item: held}
	if !c.instance.allowBlockInteract(c, click) {
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}

	// Flint and steel / fire charge on a TNT block lights it.
	if c.mayBuild() && c.tryIgniteTNT(clickedPos, held) {
		return
	}

	// A fire charge on any other block is still a fireball throw: the
	// client sends Use Item On Block, not Use Item, whenever a block is
	// in reach (looking at your own feet), and it predicts placing fire
	// on the clicked face — roll that back, then launch like Use Item.
	if held == "minecraft:fire_charge" {
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		if c.instance.allowItemUse(c, game.ItemUse{Item: held, Name: c.inv.Held(c.heldSlot.Load()).Name, Slot: int(c.heldSlot.Load())}) {
			c.throwFireball()
			c.consumeHeld()
		}
		return
	}

	// Right-clicking a chest opens it instead of placing a block.
	if chest.IsChestBlock(c.instance.World.GetBlock(clickedPos)) {
		c.openBlockChest(clickedPos)
		return
	}

	// Everything below changes the world, which adventure mode can't;
	// and it's a placement: rate and clicked-face checks (actions.go).
	if !c.mayBuild() || !c.placeAllowed(clickedPos, u.Face) {
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}

	// Item frames are entities, not blocks — placing one spawns an item
	// frame on the clicked face (the UseItemOnBlock face enum 0..5 maps
	// directly onto the frame Facing enum).
	if held == "minecraft:item_frame" || held == "minecraft:glow_item_frame" {
		c.instance.AddWorldEntity(world.Entity{
			Type: held,
			X:    float64(placePos.X) + 0.5,
			Y:    float64(placePos.Y) + 0.5,
			Z:    float64(placePos.Z) + 0.5,
			Frame: &world.FrameData{
				Facing:  byte(u.Face),
				Glowing: held == "minecraft:glow_item_frame",
			},
		})
		c.consumeHeld()
		return
	}

	// Beds are two blocks (foot + head with a facing) — handled specially.
	if bed, ok := world.BedFromItem(held); ok {
		c.placeBed(placePos, bed)
		return
	}

	// Place a block ONLY if the held item is a real block. No block in
	// hand (empty slot, or a non-block item like a sword) → nothing
	// happens; the always-place-stone fallback is gone.
	block, ok := world.BlockByName(held)
	if !ok || block == world.Air {
		return
	}
	// Only into air, fluid or vegetation: a forged click must not
	// overwrite a map block (or an enemy bed). Not flagged — a vanilla
	// client gets here too when someone else just built on the spot.
	if !world.Replaceable(c.instance.World.GetBlock(placePos)) {
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}
	// Games/plugins may swap the block (BedWars: team-coloured beds).
	block = c.instance.rewritePlacedBlock(c, placePos, block)
	// Orientation / attachment rules (ladders hang on a wall).
	block, ok = c.instance.orientForPlacement(block, placePos, u.Face, c.player.Snapshot().Yaw)
	if !ok {
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}
	// Not into anyone's hitbox (including the placer's own).
	if c.instance.blockedByPlayer(placePos) {
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}
	if !c.instance.allowBlockPlace(c, placePos, block) {
		// Veto: replay the existing block back to the client to
		// roll back its placement prediction.
		_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
		return
	}
	c.instance.SetBlock(placePos, block)
	c.consumeHeld()
	switch {
	case block == world.TNT && c.instance.TNTAutoPrime():
		// Games can make TNT light itself the moment it's placed.
		c.instance.primeTNT(placePos, tntAutoFuse, c)
	case block == world.Sponge:
		c.instance.absorbWater(placePos)
	}
}

// onPlayerAction handles Player Action: digging (start / cancel / finish),
// Q drops and releasing a used item (bow, food).
func (c *ClientConnection) onPlayerAction(a packets.PlayerAction) {
	_ = c.sendAckBlockChange(a.Sequence)
	// Started digging (creative breaks instantly), cancelled, finished
	// digging (survival); drop stack / one; release the used item. The
	// starts and finishes are "break this block".
	pos := a.Pos
	switch a.Action {
	case packets.ActionRelease: // release use item: stopped eating / drinking, or loosed a bow
		c.releaseUse()
	case packets.ActionStartDig:
		c.cancelUse()
		// Only survival/creative dig, within vanilla's dig range, at a
		// sane rate, at a block in front (actions.go).
		if !c.mayBuild() || !c.digInReach(pos) || !c.breakAllowed(pos) {
			_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
			return
		}
		// Start of a dig = a left click: plugins may consume it (wand).
		// Remember the position so the survival client's matching
		// "finished digging" for the same block is swallowed too.
		click := game.BlockInteraction{Pos: pos, Face: a.Face, Action: game.LeftClick, Item: c.heldItemName()}
		if !c.instance.allowBlockInteract(c, click) {
			c.consumedDig = &pos
			_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
			return
		}
		c.consumedDig = nil
		c.startDig(pos)
	case packets.ActionCancelDig:
		c.consumedDig = nil
		c.cancelDig()
	case packets.ActionFinishDig:
		if !c.mayBuild() || !c.digInReach(pos) || !c.breakAllowed(pos) {
			c.consumedDig = nil
			c.cancelDig()
			_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
			return
		}
		if c.consumedDig != nil && *c.consumedDig == pos {
			c.consumedDig = nil
			_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
			return
		}
		c.consumedDig = nil
		c.finishDig(pos)
	case packets.ActionDropStack: // ctrl+Q: drop the whole held stack
		if c.mayAct() {
			c.dropHeld(true)
		}
	case packets.ActionDropItem: // Q: drop one unit of the held stack
		if c.mayAct() {
			c.dropHeld(false)
		}
	}
}

// onInteract handles Interact: attacking a player, right-clicking a world
// entity (shop NPCs, item frames).
func (c *ClientConnection) onInteract(in packets.Interact) {
	// A spectator's attack only asks to spectate; the dead do nothing.
	if !c.mayAct() {
		return
	}
	switch in.Type {
	case packets.InteractAttack:
		if victim, ok := c.instance.Players.Get(in.Target); ok && victim != c && victim.player != nil {
			// Lag-compensated reach (reach.go): an out-of-reach hit is
			// cancelled and flagged.
			if !c.attackInReach(victim) || !c.attackAllowed(victim) {
				return
			}
			// The game logic hook gets first refusal: returning false
			// vetoes the hit (no damage), so a game can implement teams,
			// spawn protection, spectators, etc. A nil hook = allow.
			if c.instance.allowAttack(c, victim) {
				c.handleAttack(victim)
			}
		}
	case packets.InteractUse: // right-click. Only "interact" (not interact_at)
		// drives the action so a single right-click isn't processed twice.
		held := c.heldItemName()
		if e, ok := c.instance.worldEntity(in.Target); ok {
			if !c.entityInReach(e) {
				return
			}
			// Game logic / plugins first (shop NPCs); a consumed click
			// skips the item-frame default.
			ei := game.EntityInteraction{EntityID: in.Target, Type: e.Type, X: e.X, Y: e.Y, Z: e.Z, Item: held}
			if !c.instance.allowEntityInteract(c, ei) {
				return
			}
		}
		// Item frames: insert the held item, or rotate the existing one.
		c.instance.FrameInteract(in.Target, held)
	}
}

// onUseItem handles Use Item (a right click in the air, hand + sequence
// unread): the server's menu items (the navigator opens the hub picker in the
// hub and any lobby, the arena selector the lobby's arena chest), then the
// game's veto, then food / potions, bows, fire charges, buckets, throwables.
func (c *ClientConnection) onUseItem() {
	if !c.mayAct() {
		return // spectators and the dead use nothing
	}
	// Server-given menu items are recognised by their custom name in the
	// inventory model (wherever the player moved them), so a plain ender
	// pearl is still throwable while the "Arena selector" opens a menu.
	held := c.inv.Held(c.heldSlot.Load())
	inLobby := lobby.Is(c.instance.ID)
	switch {
	case held.Name == inventory.NavigatorName:
		if c.instance == c.server.Hub || inLobby {
			c.openHubMainMenu()
		}
	case held.Name == inventory.SelectorName:
		// BedWars lobby gets the live DOTA arena browser (create/join);
		// other lobbies keep the placeholder arena list for now.
		if c.instance.ID == lobby.BedWars {
			c.openBedwarsArenaMenu()
		} else if arenas, ok := lobby.Arenas(c.instance.ID); ok {
			c.openArenaMenu(c.instance.ID, arenas)
		}
	default:
		name := c.heldItemName()
		if name == "" {
			return
		}
		// Games/plugins get first refusal on the click (bridge egg, …).
		if !c.instance.allowItemUse(c, game.ItemUse{Item: name, Name: held.Name, Slot: int(c.heldSlot.Load())}) {
			return
		}
		switch {
		case consume.Consumable(name):
			// Eating/drinking takes time: the consume tick finishes it.
			c.startUse(name)
		case name == "minecraft:bow":
			c.startDraw()
		case name == "minecraft:fire_charge":
			c.throwFireball()
			c.consumeHeld()
		case c.mayBuild() && c.useBucket(name):
			// Buckets: the client sends a plain Use Item for them, with
			// no target — the server ray-casts for the fluid / spot.
		default:
			// Throwable items (egg / snowball / ender pearl) in any slot.
			if _, ok := projectile.ThrowableEntityID(name); ok {
				c.throwProjectile(name)
				c.consumeHeld()
			}
		}
	}
}
