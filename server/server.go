package server

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"minecraft-server/cfg"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	private   *rsa.PrivateKey
	publicKey []byte
)

func init() {
	pub, priv, err := NewEncryptionRequest()
	if err != nil {
		panic(fmt.Errorf("init encryption: %w", err))
	}
	publicKey = pub
	private = priv
}

// outboundQueueSize bounds the per-connection pending-frame channel. Set
// high enough that bursty broadcasts (e.g. 100 players × 20Hz movement) fit
// without dropping, low enough that a stuck client is detected quickly.
const outboundQueueSize = 256

// outboundMsg is what the writer goroutine reads from the channel. A frame
// is bytes ready for conn.Write. A swap message means "flush whatever is
// buffered, then start writing to this new conn" — used for the encryption
// handshake, where the conn under us changes mid-stream.
type outboundMsg struct {
	frame    []byte
	swapConn net.Conn
}

// Server holds process-wide shared state — the world, the entity-ID
// allocator, the player list, the op set. One Server instance handles all
// incoming connections via HandleConn.
type Server struct {
	World        world.World
	Players      *PlayerList
	Ops          *OpSet
	nextEntityID atomic.Int32
	// joinMu serializes registration + visibility announcements so the join
	// of one player can't observe another player who's mid-join. See
	// joinAndAnnounce / leaveAndAnnounce.
	joinMu sync.Mutex
}

// New constructs a Server with an empty in-memory world, no players, and
// an op set seeded from cfg.InitialOps.
func New() *Server {
	return &Server{
		World:   world.NewMemoryWorld(),
		Players: NewPlayerList(),
		Ops:     NewOpSet(cfg.InitialOps),
	}
}

// SetBlock updates the world and broadcasts a Block Update packet to every
// connected player. The change is visible immediately to clients already in
// game; new joiners pick it up via sendCurrentWorldState on login.
func (s *Server) SetBlock(p world.Position, b world.Block) {
	s.World.SetBlock(p, b)

	var buf bytes.Buffer
	buf.Write(protocol.WritePosition(p.X, p.Y, p.Z))
	protocol.WriteVarInt32ToBuffer(&buf, b.StateID)
	s.Players.Broadcast(CbPlayBlockUpdate, buf.Bytes(), -1)
}

// BroadcastChat sends an in-game chat line to every connected player.
// Format is the standard "<sender> message". An empty sender renders as a
// server announcement (no angle brackets).
func (s *Server) BroadcastChat(sender, message string) {
	var line string
	if sender == "" {
		line = message
	} else {
		line = fmt.Sprintf("<%s> %s", sender, message)
	}
	payload := buildSystemChatPayload(line)
	s.Players.Broadcast(CbPlaySystemChat, payload, -1)
}

// SendSystemMessage delivers a SystemChat line to a single player. Used by
// command responses and other server → one-player notifications.
func (c *ClientConnection) sendSystemMessage(text string) error {
	return c.safeWrite(CbPlaySystemChat, buildSystemChatPayload(text))
}

// buildSystemChatPayload assembles the System Chat (Cb 0x64) body: a JSON
// chat component string followed by the "overlay" boolean (false = chat
// area, true = action bar).
func buildSystemChatPayload(text string) []byte {
	encoded, _ := json.Marshal(map[string]string{"text": text})
	return append(protocol.WriteString(string(encoded)), 0)
}

// HandleConn drives a single client connection through its state machine.
// Call in a goroutine per accepted net.Conn.
func (s *Server) HandleConn(conn net.Conn) {
	client := &ClientConnection{
		server:               s,
		conn:                 conn,
		state:                StateHandshake,
		compressionThreshold: protocol.CompressionDisabled,
		outbound:             make(chan outboundMsg, outboundQueueSize),
		done:                 make(chan struct{}),
		writerDone:           make(chan struct{}),
	}
	fmt.Printf("New connection from %s\n", conn.RemoteAddr())
	defer client.cleanup()

	go client.writerLoop()
	go client.keepAlive()
	client.readLoop()
}

type ClientConnection struct {
	// sendMu is held briefly during (build frame + push to outbound) so
	// concurrent safeWrite calls can't interleave with each other or with
	// enableCompression's threshold flip, which would reorder frames on the
	// wire. The mutex is short-lived: no syscalls, just memory work.
	sendMu sync.Mutex

	server *Server

	// conn is the active socket. The read side is touched only by readLoop
	// (and by handler_login which runs in the readLoop goroutine, so swaps
	// during encryption-enable are safe). The write side is owned by
	// writerLoop, which maintains its own current-conn pointer updated via
	// outboundMsg.swapConn — writes never read this field directly.
	conn net.Conn

	state State

	// compressionThreshold is protocol.CompressionDisabled until the server
	// sends Set Compression. After that, both reads and writes use the
	// compressed framing. Guarded by sendMu when writers touch it.
	compressionThreshold int

	// outbound is the pending-write queue. Producers (safeWrite, broadcast)
	// push frames; writerLoop drains and writes them. Closed by cleanup.
	outbound chan outboundMsg

	// playerName is captured from Login Start so logs and ban-check have a
	// name to print before the full Player exists.
	playerName string

	// player is nil until login completes successfully. Reach for it only
	// after the connection has transitioned to StatePlay.
	player *player.Player

	closed int32 // atomic flag (use isClosed/cleanup)
	done   chan struct{}

	// writerDone is closed by writerLoop on exit. cleanup waits on it
	// (with a timeout) so any in-flight frames — particularly the Pong that
	// status handlers fire right before cleanup — actually make it on the
	// wire before conn.Close cuts the pipe.
	writerDone chan struct{}
}

