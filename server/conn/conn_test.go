package conn

import (
	"bytes"
	"net"
	"testing"
	"time"

	"minecraft-server/protocol"
)

// TestKeepAliveStaleAckIgnored: an ack with the wrong id must NOT clear the
// pending state — otherwise a misbehaving client could keep itself alive
// forever by spamming bogus ids.
func TestKeepAliveStaleAckIgnored(t *testing.T) {
	var k KeepAlive
	id, timedOut, _ := k.Next(time.Now())
	if id == 0 || timedOut || k.Pending() != id {
		t.Fatalf("first keep-alive: id %d timedOut %v", id, timedOut)
	}
	if again, _, _ := k.Next(time.Now()); again != 0 {
		t.Fatal("no new keep-alive while one is outstanding")
	}
	if k.Answer(id + 1) {
		t.Error("stale ack accepted")
	}
	if !k.Answer(id) || k.Pending() != 0 {
		t.Error("matching ack did not clear")
	}
	SetKeepAliveTimeout(time.Millisecond)
	defer SetKeepAliveTimeout(30 * time.Second)
	k.Next(time.Now())
	if _, timedOut, _ := k.Next(time.Now().Add(time.Second)); !timedOut {
		t.Error("an unanswered keep-alive times out")
	}
}

// TestWriteReadAndCompression: frames written before the switch go out
// plain, after it compressed, in order; a closed connection takes no more.
func TestWriteReadAndCompression(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := New(a, nil)
	c.Serve()
	big := bytes.Repeat([]byte{7}, 600)
	if err := c.Write(1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableCompression(3, protocol.WriteVarInt32(256), 256); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(2, big); err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		threshold int
		id        int32
	}{{protocol.CompressionDisabled, 1}, {protocol.CompressionDisabled, 3}, {256, 2}} {
		buf, err := protocol.ReadPacket(b, want.threshold)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if id, _ := protocol.ReadVarInt(buf); int32(id) != want.id {
			t.Fatalf("frame %d: id %d, want %d", i, id, want.id)
		}
	}
	if !c.Shutdown(time.Second) || c.Shutdown(time.Second) {
		t.Fatal("Shutdown runs once")
	}
	c.Finish()
	if err := c.Write(1, nil); err != ErrClosed {
		t.Fatalf("write after close: %v", err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done not closed")
	}
}

func TestQueueFull(t *testing.T) {
	c := NewQueued(2)
	_ = c.Write(1, nil)
	_ = c.Write(1, nil)
	if err := c.Write(1, nil); err != ErrQueueFull {
		t.Fatalf("third write: %v", err)
	}
	if c.Queued() != 2 {
		t.Fatalf("queued %d", c.Queued())
	}
	if _, ok := c.TakeFrame(); !ok || c.Queued() != 1 {
		t.Fatal("TakeFrame")
	}
}
