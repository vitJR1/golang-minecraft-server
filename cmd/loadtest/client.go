package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
)

// runClient simulates one Minecraft client: connects, logs in offline, then
// alternates between async packet reads and 20Hz position writes until ctx
// is cancelled.
func runClient(ctx context.Context, addr string, id int, posHz int, s *stats) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		s.errors.Add(1)
		return
	}
	s.connected.Add(1)
	defer func() {
		_ = conn.Close()
		s.disconnects.Add(1)
	}()

	name := fmt.Sprintf("Bot-%05d", id)

	loginStart := time.Now()
	threshold, err := doLogin(conn, name, s)
	if err != nil {
		s.errors.Add(1)
		return
	}
	s.loggedIn.Add(1)
	s.loginLatTotal.Add(int64(time.Since(loginStart)))

	// Async reader for everything after login: counts, and answers what a
	// vanilla client answers (teleports, pings) — the server ignores
	// movement until its teleport is confirmed.
	ses := &session{conn: conn, threshold: threshold}
	go drainPackets(ctx, ses, s)

	// Steady-state writer: position updates at posHz.
	posInterval := time.Second / time.Duration(posHz)
	if posInterval <= 0 {
		posInterval = 50 * time.Millisecond
	}
	posTicker := time.NewTicker(posInterval)
	defer posTicker.Stop()

	rng := clientRand(id)
	for {
		select {
		case <-ctx.Done():
			return
		case <-posTicker.C:
			// A small step from where the server last put us (within the
			// anticheat's walking pace).
			x, y, z, ok := ses.step((rng.Float64()-0.5)*0.4, (rng.Float64()-0.5)*0.4)
			if !ok {
				continue // no position from the server yet
			}
			if err := ses.write(packetid.SbPlaySetPos, setPosPayload(x, y, z, true)); err != nil {
				s.errors.Add(1)
				return
			}
			s.packetsSent.Add(1)
		}
	}
}

// session is a logged-in client's connection, written from the movement
// loop and the reader (replies).
type session struct {
	conn      net.Conn
	threshold int

	mu      sync.Mutex // writes + position
	x, y, z float64
	havePos bool
}

func (ses *session) write(id int32, payload []byte) error {
	ses.mu.Lock()
	defer ses.mu.Unlock()
	return protocol.WritePacket(ses.conn, id, payload, ses.threshold)
}

// step moves the position by dx, dz and returns it.
func (ses *session) step(dx, dz float64) (x, y, z float64, ok bool) {
	ses.mu.Lock()
	defer ses.mu.Unlock()
	if !ses.havePos {
		return 0, 0, 0, false
	}
	ses.x += dx
	ses.z += dz
	return ses.x, ses.y, ses.z, true
}

// answer replies like a vanilla client: Synchronize Player Position moves
// us and is confirmed, a Ping is answered with a Pong.
func (ses *session) answer(id int, body []byte) {
	switch id {
	case packetid.CbPlaySyncPos:
		// x, y, z (3×Double) + yaw, pitch (2×Float) + flags (Byte) + VarInt id.
		if len(body) < 34 {
			return
		}
		f := func(i int) float64 {
			return math.Float64frombits(uint64(body[i])<<56 | uint64(body[i+1])<<48 | uint64(body[i+2])<<40 |
				uint64(body[i+3])<<32 | uint64(body[i+4])<<24 | uint64(body[i+5])<<16 | uint64(body[i+6])<<8 | uint64(body[i+7]))
		}
		x, y, z, flags := f(0), f(8), f(16), body[32]
		tp, _, err := protocol.ReadVarIntFromBytes(body[33:])
		if err != nil {
			return
		}
		ses.mu.Lock()
		if flags&0x01 != 0 {
			x += ses.x
		}
		if flags&0x02 != 0 {
			y += ses.y
		}
		if flags&0x04 != 0 {
			z += ses.z
		}
		ses.x, ses.y, ses.z, ses.havePos = x, y, z, true
		ses.mu.Unlock()
		_ = ses.write(packetid.SbPlayTeleportConfirm, protocol.WriteVarInt32(int32(tp)))
	case packetid.CbPlayPing:
		if len(body) >= 4 {
			_ = ses.write(packetid.SbPlayPong, append([]byte(nil), body[:4]...))
		}
	}
}

