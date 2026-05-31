package server

import (
	"minecraft-server/ban"
	"minecraft-server/protocol"
	"testing"
	"time"
)

// TestCmdKickDisconnectsTarget: op runs /kick on a victim → victim's
// connection is closed and the wire shows a Play Disconnect.
func TestCmdKickDisconnectsTarget(t *testing.T) {
	s := New()
	s.Ops.Add("Moderator")

	mod := pipeClientOn(t, s)
	completeOfflineLogin(t, mod, "Moderator")
	mod.startDiscardDrain()

	victim := pipeClientOn(t, s)
	completeOfflineLogin(t, victim, "Victim")

	// Victim drainer that also signals when it sees a disconnect.
	gotDisconnect := make(chan struct{}, 1)
	go func() {
		for {
			buf, err := protocol.ReadPacket(victim.conn, victim.threshold)
			if err != nil {
				return
			}
			id, err := protocol.ReadVarInt(buf)
			if err != nil {
				return
			}
			if id == CbPlayDisconnect {
				select {
				case gotDisconnect <- struct{}{}:
				default:
				}
			}
		}
	}()

	mod.write(t, SbPlayChatCommand, protocol.WriteString("kick Victim spam"))

	select {
	case <-gotDisconnect:
	case <-time.After(2 * time.Second):
		t.Fatal("Victim never saw Play Disconnect")
	}
	waitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("Victim")
		return !ok
	}, "Victim to be removed from PlayerList")
}

// TestCmdBanAddsEntryAndKicks: /ban inserts a ban that ban.IsBanned can
// see, and the target is dropped from the server.
func TestCmdBanAddsEntryAndKicks(t *testing.T) {
	t.Cleanup(func() { ban.Remove("Spammer") })

	s := New()
	s.Ops.Add("Mod")
	mod := pipeClientOn(t, s)
	completeOfflineLogin(t, mod, "Mod")
	mod.startDiscardDrain()

	spammer := pipeClientOn(t, s)
	completeOfflineLogin(t, spammer, "Spammer")
	spammer.startDiscardDrain()

	mod.write(t, SbPlayChatCommand, protocol.WriteString("ban Spammer 1h flooding"))

	waitFor(t, 2*time.Second, func() bool {
		return ban.IsBanned("Spammer") != nil
	}, "ban entry to land")
	if info := ban.IsBanned("Spammer"); info == nil || info.Reason != "flooding" {
		t.Errorf("ban info: %+v", info)
	}
	waitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("Spammer")
		return !ok
	}, "Spammer to be dropped")
}

// TestCmdMuteSuppressesChat: muted player's chat message never
// broadcasts to others; chat command still works.
func TestCmdMuteSuppressesChat(t *testing.T) {
	s := New()
	s.Ops.Add("Mod")
	mod := pipeClientOn(t, s)
	completeOfflineLogin(t, mod, "Mod")
	modCh := mod.startDrain()

	loud := pipeClientOn(t, s)
	completeOfflineLogin(t, loud, "Loud")
	loud.startDiscardDrain()

	// Drain the mutual join announces so we know what's pre-mute baseline.
	drainExpect(t, modCh, "Mod sees Loud join",
		CbPlayPlayerInfoUpdate, CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	mod.write(t, SbPlayChatCommand, protocol.WriteString("mute Loud 1m"))
	// "Muted Loud until …" reply.
	drainExpect(t, modCh, "mute reply", CbPlaySystemChat)

	// Loud tries to chat. Mod should NOT see SystemChat from it.
	loud.write(t, SbPlayChatMessage, protocol.WriteString("hello?"))

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
	s := New()
	s.Ops.Add("Mod")
	mod := pipeClientOn(t, s)
	completeOfflineLogin(t, mod, "Mod")
	mod.startDiscardDrain()

	s.Mutes.Mute("Quiet", time.Now().Add(time.Hour))
	mod.write(t, SbPlayChatCommand, protocol.WriteString("unmute Quiet"))

	waitFor(t, time.Second, func() bool {
		_, muted := s.Mutes.MutedUntil("Quiet")
		return !muted
	}, "Quiet to be unmuted")
}

// TestCmdBanRejectsBadDuration: malformed duration string surfaces to op.
func TestCmdBanRejectsBadDuration(t *testing.T) {
	s := New()
	s.Ops.Add("Mod")
	mod := pipeClientOn(t, s)
	completeOfflineLogin(t, mod, "Mod")
	ch := mod.startDrain()
	drainExpect(t, ch, "Mod solo bootstrap", CbPlayPlayerInfoUpdate)

	mod.write(t, SbPlayChatCommand, protocol.WriteString("ban Nobody 5x bogus"))
	drainExpect(t, ch, "bad duration reply", CbPlaySystemChat)

	if ban.IsBanned("Nobody") != nil {
		t.Error("malformed /ban should not have created an entry")
	}
}
