package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"strings"
)

func (c *ClientConnection) handlePlay(packet *bytes.Buffer, packetID int) error {
	if c.isClosed() {
		return fmt.Errorf("server closed")
	}

	switch packetID {
	case SbPlayKeepAlive:
		id, err := protocol.ReadLong(packet)
		if err != nil {
			return fmt.Errorf("reading keep alive response: %w", err)
		}
		c.onKeepAliveResponse(id)

	case SbPlayChatMessage:
		message, err := protocol.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading chat message: %w", err)
		}
		// Auth gate: until /login or /register succeeds, chat is silent
		// for everyone else. Player gets a reminder line.
		if !gateAuth(c, "Please /login or /register before chatting.") {
			break
		}
		// Mute check happens before the moderator + per-instance OnChat
		// hook so games and bots can't accidentally bypass it. The muted
		// player gets a one-line reminder; nobody else sees anything.
		if until, muted := c.server.Mutes.MutedUntil(c.playerName); muted {
			_ = c.sendSystemMessage(fmt.Sprintf("You are muted until %s",
				until.Format("2006-01-02 15:04:05")))
			break
		}
		// Custom chat bot (e.g. anti-spam / anti-flame). Returns the text
		// to broadcast and an allow flag; may also install a mute.
		if rewritten, allow := c.server.applyChatModerator(c, message); !allow {
			break
		} else {
			message = rewritten
		}
		slog.Info("chat", "player", c.playerName, "msg", message)
		if hook := c.instance.OnChat; hook != nil {
			rewrite, allow := hook(c, message)
			if !allow {
				break
			}
			message = rewrite
		}
		c.instance.BroadcastChat(c.playerName, message)

	case SbPlayChatCommand:
		raw, err := protocol.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading chat command: %w", err)
		}
		// Strip the leading slash if the client included it (it usually does
		// not — the client sends just the name+args).
		raw = strings.TrimPrefix(raw, "/")
		slog.Info("command", "player", c.playerName, "cmd", raw)
		c.server.RunCommand(c, raw)

	case SbPlaySetPos:
		x, y, z, onGround, err := readPosOnGround(packet)
		if err != nil {
			return fmt.Errorf("set player position: %w", err)
		}
		c.player.MoveTo(x, y, z, onGround)
		c.broadcastEntityTeleport()

	case SbPlaySetPosRot:
		x, y, z, err := readXYZ(packet)
		if err != nil {
			return fmt.Errorf("set player position+rotation: %w", err)
		}
		yaw, pitch, onGround, err := readYawPitchOnGround(packet)
		if err != nil {
			return fmt.Errorf("set player position+rotation: %w", err)
		}
		c.player.MoveAndLook(x, y, z, yaw, pitch, onGround)
		c.broadcastEntityTeleport()
		c.broadcastHeadRotation()

	case SbPlaySetRot:
		yaw, pitch, onGround, err := readYawPitchOnGround(packet)
		if err != nil {
			return fmt.Errorf("set player rotation: %w", err)
		}
		c.player.LookAt(yaw, pitch, onGround)
		c.broadcastEntityTeleport()
		c.broadcastHeadRotation()

	case SbPlayTeleportConfirm:
		_, _ = protocol.ReadVarInt(packet) // teleport id, no-op

	case SbPlayClientInfo:
		// Sent right after LoginSuccess (locale, view distance, chat mode,
		// displayed skin parts, main hand). No-op for now.

	case SbPlayPluginMessage:
		// Custom-channel data, e.g. vanilla client sends "minecraft:brand"
		// = "vanilla" right after login. We don't act on any of these yet.
		// Read the channel just so logs aren't noisy, then drop.
		_, _ = protocol.ReadStringFromBuf(packet)

	case SbPlayCommandSuggestReq:
		// VarInt(transactionId) + String(text). The earlier "parse fails"
		// was because we were listening on the wrong packet id (0x08 =
		// settings, not tab_complete). With the right id (0x09) the
		// fields line up.
		txID, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("suggest req txID: %w", err)
		}
		text, err := protocol.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("suggest req text: %w", err)
		}
		start, length, matches := c.server.Suggestions(c, text)
		_ = c.sendCommandSuggestionsResponse(int32(txID), start, length, matches)

	case SbPlaySwingArm:
		hand, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("swing arm: %w", err)
		}
		// Wire-level "animation" enum: 0 = main-arm swing, 3 = off-hand swing.
		anim := byte(0)
		if hand == 1 {
			anim = 3
		}
		c.broadcastEntityAnimation(anim)

	case SbPlayUseItemOnBlock:
		// hand(VarInt) + Position(8) + face(VarInt) + cursor x/y/z(3×Float)
		// + inside_block(bool) + sequence(VarInt)
		_, _ = protocol.ReadVarInt(packet) // hand
		bx, by, bz, err := protocol.ReadPosition(packet)
		if err != nil {
			return fmt.Errorf("use item on: position: %w", err)
		}
		face, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("use item on: face: %w", err)
		}
		// Drain cursor floats + inside_block; we don't need them yet.
		_, _ = protocol.ReadFloat(packet)
		_, _ = protocol.ReadFloat(packet)
		_, _ = protocol.ReadFloat(packet)
		_, _ = protocol.ReadBool(packet)
		seq, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("use item on: sequence: %w", err)
		}
		// Acknowledge the client's prediction first so it doesn't roll back.
		_ = c.sendAckBlockChange(int32(seq))

		// Right-clicking a chest opens it instead of placing a block.
		clickedPos := world.Position{X: bx, Y: by, Z: bz}
		if isChestBlock(c.instance.World.GetBlock(clickedPos)) {
			c.openBlockChest(clickedPos)
			break
		}

		placePos := offsetByFace(world.Position{X: bx, Y: by, Z: bz}, face)
		held := c.heldItemName()

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
					Facing:  byte(face),
					Glowing: held == "minecraft:glow_item_frame",
				},
			})
			break
		}

		// Beds are two blocks (foot + head with a facing) — handled specially.
		if bed, ok := bedFromItem(held); ok {
			c.placeBed(placePos, bed)
			break
		}

		// Place a block ONLY if the held item is a real block. No block in
		// hand (empty slot, or a non-block item like a sword) → nothing
		// happens; the always-place-stone fallback is gone.
		block, ok := world.BlockByName(held)
		if !ok || block == world.Air {
			break
		}
		if hook := c.instance.OnBlockPlace; hook != nil {
			if !hook(c, placePos, block) {
				// Veto: replay the existing block back to the client to
				// roll back its placement prediction.
				_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
				break
			}
		}
		c.instance.SetBlock(placePos, block)

	case SbPlayPlayerAction:
		// action(VarInt) + Position(8) + face(Byte) + sequence(VarInt)
		action, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("player action: %w", err)
		}
		bx, by, bz, err := protocol.ReadPosition(packet)
		if err != nil {
			return fmt.Errorf("player action: position: %w", err)
		}
		_, _ = packet.ReadByte() // face — we don't differentiate
		seq, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("player action: sequence: %w", err)
		}
		_ = c.sendAckBlockChange(int32(seq))
		// action 0 = started digging (creative breaks instantly),
		// action 1 = cancelled, 2 = finished digging (survival),
		// 3 = drop item stack, 4 = drop item, 5 = shoot arrow / finish eating,
		// 6 = swap held items. We treat 0/2 as "break this block".
		if action == 0 || action == 2 {
			pos := world.Position{X: bx, Y: by, Z: bz}
			if hook := c.instance.OnBlockBreak; hook != nil {
				if !hook(c, pos) {
					_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
					break
				}
			}
			c.instance.SetBlock(pos, world.Air)
		}

	case SbPlayInteract:
		// target_eid(VarInt) + type(VarInt: 0=interact, 1=attack, 2=interact_at)
		// + (if type==2) 3×Float + hand(VarInt) + sneaking(bool).
		target, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("interact target: %w", err)
		}
		atype, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("interact type: %w", err)
		}
		switch atype {
		case 1: // attack
			if victim, ok := c.instance.Players.Get(int32(target)); ok && victim != c {
				// The game logic hook gets first refusal: returning false
				// vetoes the hit (no damage), so a game can implement teams,
				// spawn protection, spectators, etc. A nil hook = allow.
				allow := true
				if hook := c.instance.OnPlayerAttack; hook != nil {
					allow = hook(c, victim)
				}
				if allow {
					c.handleAttack(victim)
				}
			}
		case 0: // interact (right-click). Only "interact" (not interact_at, 2)
			// drives the action so a single right-click isn't processed twice.
			// Item frames: insert the held item, or rotate the existing one.
			c.instance.FrameInteract(int32(target), c.heldItemName())
		}

	case SbPlayPlayerAbilities:
		// 1-byte flags. Bit 0x02 = flying (creative double-tap-space).
		// No server-side enforcement yet — accept whatever the client says.
		_, _ = packet.ReadByte()

	case SbPlayPlayerCommand:
		// entity_id (VarInt) + action (VarInt) + jump_boost (VarInt).
		// We track sprint start/stop (actions 3/4) so combat can apply the
		// 1.9 sprint-knockback bonus; the rest is ignored for now.
		_, _ = protocol.ReadVarInt(packet)
		action, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("player command action: %w", err)
		}
		_, _ = protocol.ReadVarInt(packet)
		switch action {
		case 3: // start sprinting
			c.sprinting.Store(true)
		case 4: // stop sprinting
			c.sprinting.Store(false)
		}

	case SbPlayClientCommand:
		// action (VarInt): 0 = perform respawn, 1 = request stats.
		action, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("client command action: %w", err)
		}
		if action == 0 {
			if err := c.respawn(); err != nil {
				return fmt.Errorf("respawn: %w", err)
			}
		}

	case SbPlayUseItem:
		// hand(VarInt) + sequence(VarInt). Dispatches by the player's
		// currently-held hotbar slot (tracked via SbPlaySetHeldItem)
		// against the items we hand out:
		//   slot 0 (blaze rod, "Navigator") — opens the hub picker
		//                                     in hub and in any lobby
		//   slot 1 (ender pearl, "Arena selector") — opens the
		//                                            current-lobby's
		//                                            arena chest
		// Other slots / instances no-op.
		_, _ = protocol.ReadVarInt(packet) // hand
		_, _ = protocol.ReadVarInt(packet) // sequence
		// Throwable items (egg / snowball / ender pearl) take priority — a
		// creative-placed throwable in any slot is thrown. Server-given menu
		// items (blaze rod, arena-selector pearl) aren't creative-tracked, so
		// they fall through to the slot-based menu dispatch below.
		if held := c.heldItemName(); held != "" {
			if _, ok := throwableEntityID(held); ok {
				c.throwProjectile(held)
				break
			}
		}
		switch c.heldSlot.Load() {
		case 0:
			if c.instance == c.server.Hub {
				c.openHubMainMenu()
				break
			}
			if _, ok := arenasForLobby(c.instance.ID); ok {
				c.openHubMainMenu()
			}
		case 1:
			// BedWars lobby gets the live DOTA arena browser (create/join);
			// other lobbies keep the placeholder arena list for now.
			if c.instance.ID == LobbyBedWars {
				c.openBedwarsArenaMenu()
			} else if arenas, ok := arenasForLobby(c.instance.ID); ok {
				c.openArenaMenu(c.instance.ID, arenas)
			}
		}

	case SbPlaySetHeldItem:
		// Int16 — the new hotbar slot (0..8). Saved on the connection
		// for SbPlayUseItem dispatch above.
		raw, err := protocol.ReadUShortFromBuf(packet)
		if err != nil {
			return fmt.Errorf("set held item: %w", err)
		}
		slot := int16(raw) // signed cast preserves bits; vanilla sends 0..8
		c.heldSlot.Store(int32(slot))

	case SbPlaySetCreativeSlot:
		// Short slot + Slot(item). Creative players send this whenever they
		// pick/replace an inventory item; we record the item's name per slot so
		// UseItemOnBlock can place what's actually held. Trailing item NBT (if
		// any) is left unread — the per-packet buffer is discarded after.
		c.onSetCreativeSlot(packet)

	case SbPlayClickContainer:
		// Window ID(UByte) + State ID(VarInt) + Slot(Short) + Button(Byte)
		// + Mode(VarInt) + array of changed slots + carried item. We only
		// use Slot — the rest is parsed-then-dropped because we control
		// every menu (no real player inventory transfers happen).
		winID, err := packet.ReadByte()
		if err != nil {
			return fmt.Errorf("click container: window id: %w", err)
		}
		_, _ = protocol.ReadVarInt(packet) // state id
		slotU, err := protocol.ReadUShortFromBuf(packet)
		if err != nil {
			return fmt.Errorf("click container: slot: %w", err)
		}
		// Slot can be -999 (drop outside window) which arrives as 0xFC19;
		// the int16 cast preserves the bits and gives us back the sign.
		slot := int16(slotU)
		// Drain the rest defensively — we don't care, but a future
		// handler might if it doesn't get drained first.
		_, _ = packet.ReadByte()           // button
		_, _ = protocol.ReadVarInt(packet) // mode
		if m := c.menu.Load(); m != nil && winID == menuWindowID {
			switch {
			case m.kind == "chest":
				// Persist the client's computed slot changes to the chest.
				c.applyChestClick(packet, m.chestPos)
			default:
				// Navigation menu: dispatch the clicked icon. The rest of the
				// packet (changed slots, carried item) is discarded.
				if entry, ok := m.entries[slot]; ok && m.onClick != nil {
					m.onClick(c, entry)
				}
			}
		}

	case SbPlayCloseContainer:
		// Window ID(UByte). Clear our menu state if it matches AND re-send
		// the blaze rod — opening a chest temporarily wipes the client's
		// view of the player inventory, and even with the mirror trick a
		// stray ghost-click on a menu icon could leave the cursor holding
		// air. Re-stamping the hotbar slot is cheap and idempotent.
		winID, err := packet.ReadByte()
		if err != nil {
			return fmt.Errorf("close container: window id: %w", err)
		}
		if winID == menuWindowID {
			c.menu.Store(nil)
			giveBlazeRod(c)
			if c.instance != nil {
				if _, ok := arenasForLobby(c.instance.ID); ok {
					giveArenaSelector(c)
				}
			}
		}

	default:
		slog.Debug("unknown play packet",
			"player", c.playerName,
			"packet_id", fmt.Sprintf("0x%02X", packetID),
			"length", packet.Len())
	}
	return nil
}

// isChestBlock reports whether b is a chest the player can open
// (chest / trapped_chest / ender_chest). Matched by name suffix so all
// chest variants count.
func isChestBlock(b world.Block) bool {
	return strings.HasSuffix(b.Name, "chest")
}

// offsetByFace returns the neighbor block position on the given face of p.
// Used to compute placement when the client right-clicks a face: the new
// block sits on the side they clicked.
func offsetByFace(p world.Position, face int) world.Position {
	switch face {
	case 0: // -Y (bottom face → block below)
		return world.Position{X: p.X, Y: p.Y - 1, Z: p.Z}
	case 1: // +Y (top face → block above)
		return world.Position{X: p.X, Y: p.Y + 1, Z: p.Z}
	case 2: // -Z (north face)
		return world.Position{X: p.X, Y: p.Y, Z: p.Z - 1}
	case 3: // +Z (south face)
		return world.Position{X: p.X, Y: p.Y, Z: p.Z + 1}
	case 4: // -X (west face)
		return world.Position{X: p.X - 1, Y: p.Y, Z: p.Z}
	case 5: // +X (east face)
		return world.Position{X: p.X + 1, Y: p.Y, Z: p.Z}
	}
	return p
}
