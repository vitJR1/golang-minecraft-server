package server

import (
	"bytes"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
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

// Server holds process-wide shared state — the world, the entity-ID
// allocator, the player list. One Server instance handles all incoming
// connections via HandleConn.
type Server struct {
	World        world.World
	Players      *PlayerList
	nextEntityID atomic.Int32
}

// New constructs a Server with an empty in-memory world and no players.
func New() *Server {
	return &Server{
		World:   world.NewMemoryWorld(),
		Players: NewPlayerList(),
	}
}

// HandleConn drives a single client connection through its state machine.
// Call in a goroutine per accepted net.Conn.
func (s *Server) HandleConn(conn net.Conn) {
	client := &ClientConnection{
		server:               s,
		conn:                 conn,
		state:                StateHandshake,
		compressionThreshold: protocol.CompressionDisabled,
		done:                 make(chan struct{}),
	}
	fmt.Printf("New connection from %s\n", conn.RemoteAddr())
	defer client.cleanup()

	go client.keepAlive()
	client.readLoop()
}

type ClientConnection struct {
	// writeMu guards conn writes AND the conn swap that happens when encryption
	// is enabled. Reads run only on the readLoop goroutine, so they do not need
	// the mutex; but readLoop + keepAlive both write, and CFB8 stream cipher
	// state would desync under concurrent writes.
	writeMu sync.Mutex
	server  *Server
	conn    net.Conn
	state   State
	// compressionThreshold is protocol.CompressionDisabled until the server
	// sends Set Compression. After that, both reads and writes use the
	// compressed framing. Set once during login (single goroutine) before any
	// other write occurs, so plain field access is safe.
	compressionThreshold int

	// playerName is captured from Login Start so logs and ban-check have a
	// name to print before the full Player exists.
	playerName string

	// player is nil until login completes successfully. Reach for it only
	// after the connection has transitioned to StatePlay.
	player *player.Player

	closed int32 // atomic flag (use isClosed/cleanup)
	done   chan struct{}
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
	p := c.player
	packets := []struct {
		name string
		f    func() error
	}{
		{"Login (Play)", c.sendLoginPlay},
		{"Chunk Data", func() error { return c.sendChunkData(0, 0) }},
		{"Sync Player Position", func() error {
			return c.sendSyncPlayerPosition(p.X, p.Y, p.Z, 1)
		}},
	}
	for _, pkt := range packets {
		if c.isClosed() {
			return fmt.Errorf("server closed during %s", pkt.name)
		}
		fmt.Printf("Sending %s...\n", pkt.name)
		if err := pkt.f(); err != nil {
			if c.isClosed() || err.Error() == "client disconnected: write: broken pipe" {
				return fmt.Errorf("client disconnected during %s", pkt.name)
			}
			return fmt.Errorf("%s: %w", pkt.name, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println("All play packets sent successfully")
	return nil
}

func (c *ClientConnection) safeWrite(packetID int32, payload []byte) error {
	if c.isClosed() {
		return fmt.Errorf("server already closed")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer c.conn.SetWriteDeadline(time.Time{})

	if err := protocol.WritePacket(c.conn, packetID, payload, c.compressionThreshold); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Errorf("write timeout: %w", err)
		}
		var op *net.OpError
		if errors.As(err, &op) {
			c.cleanup()
			return fmt.Errorf("client disconnected: %w", err)
		}
		c.cleanup()
		return fmt.Errorf("write error: %w", err)
	}
	return nil
}
