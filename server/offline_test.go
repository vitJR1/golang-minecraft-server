package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// Offline-connection helpers: a ClientConnection parked in an instance with
// no readLoop goroutine, so a test can set inventory / position / gamemode
// and feed packets to handlePlay on its own goroutine without racing the
// server (go test -race stays clean). Pipe clients (pipeClientOn) remain
// the tool for login-flow and packet-sequence tests.

// offlineConn is tntConn plus the player name the command/ops code reads.
func offlineConn(inst *Instance, name string, mode player.Gamemode, x, y, z float64) *ClientConnection {
	c := tntConn(inst, name, mode, x, y, z)
	c.playerName = name
	return c
}

// offlineHub parks a named survival player in the server's hub instance and
// runs the hub's join hooks (navigator hand-out etc.).
func offlineHub(s *Server, name string) *ClientConnection {
	c := offlineConn(s.Hub, name, player.Survival, 0.5, 67, 0.5)
	s.Hub.JoinAndAnnounce(c)
	drain(c)
	return c
}

// play feeds one serverbound packet body straight to the play handler and
// drains what it sent back (an unread queue would eventually kick the
// connection).
func play(t *testing.T, c *ClientConnection, id int32, payload []byte) {
	t.Helper()
	if err := c.handlePlay(bytes.NewBuffer(payload), int(id)); err != nil {
		t.Fatalf("handlePlay 0x%02X: %v", id, err)
	}
	drain(c)
}

// drain throws away everything queued for the client.
func drain(c *ClientConnection) {
	for len(c.outbound) > 0 {
		<-c.outbound
	}
}

// digAt sends Player Action action at pos.
func digAt(t *testing.T, c *ClientConnection, pos world.Position, action int32) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, action)
	p.Write(protocol.WritePosition(pos.X, pos.Y, pos.Z))
	p.WriteByte(1)
	protocol.WriteVarInt32ToBuffer(&p, 1)
	play(t, c, SbPlayPlayerAction, p.Bytes())
}

// placeOn sends Use Item On Block on the given face of clicked.
func placeOn(t *testing.T, c *ClientConnection, clicked world.Position, face int32) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0) // hand
	p.Write(protocol.WritePosition(clicked.X, clicked.Y, clicked.Z))
	protocol.WriteVarInt32ToBuffer(&p, face)
	p.Write(protocol.WriteFloat(0.5))
	p.Write(protocol.WriteFloat(0.5))
	p.Write(protocol.WriteFloat(0.5))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 1)
	play(t, c, SbPlayUseItemOnBlock, p.Bytes())
}

// clickSlot sends a window-0 Click Container with the given changed slots
// and resulting cursor.
func clickSlot(t *testing.T, c *ClientConnection, slot int16, mode int32, changed map[int16]itemStack, cursor itemStack) {
	t.Helper()
	var p bytes.Buffer
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteShort(slot))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, mode)
	protocol.WriteVarInt32ToBuffer(&p, int32(len(changed)))
	for s, st := range changed {
		p.Write(protocol.WriteShort(s))
		writeStack(&p, st)
	}
	writeStack(&p, cursor)
	play(t, c, SbPlayClickContainer, p.Bytes())
}

// hold puts one item in hotbar slot 0 and selects it.
func hold(c *ClientConnection, item string) {
	id, _ := world.ItemByName(item)
	c.inv.set(hotbarStart, itemStack{ID: id, Count: 1})
	c.heldSlot.Store(0)
}
