package server

import (
	"bytes"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"minecraft-server/protocol"
	"net"
	"sync"
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

type ClientConnection struct {
	// writeMu guards conn writes AND the conn swap that happens when encryption
	// is enabled. Reads run only on the readLoop goroutine, so they do not need
	// the mutex; but readLoop + keepAlive both write, and CFB8 stream cipher
	// state would desync under concurrent writes.
	writeMu    sync.Mutex
	conn       net.Conn
	state      State
	playerName string
	playerID   int32
	closed     int32 // atomic flag (use isClosed/cleanup)
	done       chan struct{}
}

func HandleConn(conn net.Conn) {
	client := &ClientConnection{
		conn:     conn,
		state:    StateHandshake,
		playerID: 1,
		done:     make(chan struct{}),
	}
	fmt.Printf("New connection from %s\n", conn.RemoteAddr())
	defer client.cleanup()

	go client.keepAlive()
	client.readLoop()
}

func (c *ClientConnection) readLoop() {
	defer c.cleanup()

	for {
		if c.isClosed() {
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		packet, err := protocol.ReadPacket(c.conn)
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
	packets := []struct {
		name string
		f    func() error
	}{
		{"Login (Play)", c.sendLoginPlay},
		{"Chunk Data", func() error { return c.sendChunkData(0, 0) }},
		{"Sync Player Position", func() error { return c.sendSyncPlayerPosition(0, 64, 0, 1) }},
	}
	for _, p := range packets {
		if c.isClosed() {
			return fmt.Errorf("server closed during %s", p.name)
		}
		fmt.Printf("Sending %s...\n", p.name)
		if err := p.f(); err != nil {
			if c.isClosed() || err.Error() == "client disconnected: write: broken pipe" {
				return fmt.Errorf("client disconnected during %s", p.name)
			}
			return fmt.Errorf("%s: %w", p.name, err)
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

	if err := protocol.WritePacket(c.conn, packetID, payload); err != nil {
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
