package visibility

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
)

func TestInfoAdd(t *testing.T) {
	if InfoAddPayload(nil) != nil {
		t.Fatal("no entries, no packet")
	}
	buf := bytes.NewBuffer(InfoAddPayload([]Entry{{UUID: [16]byte{7}, Name: "Ann"}}))
	if actions, _ := buf.ReadByte(); actions != playerInfoActionAdd|playerInfoActionListed {
		t.Fatalf("actions %#x", actions)
	}
	if n, _ := protocol.ReadVarInt(buf); n != 1 {
		t.Fatal("count")
	}
	uuid := buf.Next(16)
	name, _ := protocol.ReadStringFromBuf(buf)
	props, _ := protocol.ReadVarInt(buf)
	listed, _ := buf.ReadByte()
	if uuid[0] != 7 || name != "Ann" || props != 0 || listed != 1 || buf.Len() != 0 {
		t.Fatalf("entry %v %q %d %d", uuid, name, props, listed)
	}
}

func TestEntityPayloads(t *testing.T) {
	s := player.Snapshot{EntityID: 5, X: 1, Y: 2, Z: 3, OnGround: true}
	if p := SpawnPlayerPayload(s); len(p) != 1+16+24+2 {
		t.Errorf("spawn length %d", len(p))
	}
	if p := TeleportPayload(s); len(p) != 1+24+2+1 || p[len(p)-1] != 1 {
		t.Errorf("teleport % x", p)
	}
	if p := RemoveEntitiesPayload([]int32{1, 2}); !bytes.Equal(p, []byte{2, 1, 2}) {
		t.Errorf("remove % x", p)
	}
}
