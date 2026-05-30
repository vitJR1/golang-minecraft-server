package server

import (
	"bytes"
	"crypto/rsa"
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

// Server holds process-wide shared state across all instances: the op
// allocator, the op set, the entity-ID counter, and the hub instance.
// World and PlayerList live on Instance now — per-game scoping is the
// whole point of the mini-game architecture.
type Server struct {
	Hub          *Instance
	Ops          *OpSet
	nextEntityID atomic.Int32

	// instances is the registry of all live instances (including Hub).
	// Used by FindPlayer for cross-instance lookups. Add/remove via
	// addInstance/removeInstance (TODO: once we wire matchmaking).
	mu        sync.RWMutex
	instances map[string]*Instance
}

// New constructs a Server with an empty Hub instance and an op set seeded
// from cfg.InitialOps.
func New() *Server {
	s := &Server{
		Ops:       NewOpSet(cfg.InitialOps),
		instances: make(map[string]*Instance),
	}
	s.Hub = NewInstance("hub", s, world.NewMemoryWorld())
	s.instances[s.Hub.ID] = s.Hub
	return s
}

// FindPlayer looks up a logged-in player by name across every instance.
// Returns the connection and the instance it currently lives in.
func (s *Server) FindPlayer(name string) (*ClientConnection, *Instance, bool) {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()
	for _, i := range instances {
		if c, ok := i.Players.ByName(name); ok {
			return c, i, true
		}
	}
	return nil, nil, false
}

// PlayerCount sums Players across every instance. Cheap enough for /list
// and tests; not a hot path.
func (s *Server) PlayerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, i := range s.instances {
		total += i.Players.Count()
	}
	return total
}

// sendSystemMessage delivers a SystemChat line to a single player. Used by
// command responses and other server → one-player notifications.
func (c *ClientConnection) sendSystemMessage(text string) error {
	return c.safeWrite(CbPlaySystemChat, buildSystemChatPayload(text))
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

	// instance is the world/room this player currently lives in. Set when
	// login completes (defaults to Hub) and never changes until we add
	// cross-instance teleport. Single writer (handler_login), readers are
	// the broadcast paths and command handlers.
	instance *Instance

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
