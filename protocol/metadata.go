package protocol

// Entity metadata (Set Entity Metadata) indices and value types shared by
// every entity, 1.20.1 / minecraft-data 1.20.
const (
	MetaEntityFlags       = 0 // Byte: 0x01 on fire, 0x20 invisible, …
	MetaCustomName        = 2 // OptChat
	MetaCustomNameVisible = 3 // Boolean
	MetaNoGravity         = 5 // Boolean

	MetaTypeByte    = 0
	MetaTypeOptChat = 6
	MetaTypeBoolean = 8

	EntityFlagInvisible = 0x20
)