// doLogin walks Handshake -> LoginStart -> Set Compression -> LoginSuccess
// -> the three play packets sent before announceJoin. Returns the
// compression threshold the server negotiated.
func doLogin(conn net.Conn, name string, s *stats) (int, error) {
	threshold := protocol.CompressionDisabled

	// Handshake (1.20.1 = protocol 763, nextState = 2 login).
	var hs bytes.Buffer
	hs.Write(protocol.WriteVarInt32(763))
	hs.Write(protocol.WriteString("127.0.0.1"))
	hs.WriteByte(0x63) // port hi (25565 = 0x63DD)
	hs.WriteByte(0xDD) // port lo
	hs.Write(protocol.WriteVarInt32(2))
	if err := writeAndCount(conn, packetid.SbHandshake, hs.Bytes(), threshold, s); err != nil {
		return 0, err
	}

	// Login Start: just the username for 1.20.1.
	if err := writeAndCount(conn, packetid.SbLoginStart, protocol.WriteString(name), threshold, s); err != nil {
		return 0, err
	}

	// First server packet: Set Compression. Parse threshold, switch.
	id, body, err := readWithDeadline(conn, threshold, 5*time.Second, s)
	if err != nil {
		return 0, fmt.Errorf("read Set Compression: %w", err)
	}
	if id != packetid.CbLoginSetCompr {
		return 0, fmt.Errorf("expected Set Compression (0x%02X), got 0x%02X", packetid.CbLoginSetCompr, id)
	}
	thr, err := protocol.ReadVarInt(body)
	if err != nil {
		return 0, fmt.Errorf("decode threshold: %w", err)
	}
	threshold = thr

	// LoginSuccess.
	id, _, err = readWithDeadline(conn, threshold, 5*time.Second, s)
	if err != nil {
		return 0, fmt.Errorf("read LoginSuccess: %w", err)
	}
	if id != packetid.CbLoginSuccess {
		return 0, fmt.Errorf("expected LoginSuccess (0x%02X), got 0x%02X", packetid.CbLoginSuccess, id)
	}

	// sendPlayPackets order: Login(Play), Chunk Data, SyncPos, World State (no-op for empty world).
	for i := 0; i < 3; i++ {
		if _, _, err := readWithDeadline(conn, threshold, 5*time.Second, s); err != nil {
			return 0, fmt.Errorf("read play packet %d: %w", i, err)
		}
	}
	return threshold, nil
}

// writeAndCount sends a packet and bumps stats counters.
func writeAndCount(conn net.Conn, id int32, payload []byte, threshold int, s *stats) error {
	before := s.bytesSent.Load()
	cw := &countWriter{w: conn}
	if err := protocol.WritePacket(cw, id, payload, threshold); err != nil {
		return err
	}
	s.bytesSent.Add(int64(cw.n))
	s.packetsSent.Add(1)
	_ = before
	return nil
}

// readWithDeadline reads one packet, bumps stats, and returns its decoded
// header plus payload buffer.
func readWithDeadline(conn net.Conn, threshold int, deadline time.Duration, s *stats) (int, *bytes.Buffer, error) {
	_ = conn.SetReadDeadline(time.Now().Add(deadline))
	cr := &countReader{r: conn}
	buf, err := protocol.ReadPacket(cr, threshold)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return 0, nil, err
	}
	s.bytesRecv.Add(int64(cr.n))
	s.packetsRecv.Add(1)
	id, err := protocol.ReadVarInt(buf)
	if err != nil {
		return 0, nil, err
	}
	return id, buf, nil
}

// drainPackets reads server packets and updates counters. Loops until ctx
// is cancelled or the connection errors out (which is also how disconnect
// is observed).
func drainPackets(ctx context.Context, ses *session, s *stats) {
	conn, threshold := ses.conn, ses.threshold
	for {
		if ctx.Err() != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		cr := &countReader{r: conn}
		buf, err := protocol.ReadPacket(cr, threshold)
		if err != nil {
			// Read deadline is mostly a way to notice ctx.Done; only count it
			// as an error if ctx is still live.
			// ReadPacket wraps the error: errors.As, not a type assertion.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && ctx.Err() == nil {
				continue
			}
			if ctx.Err() == nil {
				s.errors.Add(1)
			}
			return
		}
		s.bytesRecv.Add(int64(cr.n))
		s.packetsRecv.Add(1)
		if id, err := protocol.ReadVarInt(buf); err == nil {
			ses.answer(id, buf.Bytes())
		}
	}
}

// setPosPayload is a Set Player Position body (no rotation).
func setPosPayload(x, y, z float64, onGround bool) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteDouble(x))
	buf.Write(protocol.WriteDouble(y))
	buf.Write(protocol.WriteDouble(z))
	if onGround {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// --- Small wrappers around net.Conn that count bytes flowing through. ---

type countWriter struct {
	w net.Conn
	n int
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// Satisfy net.Conn methods used by protocol.WritePacket (it only needs
// Write, but the API takes net.Conn). We delegate everything else.
func (c *countWriter) Read(p []byte) (int, error)         { return c.w.Read(p) }
func (c *countWriter) Close() error                       { return c.w.Close() }
func (c *countWriter) LocalAddr() net.Addr                { return c.w.LocalAddr() }
func (c *countWriter) RemoteAddr() net.Addr               { return c.w.RemoteAddr() }
func (c *countWriter) SetDeadline(t time.Time) error      { return c.w.SetDeadline(t) }
func (c *countWriter) SetReadDeadline(t time.Time) error  { return c.w.SetReadDeadline(t) }
func (c *countWriter) SetWriteDeadline(t time.Time) error { return c.w.SetWriteDeadline(t) }

type countReader struct {
	r net.Conn
	n int
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}
func (c *countReader) Write(p []byte) (int, error)        { return c.r.Write(p) }
func (c *countReader) Close() error                       { return c.r.Close() }
func (c *countReader) LocalAddr() net.Addr                { return c.r.LocalAddr() }
func (c *countReader) RemoteAddr() net.Addr               { return c.r.RemoteAddr() }
func (c *countReader) SetDeadline(t time.Time) error      { return c.r.SetDeadline(t) }
func (c *countReader) SetReadDeadline(t time.Time) error  { return c.r.SetReadDeadline(t) }
func (c *countReader) SetWriteDeadline(t time.Time) error { return c.r.SetWriteDeadline(t) }
