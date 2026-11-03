// Package conn is the transport of one client connection: the socket, the
// packet framing (with compression once it's switched on), the outbound
// queue and the writer goroutine that drains it, the switch to an encrypted
// socket mid-stream, closing, and the keep-alive bookkeeping. It knows
// nothing about protocol states or players; the server's ClientConnection
// holds one.
//
// Writes never block the caller: Write builds the frame and queues it, and
// the writer goroutine batches whatever is queued into one writev. A full
// queue means a stuck client — Write reports ErrQueueFull and the server
// kicks. sendMu covers (build frame + queue) so concurrent writers, the
// compression switch and the encryption swap can't reorder frames.
package conn

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"minecraft-server/protocol"
)

// QueueSize bounds the pending-frame queue. High enough that bursty
// broadcasts (e.g. 100 players × 20Hz movement) fit without dropping, low
// enough that a stuck client is detected quickly.
const QueueSize = 256

var (
	// ErrClosed: the connection is closed.
	ErrClosed = errors.New("connection closed")
	// ErrQueueFull: the client doesn't read fast enough.
	ErrQueueFull = errors.New("send queue full")
)

// msg is what the writer goroutine reads from the queue. A frame is bytes
// ready for the socket. A swap means "flush whatever is buffered, then start
// writing to this new socket" — the encryption handshake, where the socket
// under us changes mid-stream.
type msg struct {
	frame []byte
	swap  net.Conn
}

// Conn is one client's transport.
type Conn struct {
	sendMu sync.Mutex

	// raw is the active socket (nil for a bot or a test connection). The
	// read side is touched only by the read loop (and the login handler,
	// which runs on it, so the encryption swap is safe); the writer keeps
	// its own current-socket pointer, updated through swap messages.
	raw net.Conn

	// threshold is protocol.CompressionDisabled until Set Compression went
	// out; both directions use it. Written under sendMu.
	threshold int

	out        chan msg
	closed     atomic.Bool
	done       chan struct{}
	writerDone chan struct{}

	// onWriteError is told about a failed socket write (the server kicks).
	onWriteError func(error)

	// KeepAlive is the keep-alive in flight.
	KeepAlive KeepAlive
}

// New wraps a socket. Call Serve to start writing.
func New(raw net.Conn, onWriteError func(error)) *Conn {
	return newConn(raw, QueueSize, onWriteError)
}

// NewQueued is a connection without a socket whose frames stay queued (up
// to size) until TakeFrame — for tests, which read what was sent.
func NewQueued(size int) *Conn {
	c := newConn(nil, size, nil)
	close(c.writerDone) // no writer to wait for
	return c
}

func newConn(raw net.Conn, size int, onWriteError func(error)) *Conn {
	return &Conn{
		raw:          raw,
		threshold:    protocol.CompressionDisabled,
		out:          make(chan msg, size),
		done:         make(chan struct{}),
		writerDone:   make(chan struct{}),
		onWriteError: onWriteError,
	}
}

// Serve starts the writer goroutine.
func (c *Conn) Serve() { go c.writerLoop() }

// Discard starts a writer that throws every frame away: a bot's "client",
// which never reads what the server sends.
func (c *Conn) Discard() {
	go func() {
		for range c.out {
		}
		close(c.writerDone)
	}()
}

// Write frames a packet and queues it. It returns at once; transport errors
// surface on a later call (after the writer noticed and the server closed
// the connection).
func (c *Conn) Write(packetID int32, payload []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	frame, err := protocol.BuildFrame(packetID, payload, c.threshold)
	if err != nil {
		return fmt.Errorf("build frame: %w", err)
	}
	return c.queue(msg{frame: frame})
}

// SharedFrame is one packet whose frame is built once and queued to many
// connections (an instance's cached chunk data): framing and compression
// cost once per compression threshold, not once per recipient. Frames are
// plaintext in the queue (the socket encrypts), so the bytes are shared
// read-only. Only the frame for the last threshold is kept — the payload
// is rebuilt by Build for another one: an uncompressed payload can be much
// bigger (a chunk column's light arrays: ~55 KB, framed ~0.4 KB). Safe for
// concurrent use.
type SharedFrame struct {
	ID    int32
	Build func() []byte // the packet payload

	mu        sync.Mutex
	threshold int
	frame     []byte
}

// frameFor returns the frame for threshold, building it on first use (or
// when a connection with another threshold asks).
func (f *SharedFrame) frameFor(threshold int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.frame != nil && f.threshold == threshold {
		return f.frame, nil
	}
	frame, err := protocol.BuildFrame(f.ID, f.Build(), threshold)
	if err != nil {
		return nil, err
	}
	f.frame, f.threshold = frame, threshold
	return frame, nil
}

// WriteShared queues a shared packet, like Write.
func (c *Conn) WriteShared(f *SharedFrame) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	frame, err := f.frameFor(c.threshold)
	if err != nil {
		return fmt.Errorf("build frame: %w", err)
	}
	return c.queue(msg{frame: frame})
}

