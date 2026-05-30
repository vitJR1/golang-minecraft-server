package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/server"
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

	// Async reader for everything after login. Counts but doesn't interpret.
	go drainPackets(ctx, conn, threshold, s)

	// Steady-state writer: position updates at posHz.
	posInterval := time.Second / time.Duration(posHz)
	if posInterval <= 0 {
		posInterval = 50 * time.Millisecond
	}
	posTicker := time.NewTicker(posInterval)
	defer posTicker.Stop()

	rng := clientRand(id)
	x, y, z := float64(rng.Intn(64)-32), 80.0, float64(rng.Intn(64)-32)

	for {
		select {
		case <-ctx.Done():
			return
		case <-posTicker.C:
			x += (rng.Float64() - 0.5) * 0.6
			z += (rng.Float64() - 0.5) * 0.6
			if err := sendSetPos(conn, threshold, x, y, z, true); err != nil {
				s.errors.Add(1)
				return
			}
			s.packetsSent.Add(1)
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
	if err := writeAndCount(conn, server.SbHandshake, hs.Bytes(), threshold, s); err != nil {
		return 0, err
	}

	// Login Start: just the username for 1.20.1.
	if err := writeAndCount(conn, server.SbLoginStart, protocol.WriteString(name), threshold, s); err != nil {
		return 0, err
	}

	// First server packet: Set Compression. Parse threshold, switch.
	id, body, err := readWithDeadline(conn, threshold, 5*time.Second, s)
	if err != nil {
		return 0, fmt.Errorf("read Set Compression: %w", err)
	}
	if id != server.CbLoginSetCompr {
		return 0, fmt.Errorf("expected Set Compression (0x%02X), got 0x%02X", server.CbLoginSetCompr, id)
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
	if id != server.CbLoginSuccess {
		return 0, fmt.Errorf("expected LoginSuccess (0x%02X), got 0x%02X", server.CbLoginSuccess, id)
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
func drainPackets(ctx context.Context, conn net.Conn, threshold int, s *stats) {
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
			if ne, ok := err.(net.Error); ok && ne.Timeout() && ctx.Err() == nil {
				continue
			}
			if ctx.Err() == nil {
				s.errors.Add(1)
			}
			return
		}
		s.bytesRecv.Add(int64(cr.n))
		s.packetsRecv.Add(1)
		_ = buf
	}
}

// sendSetPos issues a Set Player Position packet (no rotation) with the
// given coords.
func sendSetPos(conn net.Conn, threshold int, x, y, z float64, onGround bool) error {
	var buf bytes.Buffer
	buf.Write(protocol.WriteDouble(x))
	buf.Write(protocol.WriteDouble(y))
	buf.Write(protocol.WriteDouble(z))
	if onGround {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return protocol.WritePacket(conn, server.SbPlaySetPos, buf.Bytes(), threshold)
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
