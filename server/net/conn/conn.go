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
	"bufio"
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
	// rd buffers the read side over raw (nil without a socket): a packet
	// read used to cost a syscall per length byte plus one for the body.
	rd *bufio.Reader

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
	var rd *bufio.Reader
	if raw != nil {
		rd = bufio.NewReader(raw)
	}
	return &Conn{
		raw:          raw,
		rd:           rd,
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
// read-only. The frames are kept per threshold (players compress, bots
// don't), not the payload, which Build makes on demand: an uncompressed
// payload can be much bigger (a chunk column's light arrays: ~55 KB, framed
// ~0.4 KB). Safe for concurrent use.
type SharedFrame struct {
	ID    int32
	Build func() []byte // the packet payload

	mu     sync.Mutex
	frames []thresholdFrame // one per threshold seen, usually one or two
}

type thresholdFrame struct {
	threshold int
	frame     []byte
}

// NewSharedFrame shares an already built payload (a broadcast).
func NewSharedFrame(id int32, payload []byte) *SharedFrame {
	return &SharedFrame{ID: id, Build: func() []byte { return payload }}
}

// frameFor returns the frame for threshold, building it on first use.
func (f *SharedFrame) frameFor(threshold int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, tf := range f.frames {
		if tf.threshold == threshold {
			return tf.frame, nil
		}
	}
	frame, err := protocol.BuildFrame(f.ID, f.Build(), threshold)
	if err != nil {
		return nil, err
	}
	f.frames = append(f.frames, thresholdFrame{threshold, frame})
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
// it is flushed in plaintext. The read buffer must be empty — whatever it
// holds was read before the swap and is ciphertext nobody would decrypt; a
// 1.20.1 client sends nothing after its Encryption Response until the
// server answers, so a non-empty buffer is a protocol violation.
func (c *Conn) Encrypt(wrap func(net.Conn) net.Conn) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	if c.rd != nil && c.rd.Buffered() > 0 {
		return ErrDataBeforeEncryption
	}
	encrypted := wrap(c.raw)
	c.raw = encrypted // under sendMu: Finish reads it from another goroutine
	if c.rd != nil {
		c.rd.Reset(encrypted)
	}
	return c.queue(msg{swap: encrypted})
}

// ErrDataBeforeEncryption: the client sent more right behind its
// Encryption Response.
var ErrDataBeforeEncryption = errors.New("data received before encryption was enabled")

// Read reads the next packet, waiting at most timeout.
func (c *Conn) Read(timeout time.Duration) (*bytes.Buffer, error) {
	_ = c.raw.SetReadDeadline(time.Now().Add(timeout))
	packet, err := protocol.ReadPacket(c.rd, c.threshold)
	if err != nil {
		return nil, err
	}
	_ = c.raw.SetReadDeadline(time.Time{})
	return packet, nil
}

// ReadSplit reads the next packet, uncompressed, as id + body — the login
// handler's Encryption Response, read straight off the socket.
func (c *Conn) ReadSplit() (int, []byte, error) {
	return protocol.ReadPacketSplit(c.rd, protocol.CompressionDisabled)
}

// RemoteAddr is the client's address (nil without a socket).
func (c *Conn) RemoteAddr() net.Addr {
	c.sendMu.Lock()
	raw := c.raw
	c.sendMu.Unlock()
	if raw == nil {
		return nil
	}
	return raw.RemoteAddr()
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
	c.sendMu.Lock()
	raw := c.raw
	c.sendMu.Unlock()
	if raw != nil {
		_ = raw.Close()
	}
	close(c.done)
}

// CoalesceWindow is how long the writer gathers frames after the first one
// before writing the batch: the latency it adds to a lone packet, well
// under a tick.
var CoalesceWindow = time.Millisecond

// framesWritten / flushes count, process-wide, the frames the writers sent
// and the socket writes that carried them (WriteStats).
var framesWritten, flushes atomic.Uint64

// WriteStats reports how many frames went out in how many socket writes —
// the batching ratio of the outbound queues (cmd/loadtest prints it).
func WriteStats() (frames, writes uint64) { return framesWritten.Load(), flushes.Load() }

// vectorWriter writes several buffers in one call where net.Buffers can't
// (it falls back to a Write per buffer for anything but a raw socket): the
// encryption wrapper encrypts the batch into one buffer and writes it once.
type vectorWriter interface {
	WriteVectored(bufs [][]byte) (int64, error)
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
		framesWritten.Add(uint64(len(pending)))
		flushes.Add(1)
		_ = current.SetWriteDeadline(time.Now().Add(5 * time.Second))
		var err error
		if vw, ok := current.(vectorWriter); ok {
			_, err = vw.WriteVectored(pending) // the encrypted socket
		} else {
			_, err = pending.WriteTo(current) // writev on a TCP socket
		}
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

	window := time.NewTimer(time.Hour)
	window.Stop()
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
		// Collect what else arrives within CoalesceWindow, then write it all
		// at once. The packets of one tick (movement broadcasts, entity
		// updates) are queued by different goroutines a few microseconds
		// apart; written as they came, nearly every frame cost its own
		// syscall (1.2 frames per write under load). Bound the batch so a
		// single write doesn't hold the cipher state forever.
		window.Reset(CoalesceWindow)
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
			case <-window.C:
				break drain
			}
		}
		window.Stop()
		if err := flush(); err != nil {
			fail(err)
			return
		}
	}
}