// queue pushes m without blocking; sendMu held.
func (c *Conn) queue(m msg) error {
	select {
	case c.out <- m:
		return nil
	default:
		return ErrQueueFull
	}
}

// EnableCompression queues packet (Set Compression) framed with the current
// threshold — so it goes out uncompressed and the client decodes it before
// switching — then switches to threshold. Under sendMu, so a concurrent
// Write lands wholly before or wholly after the switch, and the queue is
// FIFO: wire order matches build order.
func (c *Conn) EnableCompression(packetID int32, payload []byte, threshold int) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	frame, err := protocol.BuildFrame(packetID, payload, c.threshold)
	if err != nil {
		return fmt.Errorf("build frame: %w", err)
	}
	if err := c.queue(msg{frame: frame}); err != nil {
		return err
	}
	c.threshold = threshold
	return nil
}

// Encrypt swaps the socket for wrap(socket): the read side at once (the
// caller is the read loop), the write side once every frame queued before
// it is flushed in plaintext.
func (c *Conn) Encrypt(wrap func(net.Conn) net.Conn) error {
	encrypted := wrap(c.raw)
	c.raw = encrypted
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	return c.queue(msg{swap: encrypted})
}

// Read reads the next packet, waiting at most timeout.
func (c *Conn) Read(timeout time.Duration) (*bytes.Buffer, error) {
	_ = c.raw.SetReadDeadline(time.Now().Add(timeout))
	packet, err := protocol.ReadPacket(c.raw, c.threshold)
	if err != nil {
		return nil, err
	}
	_ = c.raw.SetReadDeadline(time.Time{})
	return packet, nil
}

// ReadSplit reads the next packet, uncompressed, as id + body — the login
// handler's Encryption Response, read straight off the socket.
func (c *Conn) ReadSplit() (int, []byte, error) {
	return protocol.ReadPacketSplit(c.raw, protocol.CompressionDisabled)
}

// RemoteAddr is the client's address (nil without a socket).
func (c *Conn) RemoteAddr() net.Addr {
	if c.raw == nil {
		return nil
	}
	return c.raw.RemoteAddr()
}

// Closed reports whether Shutdown ran.
func (c *Conn) Closed() bool { return c.closed.Load() }

// Done is closed once the connection is finished (Finish).
func (c *Conn) Done() <-chan struct{} { return c.done }

// Queued is how many frames wait to be written.
func (c *Conn) Queued() int { return len(c.out) }

// TakeFrame pops one queued frame without blocking (tests: NewQueued);
// false when none is queued.
func (c *Conn) TakeFrame() ([]byte, bool) {
	for {
		select {
		case m, ok := <-c.out:
			if !ok {
				return nil, false
			}
			if m.swap != nil {
				continue
			}
			return m.frame, true
		default:
			return nil, false
		}
	}
}

// Shutdown starts closing: no more writes, and the writer gets up to drain
// to flush what's queued (a kick's Disconnect, a status Pong). It reports
// false when the connection was already closing. Finish completes it.
func (c *Conn) Shutdown(drain time.Duration) bool {
	if !c.closed.CompareAndSwap(false, true) {
		return false
	}
	// Close the queue under sendMu: a concurrent Write either sees closed
	// (taking sendMu after us) or already queued (before us); never a send
	// on a closed channel.
	c.sendMu.Lock()
	close(c.out)
	c.sendMu.Unlock()
	select {
	case <-c.writerDone:
	case <-time.After(drain):
	}
	return true
}

// Finish closes the socket and signals Done.
func (c *Conn) Finish() {
	if c.raw != nil {
		_ = c.raw.Close()
	}
	close(c.done)
}

// writerLoop owns the write side of the connection. It batches whatever is
// queued at the moment of a wake-up into one writev syscall via
// net.Buffers, then waits for the next message. This is the main reason
// for the queue: avoid one syscall per broadcast recipient.
func (c *Conn) writerLoop() {
	defer close(c.writerDone)
	current := c.raw
	var pending net.Buffers

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		_ = current.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := pending.WriteTo(current)
		_ = current.SetWriteDeadline(time.Time{})
		pending = pending[:0]
		return err
	}
	fail := func(err error) {
		if !c.closed.Load() && c.onWriteError != nil {
			c.onWriteError(err)
		}
	}
	// take handles one message; false ends the loop.
	take := func(m msg) bool {
		if m.swap == nil {
			pending = append(pending, m.frame)
			return true
		}
		if err := flush(); err != nil {
			fail(err)
			return false
		}
		current = m.swap
		return true
	}

	for {
		// Block waiting for the first message.
		m, ok := <-c.out
		if !ok {
			_ = flush()
			return
		}
		if !take(m) {
			return
		}
		// Drain whatever else is queued without blocking. Bound the batch
		// so a single writev doesn't hold the cipher state forever and we
		// give the scheduler a chance to interleave.
	drain:
		for len(pending) < QueueSize {
			select {
			case m, ok := <-c.out:
				if !ok {
					_ = flush()
					return
				}
				if !take(m) {
					return
				}
			default:
				break drain
			}
		}
		if err := flush(); err != nil {
			fail(err)
			return
		}
	}
}
