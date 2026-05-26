// Package player models a connected player's gameplay state — identity,
// position, gamemode, eventually inventory. Wire-level concerns (the TCP
// connection, encryption, compression, packet framing) stay on
// server.ClientConnection and are deliberately not referenced here.
package player

// Gamemode mirrors vanilla's 1-byte gamemode field.
type Gamemode byte

const (
	Survival  Gamemode = 0
	Creative  Gamemode = 1
	Adventure Gamemode = 2
	Spectator Gamemode = 3
)

// Player is the per-connection gameplay entity.
type Player struct {
	EntityID int32
	Name     string
	UUID     [16]byte // binary form, ready for the wire

	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool

	Gamemode Gamemode
}

// New constructs a Player at the default spawn (0, 80, 0) in Creative mode.
// Spawn point and gamemode will become server-configurable later; for now
// they are sensible defaults for an empty-world test bed.
func New(entityID int32, name string, uuid [16]byte) *Player {
	return &Player{
		EntityID: entityID,
		Name:     name,
		UUID:     uuid,
		X:        0,
		Y:        80,
		Z:        0,
		Gamemode: Creative,
	}
}
