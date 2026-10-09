package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

func TestTNTAutoPrimeOnPlacement(t *testing.T) {
	s := New()
	s.Hub.World.SetBlock(world.Position{X: 0, Y: 63, Z: 0}, world.Stone)
	s.Hub.SetTNTAutoPrime(true)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Demo")
	cli.startDiscardDrain()
	c := findConn(t, s, "Demo")
	c.player.SetGamemode(player.Survival)
	c.player.MoveTo(3.5, 64, 0.5, true)
	hold(c, "minecraft:tnt")

	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WritePosition(0, 63, 0))
	protocol.WriteVarInt32ToBuffer(&p, 1) // top face → (0,64,0)
	p.Write(protocol.WriteFloat(0.5))
	p.Write(protocol.WriteFloat(0.5))
	p.Write(protocol.WriteFloat(0.5))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 1)
	cli.write(t, SbPlayUseItemOnBlock, p.Bytes())

	waitFor(t, time.Second, func() bool {
		s.Hub.tntMu.Lock()
		defer s.Hub.tntMu.Unlock()
		return len(s.Hub.tnts) == 1
	}, "placed TNT to be primed")
	if got := s.Hub.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got == world.TNT {
		t.Error("primed TNT should have left the block grid")
	}
}
