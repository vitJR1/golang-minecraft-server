// Package abilities is the Player Abilities packet (Cb 0x34: flags Byte,
// flying speed Float, field of view modifier Float; minecraft-data 1.20
// "abilities"): what a client may do — fly, build instantly, take no damage —
// by gamemode, plus the server's own "may fly" grant (/fly).
package abilities

import (
	"minecraft-server/player"
	"minecraft-server/protocol"
)

// Player Abilities flags.
const (
	Invulnerable = 0x01
	Flying       = 0x02
	MayFly       = 0x04
	Instabuild   = 0x08
)

// Vanilla defaults carried by every Player Abilities packet.
const (
	DefaultFlySpeed = 0.05
	DefaultFOVMod   = 0.1
)

// Flags are a player's abilities: its gamemode's, plus MayFly when the
// server lets it fly, plus Flying when it should be flying now.
func Flags(mode player.Gamemode, mayFly, flying bool) byte {
	var flags byte
	switch mode {
	case player.Creative:
		flags = Invulnerable | MayFly | Instabuild
	case player.Spectator:
		flags = Invulnerable | MayFly | Flying
	}
	if mayFly {
		flags |= MayFly
	}
	if flying {
		flags |= Flying
	}
	return flags
}

// Payload builds Player Abilities with the vanilla speeds.
func Payload(flags byte) []byte {
	payload := []byte{flags}
	payload = append(payload, protocol.WriteFloat(DefaultFlySpeed)...)
	return append(payload, protocol.WriteFloat(DefaultFOVMod)...)
}
