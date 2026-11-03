package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/packets"
	"strings"
)

func (c *ClientConnection) handlePlay(packet *bytes.Buffer, packetID int) error {
	if c.isClosed() {
		return fmt.Errorf("server closed")
	}
	// Packet budget (anticheat.go): a flood is kicked before it costs
	// anything else.
	if !c.allowPacket() {
		return nil
	}

	switch packetID {
	case packetid.SbPlayKeepAlive:
		id, err := protocol.ReadLong(packet)
		if err != nil {
			return fmt.Errorf("reading keep alive response: %w", err)
		}
		if !c.onKeepAliveResponse(id) {
			c.flag("badpackets.keepalive", fmt.Sprintf("keep-alive %d was never sent", id))
		}

	case packetid.SbPlayPong:
		id, err := protocol.ReadInt(packet)
		if err != nil {
			return fmt.Errorf("pong: %w", err)
		}
		c.onPong(id)

	case packetid.SbPlayChatMessage:
		message, err := protocol.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading chat message: %w", err)
		}
		c.onChat(message)

	case packetid.SbPlayChatCommand:
		raw, err := protocol.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading chat command: %w", err)
		}
		if !c.acceptChat(raw) {
			break
		}
		// The vanilla client sends the text after the first slash, so a
		// WorldEdit-style "//set stone" arrives as "/set stone" — the leading
		// slash is part of the command name there. RunCommand falls back to
		// the slash-less name for clients that include the slash themselves.
		slog.Info("command", "player", c.playerName, "cmd", loggedCommand(raw))
		c.server.RunCommand(c, raw)

	case packetid.SbPlaySetPos, packetid.SbPlaySetPosRot, packetid.SbPlaySetRot:
		c.countMove()
		var m packets.Move
		var err error
		switch packetID {
		case packetid.SbPlaySetPos:
			m, err = packets.ReadPos(packet)
		case packetid.SbPlaySetPosRot:
			m, err = packets.ReadPosRot(packet)
		default:
			m, err = packets.ReadRot(packet)
		}
		if err != nil {
			return err
		}
		c.onMove(m)

	case packetid.SbPlayMoveStatus:
		// On-ground flag only, sent when it changes; the server works out
		// ground itself (movement.go), so it only counts for the timer.
		c.countMove()

	case packetid.SbPlayTeleportConfirm:
		id, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("teleport confirm: %w", err)
		}
		c.onTeleportConfirm(int32(id))

	case packetid.SbPlayClientInfo:
		// Sent right after LoginSuccess (locale, view distance, chat mode,
		// displayed skin parts, main hand). No-op for now.

	case packetid.SbPlayPluginMessage:
		// Custom-channel data, e.g. vanilla client sends "minecraft:brand"
		// = "vanilla" right after login. We don't act on any of these yet.
		// Read the channel just so logs aren't noisy, then drop.
		_, _ = protocol.ReadStringFromBuf(packet)

	case packetid.SbPlayCommandSuggestReq:
		req, err := packets.ReadSuggestRequest(packet)
		if err != nil {
			return err
		}
		start, length, matches := c.server.Suggestions(c, req.Text)
		_ = c.sendCommandSuggestionsResponse(req.ID, start, length, matches)

	case packetid.SbPlaySwingArm:
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
		c.swung()

	case packetid.SbPlayUseItemOnBlock:
		u, err := packets.ReadUseItemOn(packet)
		if err != nil {
			return err
		}
		c.onUseItemOn(u)

	case packetid.SbPlayPlayerAction:
		a, err := packets.ReadPlayerAction(packet)
		if err != nil {
			return err
		}
		c.onPlayerAction(a)

	case packetid.SbPlayInteract:
		in, err := packets.ReadInteract(packet)
		if err != nil {
			return err
		}
		c.onInteract(in)

	case packetid.SbPlayPlayerAbilities:
		// 1-byte flags. Bit 0x02 = flying (creative double-tap-space, /fly).
		// Flying without permission is a fly module (fly.go).
		flags, err := packet.ReadByte()
		if err != nil {
			return fmt.Errorf("player abilities: %w", err)
		}
		c.onClientAbilities(flags)

	case packetid.SbPlayPlayerCommand:
		pc, err := packets.ReadPlayerCommand(packet)
		if err != nil {
			return err
		}
		c.onPlayerCommand(pc)

	case packetid.SbPlayClientCommand:
		// action (VarInt): 0 = perform respawn, 1 = request stats.
		action, err := protocol.ReadVarInt(packet)
		if err != nil {
			return fmt.Errorf("client command action: %w", err)
		}
		if action == 0 {
			// Only from the death screen: a living player would get a free
			// heal + teleport home, and under custom respawn (BedWars) the
			// client never sees a death screen — the game respawns it.
			if !c.player.IsDead() || c.instance.CustomRespawn() {
				c.flag("badpackets.respawn", fmt.Sprintf("respawn request (dead=%v, custom respawn=%v)",
					c.player.IsDead(), c.instance.CustomRespawn()))
				break
			}
			if err := c.respawn(); err != nil {
				return fmt.Errorf("respawn: %w", err)
			}
		}

	case packetid.SbPlayUseItem:
		// hand(VarInt) + sequence(VarInt), both unused: dispatch is by the
		// held stack.
		c.onUseItem()

	case packetid.SbPlaySetHeldItem:
		slot, err := packets.ReadHeldSlot(packet)
		if err != nil {
			return err
		}
		c.onSetHeldItem(slot)

	case packetid.SbPlaySetCreativeSlot:
		// Short slot + Slot(item). Creative players send this whenever they
		// pick/replace an inventory item; we record the item's name per slot so
		// UseItemOnBlock can place what's actually held. Trailing item NBT (if
		// any) is left unread — the per-packet buffer is discarded after.
		c.onSetCreativeSlot(packet)

	case packetid.SbPlayClickContainer:
		k, err := packets.ReadClick(packet)
		if err != nil {
			return err
		}
		c.onClickContainer(k, packet)

	case packetid.SbPlayCloseContainer:
		winID, err := packet.ReadByte()
		if err != nil {
			return fmt.Errorf("close container: window id: %w", err)
		}
		c.onCloseContainer(winID)

	default:
		slog.Debug("unknown play packet",
			"player", c.playerName,
			"packet_id", fmt.Sprintf("0x%02X", packetID),
			"length", packet.Len())
	}
	return nil
}

// secretCommands carry a password in their arguments.
var secretCommands = map[string]bool{"register": true, "login": true}

// loggedCommand is raw as it may appear in the log: the arguments of
// /register and /login (passwords) are replaced by "***".
func loggedCommand(raw string) string {
	name, args, _ := strings.Cut(raw, " ")
	if args != "" && secretCommands[strings.ToLower(strings.TrimPrefix(name, "/"))] {
		return name + " ***"
	}
	return raw
}