func (c *ClientConnection) readLoop() {
	defer c.cleanup()

	for {
		if c.isClosed() {
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		packet, err := protocol.ReadPacket(c.conn, c.compressionThreshold)
		if err != nil {
			c.handleReadError(err)
			return
		}
		c.conn.SetReadDeadline(time.Time{})

		if err := c.processPacket(packet); err != nil {
			fmt.Printf("Error processing packet: %v\n", err)
			// Play-state errors aren't necessarily fatal (one malformed packet
			// shouldn't kick the player). For pre-play states, bail.
			if c.state != StatePlay {
				return
			}
		}
	}
}

// writerLoop owns the write side of the connection. It batches whatever is
// queued at the moment of a wake-up into one writev syscall via
// net.Buffers, then waits for the next message. This is the main reason
// for the queue: avoid one syscall per broadcast recipient.
func (c *ClientConnection) writerLoop() {
	defer close(c.writerDone)
	currentConn := c.conn
	var pending net.Buffers

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		_ = currentConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := pending.WriteTo(currentConn)
		_ = currentConn.SetWriteDeadline(time.Time{})
		pending = pending[:0]
		return err
	}

	for {
		// Block waiting for the first message.
		msg, ok := <-c.outbound
		if !ok {
			_ = flush()
			return
		}
		if msg.swapConn != nil {
			if err := flush(); err != nil {
				c.handleWriteError(err)
				return
			}
			currentConn = msg.swapConn
		} else {
			pending = append(pending, msg.frame)
		}

		// Drain whatever else is queued without blocking. Bound the batch
		// so a single writev doesn't hold the cipher state forever and we
		// give the scheduler a chance to interleave.
	drain:
		for len(pending) < outboundQueueSize {
			select {
			case msg, ok := <-c.outbound:
				if !ok {
					_ = flush()
					return
				}
				if msg.swapConn != nil {
					if err := flush(); err != nil {
						c.handleWriteError(err)
						return
					}
					currentConn = msg.swapConn
				} else {
					pending = append(pending, msg.frame)
				}
			default:
				break drain
			}
		}

		if err := flush(); err != nil {
			c.handleWriteError(err)
			return
		}
	}
}

func (c *ClientConnection) handleWriteError(err error) {
	if c.isClosed() {
		return
	}
	fmt.Printf("Client %s write error: %v\n", c.playerName, err)
	go c.cleanup()
}

func (c *ClientConnection) handleReadError(err error) {
	if c.isClosed() {
		return
	}
	switch {
	case err == io.EOF:
		fmt.Printf("Client %s gracefully disconnected\n", c.playerName)
	default:
		if opErr, ok := err.(*net.OpError); ok {
			if opErr.Err.Error() == "use of closed network connection" {
				return
			}
		}
		fmt.Printf("Client %s read error: %v\n", c.playerName, err)
	}
	c.cleanup()
}

func (c *ClientConnection) processPacket(packet *bytes.Buffer) error {
	packetID, err := protocol.ReadVarInt(packet)
	if err != nil {
		return fmt.Errorf("reading packet ID: %w", err)
	}

	switch c.state {
	case StateHandshake:
		return c.handleHandshake(packet, packetID)
	case StateStatus:
		return c.handleStatus(packet, packetID)
	case StateLogin:
		return c.handleLogin(packet, packetID)
	case StatePlay:
		return c.handlePlay(packet, packetID)
	default:
		return fmt.Errorf("unknown state: %s", c.state)
	}
}

// sendPlayPackets dispatches the post-LoginSuccess sequence that the client
// requires before it will render the world.
func (c *ClientConnection) sendPlayPackets() error {
	spawn := c.player.Snapshot()
	packets := []struct {
		name string
		f    func() error
	}{
		{"Login (Play)", c.sendLoginPlay},
		{"Chunk Data", func() error { return c.sendChunkData(0, 0) }},
		{"Sync Player Position", func() error {
			return c.sendSyncPlayerPosition(spawn.X, spawn.Y, spawn.Z, 1)
		}},
		{"World State", c.sendCurrentWorldState},
	}
	for _, pkt := range packets {
		if c.isClosed() {
			return fmt.Errorf("server closed during %s", pkt.name)
		}
		if err := pkt.f(); err != nil {
			if c.isClosed() {
				return fmt.Errorf("client disconnected during %s", pkt.name)
			}
			return fmt.Errorf("%s: %w", pkt.name, err)
		}
	}
	return nil
}

// safeWrite queues a packet for the writer goroutine. Returns immediately;
// transport errors surface on the next call (after the writer notices and
// closes the connection). If the queue is full, kicks the client — slow
// consumers must not block the broadcaster.
func (c *ClientConnection) safeWrite(packetID int32, payload []byte) error {
	c.sendMu.Lock()
	if c.isClosed() {
		c.sendMu.Unlock()
		return fmt.Errorf("connection closed")
	}
	frame, err := protocol.BuildFrame(packetID, payload, c.compressionThreshold)
	if err != nil {
		c.sendMu.Unlock()
		return fmt.Errorf("build frame: %w", err)
	}
	select {
	case c.outbound <- outboundMsg{frame: frame}:
		c.sendMu.Unlock()
		return nil
	default:
		c.sendMu.Unlock()
		go c.cleanup() // queue full — kick
		return fmt.Errorf("send queue full")
	}
}
