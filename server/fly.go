package server

import (
	"fmt"

	"minecraft-server/player"
	"minecraft-server/protocol"
)

// fly.go: /fly — lets a survival or adventure player fly (double-tap jump),
// through Player Abilities (Cb 0x34: flags Byte, flying speed Float, field
// of view modifier Float; minecraft-data 1.20 "abilities").
//
// The client resets its abilities on every gamemode change and Respawn, so
// the permission ends there too (dropFlight): flight granted in the hub
// never reaches a game. While flight is allowed — and until the client has
// confirmed it was taken away — the movement checks leave the player alone.

// Player Abilities flags.
const (
	abilityInvulnerable = 0x01
	abilityFlying       = 0x02
	abilityMayFly       = 0x04
	abilityInstabuild   = 0x08
)

// Vanilla defaults carried by every Player Abilities packet.
const (
	defaultFlySpeed = 0.05
	defaultFOVMod   = 0.1
)

// flightExempt reports whether the movement checks should skip the player:
// /fly is on, or was turned off and the client hasn't confirmed it yet.
func (c *ClientConnection) flightExempt() bool {
	return c.canFly.Load() || c.flyGrace.Load()
}

// sendAbilities tells the client what it may do: its gamemode's abilities,
// plus "may fly" when /fly is on. flying forces the flying state (sent
// false when flight is taken away, so a flying player drops).
func (c *ClientConnection) sendAbilities(flying bool) error {
	var flags byte
	switch c.gamemode() {
	case player.Creative:
		flags = abilityInvulnerable | abilityMayFly | abilityInstabuild
	case player.Spectator:
		flags = abilityInvulnerable | abilityMayFly | abilityFlying
	}
	if c.canFly.Load() {
		flags |= abilityMayFly
	}
	if flying {
		flags |= abilityFlying
	}
	payload := []byte{flags}
	payload = append(payload, protocol.WriteFloat(defaultFlySpeed)...)
	payload = append(payload, protocol.WriteFloat(defaultFOVMod)...)
	return c.safeWrite(CbPlayPlayerAbil, payload)
}

// setFlight turns /fly on or off. Off keeps the movement checks away until
// the client has processed the new abilities (a Ping after them).
func (c *ClientConnection) setFlight(on bool) {
	if on {
		c.canFly.Store(true)
		c.flyGrace.Store(true)
		_ = c.sendAbilities(false)
		return
	}
	c.canFly.Store(false)
	_ = c.sendAbilities(false)
	c.endFlightGrace()
}

// dropFlight forgets /fly when the client resets its abilities by itself:
// a gamemode change, a Respawn. No packet — the client already knows.
func (c *ClientConnection) dropFlight() {
	if !c.canFly.Swap(false) {
		return
	}
	c.endFlightGrace()
}

// endFlightGrace ends the exemption once the client confirms it has seen
// the change (the Ping goes out after the packet that took flight away).
func (c *ClientConnection) endFlightGrace() {
	if c.isBot() {
		c.flyGrace.Store(c.canFly.Load()) // no client to confirm: done at once
		return
	}
	c.sendTransaction(func() {
		if !c.canFly.Load() {
			c.flyGrace.Store(false)
		}
	})
}

// onClientAbilities handles Sb Player Abilities: the client says whether it
// is flying. A vanilla client can't fly without permission — fly modules
// set the flag anyway.
func (c *ClientConnection) onClientAbilities(flags byte) {
	if flags&abilityFlying == 0 || c.flightExempt() {
		return
	}
	switch c.gamemode() {
	case player.Creative, player.Spectator:
		return
	}
	c.flag("badpackets.abilities", fmt.Sprintf("claims flight in %v without permission", c.gamemode()))
}

// cmdFly: /fly [player] — toggle flight.
func cmdFly(c *ClientConnection, args []string) {
	target := c
	if len(args) > 1 {
		_ = c.sendSystemMessage("Usage: /fly [player]")
		return
	}
	if len(args) == 1 {
		conn, _, ok := c.server.FindPlayer(args[0])
		if !ok {
			_ = c.sendSystemMessage("Player not found: " + args[0])
			return
		}
		target = conn
	}
	on := !target.canFly.Load()
	target.setFlight(on)
	state := "off"
	if on {
		state = "on"
	}
	_ = c.sendSystemMessage(fmt.Sprintf("Flight %s for %s", state, target.playerName))
	if target != c {
		_ = target.sendSystemMessage("Flight " + state)
	}
}
