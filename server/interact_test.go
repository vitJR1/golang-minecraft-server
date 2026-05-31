package server

import (
	"bytes"
	"minecraft-server/protocol"
	"sync/atomic"
	"testing"
	"time"
)

// TestOnPlayerAttackHookFires drives two pipe clients into the same
// instance, makes attacker send SbPlayInteract action=attack targeting
// the victim's entity ID, and asserts that Instance.OnPlayerAttack ran
// with the right connections.
func TestOnPlayerAttackHookFires(t *testing.T) {
	s := New()

	attackerCli := pipeClientOn(t, s)
	completeOfflineLogin(t, attackerCli, "Attacker")
	attackerCli.startDiscardDrain()

	victimCli := pipeClientOn(t, s)
	completeOfflineLogin(t, victimCli, "Victim")
	victimCli.startDiscardDrain()

	attackerConn := findConn(t, s, "Attacker")
	victimConn := findConn(t, s, "Victim")

	var hookFired atomic.Int32
	var seenAttacker, seenVictim string
	s.Hub.OnPlayerAttack = func(a, v *ClientConnection) bool {
		seenAttacker = a.playerName
		seenVictim = v.playerName
		hookFired.Add(1)
		return true
	}

	// Build interact packet: target_eid + type(1=attack) + sneaking(bool).
	// (1.20.1 has no hand field for type=attack; sneaking byte follows.)
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, victimConn.player.EntityID)
	protocol.WriteVarInt32ToBuffer(&p, 1) // attack
	p.WriteByte(0)                        // not sneaking
	attackerCli.write(t, SbPlayInteract, p.Bytes())

	waitFor(t, time.Second, func() bool { return hookFired.Load() == 1 },
		"OnPlayerAttack to fire")

	if seenAttacker != "Attacker" {
		t.Errorf("attacker: got %q, want Attacker", seenAttacker)
	}
	if seenVictim != "Victim" {
		t.Errorf("victim: got %q, want Victim", seenVictim)
	}
	_ = attackerConn // referenced for symmetry
}

// TestOnPlayerAttackSelfAttackIgnored: the handler skips self-targeted
// attacks (attackers shouldn't be able to credit themselves).
func TestOnPlayerAttackSelfAttackIgnored(t *testing.T) {
	s := New()

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Loner")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Loner")

	var hookFired atomic.Int32
	s.Hub.OnPlayerAttack = func(*ClientConnection, *ClientConnection) bool {
		hookFired.Add(1)
		return true
	}

	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, conn.player.EntityID) // target self
	protocol.WriteVarInt32ToBuffer(&p, 1)
	p.WriteByte(0)
	cli.write(t, SbPlayInteract, p.Bytes())

	// Give it a moment — nothing should fire.
	time.Sleep(100 * time.Millisecond)
	if got := hookFired.Load(); got != 0 {
		t.Errorf("hook fired %d times on self-attack, want 0", got)
	}
}
