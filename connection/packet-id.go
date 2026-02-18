package connection

const (
	Check                = 0x00
	Status               = 0x00
	TeleportConfirm      = 0x00
	Ping                 = 0x01
	LoginStart           = 0x00
	KeepAlive            = 0x15
	ChatMessage          = 0x1A
	PlayerPosition       = 0x1B
	SendJoinGamePosition = 0x28
	SendPlayerPosition   = 0x38
)
