package server

import (
	"fmt"

	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/abilities"
)

// fly.go: /fly — lets a survival or adventure player fly (double-tap jump),
// through Player Abilities (server/gameplay/abilities).
//
// The client resets its abilities on every gamemode change and Respawn, so
// the permission ends there too (dropFlight): flight granted in the hub
// never reaches a game. While flight is allowed — and until the client has
// confirmed it was taken away — the movement checks leave the player alone.

// flightExempt reports whether the movement checks should skip the player:
// /fly is on, or was turned off and the client hasn't confirmed it yet.
func (c *ClientConnection) flightExempt() bool {
	return c.canFly.Load() || c.flyGrace.Load()
}

// sendAbilities tells the client what it may do: its gamemode's abilities,
// plus "may fly" when /fly is on. flying forces the flying state (sent
// false when flight is taken away, so a flying player drops).
func (c *ClientConnection) sendAbilities(flying bool) error {
	flags := abilities.Flags(c.gamemode(), c.canFly.Load(), flying)
	return c.safeWrite(packetid.CbPlayPlayerAbil, abilities.Payload(flags))
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
	if flags&abilities.Flying == 0 || c.flightExempt() {
		return
	}
	switch c.gamemode() {
	case player.Creative, player.Spectator:
		return
	}
	c.flag("badpackets.abilities", fmt.Sprintf("claims flight in %v without permission", c.gamemode()))
}
