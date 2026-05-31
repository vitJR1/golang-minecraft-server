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
	SbPlayTeleportConfirm   = 0x00
	SbPlayChatCommand       = 0x04
	SbPlayChatMessage       = 0x05
	SbPlayClientInfo        = 0x08 // "settings": locale, view distance, etc.
	SbPlayCommandSuggestReq = 0x09 // "tab_complete": fires per keystroke after "/"
	SbPlayPluginMessage     = 0x0D
	SbPlayInteract          = 0x10
	SbPlayKeepAlive         = 0x12
	SbPlaySetPos            = 0x14
	SbPlaySetPosRot         = 0x15
	SbPlaySetRot            = 0x16
	SbPlayPlayerAbilities   = 0x1C // flags byte: flying / invulnerable / etc.
	SbPlayPlayerAction      = 0x1D // NOT 0x1C — that's Player Abilities in 1.20.1
	SbPlayPlayerCommand     = 0x1E // sneak / sprint / jump_boost
	SbPlaySwingArm          = 0x2F
	SbPlayUseItemOnBlock    = 0x31
	SbPlayUseItem           = 0x32 // right-click in air (e.g. holding blaze rod)
	SbPlaySetHeldItem       = 0x28 // Int16 hotbar slot the player switched to

	// Inventory / container interaction. IDs verified against
	// minecraft-data protocol.json for 1.20 (which 1.20.1 inherits).
	SbPlayClickContainer = 0x0B
	SbPlayCloseContainer = 0x0C
)

// Clientbound, state = play.
const (
	CbPlaySpawnPlayer        = 0x03
	CbPlayEntityAnimation    = 0x04
	CbPlayAckBlockChange     = 0x06
	CbPlayBlockUpdate        = 0x0A
	CbPlayCommandSuggestResp = 0x0F // "tab_complete" response
	CbPlayDeclareCommands    = 0x10 // brigadier tree — what /<TAB> shows
	CbPlayGameEvent          = 0x1F // NOT 0x20 (= Open Horse Screen for 1.20.1)
	CbPlayKeepAlive          = 0x23
	CbPlayChunkData          = 0x24
	CbPlayLogin              = 0x28
	CbPlayPlayerAbil         = 0x34 // best guess; not actively used yet
	// NOTE 0x35 = Player Chat Message in 1.20.1, NOT Player Info Remove.
	// Sending PI-Remove bytes to 0x35 crashes the client (Player Chat Message
	// expects UUID + Index + sig fields).
	CbPlayPlayerInfoRemove = 0x39
	CbPlayPlayerInfoUpdate = 0x3A
	CbPlaySyncPos          = 0x3C
	CbPlayRemoveEntities   = 0x3E // NOT 0x3B (= Look At for 1.20.1)
	CbPlayRespawn          = 0x41
	CbPlayHeadRotation     = 0x42 // body yaw rides Teleport Entity; head needs its own packet
	CbPlayDisconnect       = 0x1A // server-initiated kick with reason
	CbPlaySetCenterChunk   = 0x4E // "update_view_position" — center chunk for the client
	CbPlaySpawnPos         = 0x50
	CbPlaySystemChat       = 0x64
	CbPlayTeleportEntity   = 0x68
	CbPlaySetExperience    = 0x56 // float bar + VarInt level + VarInt total xp

	// Inventory / container packets. IDs from minecraft-data 1.20
	// protocol.json (window_items / set_slot / open_window).
	CbPlaySetContainerContent = 0x12 // window_items
	CbPlaySetContainerSlot    = 0x14 // set_slot
	CbPlayOpenScreen          = 0x30 // open_window
)
