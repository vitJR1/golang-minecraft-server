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
		// Silent no-op. We previously tried to parse as (VarInt txID +
		// String text) but real 1.20.1 clients send bytes that don't fit
		// that layout — first non-txID byte parsed as a VarInt string
		// length comes out as ~114 with only ~11 bytes available. Without
		// a confirmed format we'd just spam the log with parse errors,
		// and a wrong reply would risk crashing the client. Tab complete
		// remains unimplemented until we capture a real vanilla packet
		// for diff. Server.Suggestions + sendCommandSuggestionsResponse
		// are kept ready for when the format is verified.

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
		// Until we have inventory, every right-click places a Stone block on
		// the face that was clicked.
		placePos := offsetByFace(world.Position{X: bx, Y: by, Z: bz}, face)
		if hook := c.instance.OnBlockPlace; hook != nil {
			if !hook(c, placePos, world.Stone) {
				// Veto: replay the existing block back to the client to
				// roll back its placement prediction.
				_ = c.sendBlockUpdate(placePos, c.instance.World.GetBlock(placePos))
				break
			}
		}
		c.instance.SetBlock(placePos, world.Stone)

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
		// Only "attack" (1) is wired up so far. interact / interact_at go
		// to entity gameplay we haven't built (right-click NPCs, etc.).
		if atype == 1 {
			if hook := c.instance.OnPlayerAttack; hook != nil {
				if victim, ok := c.instance.Players.Get(int32(target)); ok && victim != c {
					hook(c, victim)
				}
			}
		}

	case SbPlayPlayerAbilities:
		// 1-byte flags. Bit 0x02 = flying (creative double-tap-space).
		// No server-side enforcement yet — accept whatever the client says.
		_, _ = packet.ReadByte()

	case SbPlayPlayerCommand:
		// entity_id (VarInt) + action (VarInt: sneak/sprint/etc.) +
		// jump_boost (VarInt). No-op until games care about stamina.
		_, _ = protocol.ReadVarInt(packet)
		_, _ = protocol.ReadVarInt(packet)
		_, _ = protocol.ReadVarInt(packet)

	case SbPlayUseItem:
		// hand(VarInt) + sequence(VarInt). Fires when the player
		// right-clicks in empty air. In hub we treat it as "blaze rod
		// activated" — open the navigator menu. (We don't track held
		// slot yet, but blaze rod is the only item we give, so it's the
		// only thing that can trigger this here.)
		_, _ = protocol.ReadVarInt(packet) // hand
		_, _ = protocol.ReadVarInt(packet) // sequence
		if c.instance == c.server.Hub {
			c.openHubMainMenu()
		}

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
		// Don't bother parsing changed-slots array or carried item;
		// the buffer is discarded after this handler returns.
		if m := c.menu.Load(); m != nil && winID == menuWindowID {
			if entry, ok := m.entries[slot]; ok && m.onClick != nil {
				m.onClick(c, entry)
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
		}

	default:
		slog.Debug("unknown play packet",
			"player", c.playerName,
			"packet_id", fmt.Sprintf("0x%02X", packetID),
			"length", packet.Len())
	}
	return nil
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
