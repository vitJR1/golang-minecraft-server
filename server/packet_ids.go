package server

// Packet IDs for Minecraft Java protocol 763 (1.20.1).
// Reference: https://wiki.vg/index.php?title=Protocol&oldid=18375
//
// Many IDs collide at 0x00 across states — dispatch is keyed by state, so
// duplicates here are intentional. Don't merge them.

// Serverbound, state = handshake.
const (
	SbHandshake = 0x00 // Handshake intent
)

// Serverbound, state = status.
const (
	SbStatusRequest = 0x00
	SbStatusPing    = 0x01
)

// Clientbound, state = status.
const (
	CbStatusResponse = 0x00
	CbStatusPong     = 0x01
)

// Serverbound, state = login.
const (
	SbLoginStart       = 0x00
	SbLoginEncResponse = 0x01
)

// Clientbound, state = login.
const (
	CbLoginDisconnect = 0x00
	CbLoginEncRequest = 0x01
	CbLoginSuccess    = 0x02
	CbLoginSetCompr   = 0x03
)

// Serverbound, state = play.
const (
	SbPlayTeleportConfirm = 0x00
	SbPlayChatCommand     = 0x04
	SbPlayChatMessage     = 0x05
	SbPlayClientInfo      = 0x07
	SbPlayKeepAlive       = 0x12
	SbPlaySetPos          = 0x14
	SbPlaySetPosRot       = 0x15
	SbPlaySetRot          = 0x16
)

// Clientbound, state = play.
const (
	CbPlayChunkData   = 0x24
	CbPlayKeepAlive   = 0x23
	CbPlayLogin       = 0x28
	CbPlaySystemChat  = 0x64
	CbPlayPlayerAbil  = 0x36
	CbPlaySyncPos     = 0x3C
	CbPlaySpawnPos    = 0x50
	CbPlayUpdateLight = 0x29
)
