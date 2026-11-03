package server

import "sync"

// join_stream.go keeps a joining player's world in step while MovePlayer
// streams it. The chunks are a snapshot of the world; a block that changes
// after the snapshot but before the player is in Players (whose broadcasts
// carry block updates) would otherwise stay stale on that client for good.
// So between beginStream and endStream every block broadcast is also
// buffered for the joiner, and endStream — called once the player is in
// Players — replays the buffer in order. Replays may repeat an update the
// player already got directly; block updates are absolute, so the last one
// wins either way.

type blockStreams struct {
	mu sync.Mutex // also orders broadcasts against endStream's replay
	by map[*ClientConnection][]streamedPacket
}

type streamedPacket struct {
	id      int32
	payload []byte
}

// beginStream starts buffering block broadcasts for c.
func (i *Instance) beginStream(c *ClientConnection) {
	i.streams.mu.Lock()
	defer i.streams.mu.Unlock()
	if i.streams.by == nil {
		i.streams.by = map[*ClientConnection][]streamedPacket{}
	}
	i.streams.by[c] = nil
}

// endStream replays what was buffered for c and stops buffering. c must
// already be in Players, so nothing broadcast afterwards is missed.
func (i *Instance) endStream(c *ClientConnection) {
	i.streams.mu.Lock()
	defer i.streams.mu.Unlock()
	for _, p := range i.streams.by[c] {
		_ = c.safeWrite(p.id, p.payload)
	}
	delete(i.streams.by, c)
}

// dropStream stops buffering for c without replaying (the join failed).
func (i *Instance) dropStream(c *ClientConnection) {
	i.streams.mu.Lock()
	defer i.streams.mu.Unlock()
	delete(i.streams.by, c)
}

// broadcastBlocks sends a block update (Block Update / Update Section
// Blocks) to everyone in the instance and buffers it for joiners mid-stream.
func (i *Instance) broadcastBlocks(id int32, payload []byte) {
	i.streams.mu.Lock()
	defer i.streams.mu.Unlock()
	i.Players.Broadcast(id, payload, -1)
	for c, buf := range i.streams.by {
		i.streams.by[c] = append(buf, streamedPacket{id, payload})
	}
}
