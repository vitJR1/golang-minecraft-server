package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

// BenchmarkBroadcast15 is one compressible ~300-byte packet (Set Equipment
// with NBT) to 15 compressing players.
func BenchmarkBroadcast15(b *testing.B) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	var conns []*ClientConnection
	for i := range 15 {
		c := offlineConn(inst, "P"+string(rune('a'+i)), player.Survival, 0, 64, 0)
		c.link.EnableCompression(0x03, nil, 256)
		conns = append(conns, c)
	}
	payload := make([]byte, 300)
	for i := range payload {
		payload[i] = byte(i % 23)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		inst.Players.Broadcast(0x55, payload, -1)
		for _, c := range conns {
			drain(c)
		}
	}
}
