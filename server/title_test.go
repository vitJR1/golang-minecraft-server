package server

import (
	"bytes"
	"encoding/binary"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

func TestSendTitleSendsTimesSubtitleThenTitle(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Viewer", player.Survival, 0, 64, 0)
	if err := c.sendTitle("You died!", "Respawning in 5…", 5, 40, 10); err != nil {
		t.Fatal(err)
	}
	var ids []int32
	var bodies [][]byte
	for len(c.outbound) > 0 {
		msg := <-c.outbound
		buf := bytes.NewBuffer(msg.frame)
		_, _ = protocol.ReadVarInt(buf)
		id, _ := protocol.ReadVarInt(buf)
		ids = append(ids, int32(id))
		bodies = append(bodies, buf.Bytes())
	}
	want := []int32{CbPlaySetTitleAnimationTimes, CbPlaySetSubtitleText, CbPlaySetTitleText}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("packet order %v, want %v", ids, want)
	}
	if fadeIn := binary.BigEndian.Uint32(bodies[0][0:4]); fadeIn != 5 {
		t.Errorf("fade in %d, want 5", fadeIn)
	}
	if stay := binary.BigEndian.Uint32(bodies[0][4:8]); stay != 40 {
		t.Errorf("stay %d, want 40", stay)
	}
	sub, _ := protocol.ReadStringFromBuf(bytes.NewBuffer(bodies[1]))
	if sub != `{"text":"Respawning in 5…"}` {
		t.Errorf("subtitle component %q", sub)
	}
	title, _ := protocol.ReadStringFromBuf(bytes.NewBuffer(bodies[2]))
	if title != `{"text":"You died!"}` {
		t.Errorf("title component %q", title)
	}
}

func TestBroadcastTitleReachesEveryoneAndDefaultsTimes(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	a := tntConn(inst, "A", player.Survival, 0, 64, 0)
	b := tntConn(inst, "B", player.Survival, 2, 64, 0)
	inst.broadcastTitle("Trap!", "", 0, 0, 0)
	for _, c := range []*ClientConnection{a, b} {
		// Read the queue once: lastPacket drains everything it scans.
		got := map[int32][]byte{}
		for len(c.outbound) > 0 {
			buf := bytes.NewBuffer((<-c.outbound).frame)
			_, _ = protocol.ReadVarInt(buf)
			id, _ := protocol.ReadVarInt(buf)
			got[int32(id)] = buf.Bytes()
		}
		times := got[CbPlaySetTitleAnimationTimes]
		if times == nil || binary.BigEndian.Uint32(times[4:8]) != titleStay {
			t.Errorf("%s: default stay expected, got %v", c.player.Name, times)
		}
		if got[CbPlaySetTitleText] == nil {
			t.Errorf("%s: no title", c.player.Name)
		}
	}
}
