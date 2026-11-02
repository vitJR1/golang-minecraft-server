package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/abilities"
)

// fly_test.go: flight permission (/fly is server/commands) and its interplay
// with the movement checks.

// abilitiesSent returns the flags of the Player Abilities packets c sent.
func abilitiesSent(t *testing.T, c *ClientConnection) []byte {
	t.Helper()
	var flags []byte
	for _, p := range drainPackets(t, c) {
		if p.id == packetid.CbPlayPlayerAbil {
			if len(p.body) != 9 {
				t.Fatalf("abilities body is %d bytes, want 9 (flags + 2 floats)", len(p.body))
			}
			flags = append(flags, p.body[0])
		}
	}
	return flags
}

// pongAll answers every outstanding Ping of a fresh offline connection.
func pongAll(t *testing.T, c *ClientConnection) {
	t.Helper()
	for id := int32(1); c.ac.Transactions.Outstanding() > 0 && id < 100; id++ {
		play(t, c, packetid.SbPlayPong, protocol.WriteInt(id))
	}
}

func TestFlyTogglesMayFly(t *testing.T) {
	s := New()
	s.Ops.Add("Pilot")
	c := offlineConn(s.Hub, "Pilot", player.Survival, 0.5, 64, 0.5)
	drain(c)

	c.setFlight(!c.canFly.Load())
	if got := abilitiesSent(t, c); len(got) != 1 || got[0] != abilities.MayFly {
		t.Fatalf("/fly on sent abilities %v, want [may fly]", got)
	}
	if !c.canFly.Load() {
		t.Fatal("flight not allowed after /fly")
	}
	c.setFlight(!c.canFly.Load())
	if got := abilitiesSent(t, c); len(got) != 1 || got[0] != 0 {
		t.Fatalf("/fly off sent abilities %v, want [none]", got)
	}
}

func TestFlyKeepsCreativeAbilities(t *testing.T) {
	s := New()
	s.Ops.Add("Builder")
	c := offlineConn(s.Hub, "Builder", player.Creative, 0.5, 64, 0.5)
	drain(c)
	c.setFlight(!c.canFly.Load())
	c.setFlight(!c.canFly.Load()) // off again: creative still flies
	got := abilitiesSent(t, c)
	want := byte(abilities.Invulnerable | abilities.MayFly | abilities.Instabuild)
	if len(got) != 2 || got[1] != want {
		t.Fatalf("creative after /fly off: %v, want creative's own flags", got)
	}
}

func TestFlyExemptsMovementChecks(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Pilot", player.Survival, 0.5, 64, 0.5)
	c.setFlight(true)
	drain(c)
	path(t, c, 15.5, 72, 0.5, 15) // up and across, faster than walking
	if flaggedNow(c, "movement.fly") || flaggedNow(c, "movement.speed") {
		t.Fatal("flying with /fly was flagged")
	}

	// Off: exempt until the client confirms, checked after.
	c.setFlight(false)
	drain(c)
	setPos(t, c, 15.5, 72.5, 0.5) // still flying before the Pong: fine
	if flaggedNow(c, "movement.fly") {
		t.Fatal("flagged before the client saw flight end")
	}
	pongAll(t, c)
	path(t, c, 15.5, 76, 0.5, 10) // keeps climbing after it
	if !flaggedNow(c, "movement.fly") {
		t.Fatal("flying on after /fly off wasn't flagged")
	}
}

func TestGamemodeChangeAndRespawnDropFlight(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Pilot", player.Survival, 0.5, 64, 0.5)
	c.setFlight(true)
	_ = c.sendGameModeChange(player.Adventure)
	if c.canFly.Load() {
		t.Fatal("a gamemode change kept /fly")
	}
	c.setFlight(true)
	drain(c)
	_ = c.sendRespawn()
	if c.canFly.Load() {
		t.Fatal("a Respawn (instance move) kept /fly")
	}
}

func TestClientClaimingFlightFlagged(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Hacker", player.Survival, 0.5, 64, 0.5)
	play(t, c, packetid.SbPlayPlayerAbilities, []byte{abilities.Flying})
	if !flaggedNow(c, "badpackets.abilities") {
		t.Fatal("flying without permission not flagged")
	}
	ok := offlineConn(inst, "Pilot", player.Survival, 2.5, 64, 0.5)
	ok.setFlight(true)
	drain(ok)
	play(t, ok, packetid.SbPlayPlayerAbilities, []byte{abilities.Flying})
	if flaggedNow(ok, "badpackets.abilities") {
		t.Fatal("flying with /fly flagged")
	}
}
