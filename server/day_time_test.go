package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// dayTimes returns the time-of-day field of every Update Time in pkts.
func dayTimes(t *testing.T, pkts []sentPacket) []int64 {
	t.Helper()
	var out []int64
	for _, p := range pkts {
		if p.id != CbPlayUpdateTime {
			continue
		}
		if len(p.body) != 16 {
			t.Fatalf("Update Time body is %d bytes, want 16 (two Longs)", len(p.body))
		}
		buf := bytes.NewBuffer(p.body[8:])
		v, err := protocol.ReadLong(buf)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestFrozenDayTime(t *testing.T) {
	s := New()
	inst := bareInstance(s, world.NewMemoryWorld())
	c := offlineConn(inst, "Sun", player.Survival, 0, 64, 0)
	drainPackets(t, c)

	// No fixed time: the client's clock is left alone.
	c.sendDayTime()
	if got := dayTimes(t, drainPackets(t, c)); len(got) != 0 {
		t.Fatalf("unfrozen instance sent times %v", got)
	}

	// Freezing reaches the players already there: negative = frozen noon.
	inst.FreezeTime(6000)
	if got := dayTimes(t, drainPackets(t, c)); len(got) != 1 || got[0] != -6000 {
		t.Fatalf("on freeze: %v, want [-6000]", got)
	}
	// A resync's Respawn resets the client's clock: sent again.
	if err := c.resyncView(); err != nil {
		t.Fatal(err)
	}
	if got := dayTimes(t, drainPackets(t, c)); len(got) != 1 || got[0] != -6000 {
		t.Fatalf("after resync: %v, want [-6000]", got)
	}
	// So does joining.
	other := bareInstance(s, world.NewMemoryWorld())
	other.FreezeTime(18000)
	inst.LeaveAndAnnounce(c)
	c.instance = other
	other.JoinAndAnnounce(c)
	if got := dayTimes(t, drainPackets(t, c)); len(got) != 1 || got[0] != -18000 {
		t.Fatalf("joining a midnight instance: %v, want [-18000]", got)
	}
}
