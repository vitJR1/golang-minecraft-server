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
		_ = id

	case SbPlayChatMessage:
		message, err := protocol.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading chat message: %w", err)
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
		// + (if type==2) 3×Float + hand(VarInt) + sneaking(bool)
		// Logged for now; PvP and entity interaction land with games.
		target, _ := protocol.ReadVarInt(packet)
		atype, _ := protocol.ReadVarInt(packet)
		_, _ = target, atype

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
