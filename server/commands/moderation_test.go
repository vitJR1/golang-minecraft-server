package commands

import (
	"context"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server"
	"minecraft-server/server/servertest"
	"testing"
	"time"
)

// TestCmdKickDisconnectsTarget: op runs /kick on a victim → victim's
// connection is closed and the wire shows a Play Disconnect.
func TestCmdKickDisconnectsTarget(t *testing.T) {
	s := server.New()
	s.Ops.Add("Moderator")

	mod := servertest.Dial(t, s)
	mod.Login(t, "Moderator")
	mod.StartDiscardDrain()

	victim := servertest.Dial(t, s)
	victim.Login(t, "Victim")

	// Victim drainer that also signals when it sees a disconnect.
	gotDisconnect := make(chan struct{}, 1)
	go func() {
		for {
			buf, err := protocol.ReadPacket(victim.Conn, victim.Threshold)
			if err != nil {
				return
			}
			id, err := protocol.ReadVarInt(buf)
			if err != nil {
				return
			}
			if id == packetid.CbPlayDisconnect {
				select {
				case gotDisconnect <- struct{}{}:
				default:
				}
			}
		}
	}()

	mod.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("kick Victim spam"))

	select {
	case <-gotDisconnect:
	case <-time.After(2 * time.Second):
		t.Fatal("Victim never saw Play Disconnect")
	}
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("Victim")
		return !ok
	}, "Victim to be removed from PlayerList")
}

// TestCmdBanAddsEntryAndKicks: /ban inserts a ban that s.Bans can see
// (with the issuer recorded), and the target is dropped from the server.
func TestCmdBanAddsEntryAndKicks(t *testing.T) {
	s := server.New()
	s.Ops.Add("Mod")
	mod := servertest.Dial(t, s)
	mod.Login(t, "Mod")
	mod.StartDiscardDrain()

	spammer := servertest.Dial(t, s)
	spammer.Login(t, "Spammer")
	spammer.StartDiscardDrain()

	mod.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("ban Spammer 1h flooding"))

	servertest.WaitFor(t, 2*time.Second, func() bool {
		info, _ := s.Bans.IsBanned(context.Background(), "Spammer")
		return info != nil
	}, "ban entry to land")
	info, _ := s.Bans.IsBanned(context.Background(), "Spammer")
	if info == nil || info.Reason != "flooding" || info.IssuedBy != "Mod" {
		t.Errorf("ban info: %+v", info)
	}
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("Spammer")
		return !ok
	}, "Spammer to be dropped")
}

// TestCmdMuteSuppressesChat: muted player's chat message never
// broadcasts to others; chat command still works.
func TestCmdMuteSuppressesChat(t *testing.T) {
	s := server.New()
	s.Ops.Add("Mod")
	mod := servertest.Dial(t, s)
	mod.Login(t, "Mod")
	modCh := mod.StartDrain()

	loud := servertest.Dial(t, s)
	loud.Login(t, "Loud")
	loud.StartDiscardDrain()

	// Drain the mutual join announces so we know what's pre-mute baseline.
	servertest.DrainExpect(t, modCh, "Mod sees Loud join",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	mod.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("mute Loud 1m"))
	// "Muted Loud until …" reply.
	servertest.DrainExpect(t, modCh, "mute reply", packetid.CbPlaySystemChat)

	// Loud tries to chat. Mod should NOT see SystemChat from it.
	loud.Write(t, packetid.SbPlayChatMessage, protocol.WriteString("hello?"))

	select {
	case id, ok := <-modCh:
		if ok {
			t.Errorf("Mod saw a packet after Loud's chat (id 0x%02X) — should be silent",
				id)
		}
	case <-time.After(200 * time.Millisecond):
		// good — silence is correct
	}

	if _, muted := s.Mutes.MutedUntil("Loud"); !muted {
		t.Error("Loud should still be muted server-side")
	}
}

// TestCmdUnmuteRestoresChat verifies the round-trip.
func TestCmdUnmuteRestoresChat(t *testing.T) {
	s := server.New()
	s.Ops.Add("Mod")
	mod := servertest.Dial(t, s)
	mod.Login(t, "Mod")
	mod.StartDiscardDrain()

	s.Mutes.Mute("Quiet", time.Now().Add(time.Hour))
	mod.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("unmute Quiet"))

	servertest.WaitFor(t, time.Second, func() bool {
		_, muted := s.Mutes.MutedUntil("Quiet")
		return !muted
	}, "Quiet to be unmuted")
}

// TestCmdBanRejectsBadDuration: malformed duration string surfaces to op.
func TestCmdBanRejectsBadDuration(t *testing.T) {
	s := server.New()
	s.Ops.Add("Mod")
	mod := servertest.Dial(t, s)
	mod.Login(t, "Mod")
	ch := mod.StartDrain()
	servertest.DrainExpect(t, ch, "Mod solo bootstrap", packetid.CbPlayPlayerInfoUpdate)

	mod.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("ban Nobody 5x bogus"))
	servertest.DrainExpect(t, ch, "bad duration reply", packetid.CbPlaySystemChat)

	if info, _ := s.Bans.IsBanned(context.Background(), "Nobody"); info != nil {
		t.Error("malformed /ban should not have created an entry")
	}
}
