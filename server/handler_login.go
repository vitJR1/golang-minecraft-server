package server

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"minecraft-server/ban"
	"minecraft-server/cfg"
	"minecraft-server/encryption"
	"minecraft-server/mojang"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"time"
)

func (c *ClientConnection) handleLogin(packet *bytes.Buffer, packetID int) error {
	if packetID != SbLoginStart {
		slog.Warn("unknown login packet", "packet_id", fmt.Sprintf("0x%02X", packetID))
		return nil
	}

	name, err := protocol.ReadStringFromBuf(packet)
	if err != nil {
		return fmt.Errorf("reading player name: %w", err)
	}
	c.playerName = name
	slog.Info("player connecting", "player", c.playerName)

	if c.isClosed() {
		return fmt.Errorf("server closed during login")
	}

	// IP ban from the auth plugin's failure tracking. Engages before
	// anything expensive (Mojang auth, ban-list lookup) so spammers
	// get bounced fast.
	if authStore != nil {
		if until, allowed := authStore.CheckIPAllowed(c.conn.RemoteAddr()); !allowed {
			slog.Info("auth: IP banned, rejecting",
				"player", c.playerName, "until", until)
			msg := []byte(`{"text":"IP banned (auth failures) until ` +
				until.Format("2006-01-02 15:04:05") + `"}`)
			_ = c.safeWrite(CbLoginDisconnect, protocol.WriteString(string(msg)))
			c.cleanup()
			return nil
		}
	}

	if banned := ban.IsBanned(c.playerName); banned != nil {
		msg := []byte(`{"text":"You are banned from this server.\nReason: ` + banned.Reason +
			`. Expires: ` + banned.ExpiresAt.Format(time.RFC1123) + `"}`)
		if err := c.safeWrite(CbLoginDisconnect, protocol.WriteString(string(msg))); err != nil {
			return fmt.Errorf("sending ban message: %w", err)
		}
		c.cleanup()
		return nil
	}

	// Hard player cap. Ops bypass so admins can always rescue. Race
	// window between PlayerCount() and the actual JoinAndAnnounce is
	// small; an extra player slipping in under burst load is acceptable
	// for now — proper enforcement would need an atomic counter
	// incremented inside JoinAndAnnounce under the same lock as the
	// PlayerList.Add.
	if cfg.MaxPlayers > 0 &&
		c.server.PlayerCount() >= cfg.MaxPlayers &&
		!c.server.Ops.Has(c.playerName) {
		slog.Info("server full, rejecting",
			"player", c.playerName,
			"online", c.server.PlayerCount(),
			"max", cfg.MaxPlayers)
		msg := []byte(`{"text":"Server is full (` +
			fmt.Sprintf("%d/%d", c.server.PlayerCount(), cfg.MaxPlayers) +
			`)"}`)
		_ = c.safeWrite(CbLoginDisconnect, protocol.WriteString(string(msg)))
		c.cleanup()
		return nil
	}

	if cfg.OnlineMode {
		if err := c.runOnlineLogin(); err != nil {
			return err
		}
	} else {
		if err := c.runOfflineLogin(); err != nil {
			return err
		}
	}

	c.state = StatePlay
	// Default landing: hub. With the auth plugin enabled, players go
	// to the dedicated auth instance instead — they're moved to hub
	// once /register or /login succeeds.
	if authStore != nil && c.server.Auth != nil {
		c.instance = c.server.Auth
	} else {
		c.instance = c.server.Hub
	}

	if err := c.sendPlayPackets(); err != nil {
		if c.isClosed() {
			slog.Info("client disconnected during play packets",
				"player", c.playerName, "err", err)
			return nil
		}
		return fmt.Errorf("sending play packets: %w", err)
	}

	// Register + announce atomically. Doing both under the instance's
	// joinMu means another player joining concurrently can't observe c
	// half-registered (in the PlayerList but not yet visible to everyone),
	// which would otherwise produce duplicate or out-of-order Spawn packets.
	c.instance.JoinAndAnnounce(c)

	// Auth gate: in offline mode with the plugin enabled, flip the
	// connection's `authed` bool to false and tell the player how to
	// proceed. No-op when authStore == nil.
	promptAuth(c)

	slog.Info("player joined", "player", c.playerName, "instance", c.instance.ID)
	return nil
}

// CompressionThreshold matches the vanilla server default. Packets at or
// above this size go through zlib; smaller packets pass uncompressed.
const CompressionThreshold = 256

// enableCompression queues Set Compression (Cb 0x03) and flips the
// connection's threshold under one critical section. Holding sendMu around
// both the push and the threshold update means any concurrent safeWrite
// either: (a) builds + pushes its frame with the old threshold *before*
// Set Compression goes into the queue, or (b) builds + pushes with the new
// threshold *after*. The queue is FIFO, so wire order matches build order.
func (c *ClientConnection) enableCompression() error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if c.isClosed() {
		return fmt.Errorf("connection closed")
	}
	// Built with the *current* threshold (CompressionDisabled), so this packet
	// goes on the wire uncompressed — the client decodes it before switching.
	frame, err := protocol.BuildFrame(
		CbLoginSetCompr,
		protocol.WriteVarInt32(CompressionThreshold),
		c.compressionThreshold,
	)
	if err != nil {
		return fmt.Errorf("build Set Compression: %w", err)
	}
	select {
	case c.outbound <- outboundMsg{frame: frame}:
	default:
		return fmt.Errorf("send queue full")
	}
	c.compressionThreshold = CompressionThreshold
	return nil
}

// runOfflineLogin skips Mojang verification: derives a v3 UUID from the
// player's name and immediately sends LoginSuccess in plaintext.
func (c *ClientConnection) runOfflineLogin() error {
	uuidStr := protocol.OfflineUUID(c.playerName)
	slog.Debug("offline uuid resolved", "player", c.playerName, "uuid", uuidStr)

	uuidBytes, err := protocol.WriteUUID(uuidStr)
	if err != nil {
		return fmt.Errorf("writing UUID: %w", err)
	}
	var uuid [16]byte
	copy(uuid[:], uuidBytes)
	c.player = player.New(c.server.nextEntityID.Add(1), c.playerName, uuid)

	if err := c.enableCompression(); err != nil {
		return err
	}

	payload := make([]byte, 0, 32+len(c.playerName))
	payload = append(payload, uuid[:]...)
	payload = append(payload, protocol.WriteString(c.playerName)...)
	payload = append(payload, protocol.WriteVarInt32(0)...) // properties count = 0

	if err := c.safeWrite(CbLoginSuccess, payload); err != nil {
		return fmt.Errorf("sending login success: %w", err)
	}
	return nil
}

// runOnlineLogin performs the full Mojang-verified handshake: Encryption
// Request → Encryption Response → RSA decrypt → Mojang verify → enable AES
// CFB8 → LoginSuccess.
func (c *ClientConnection) runOnlineLogin() error {
	slog.Info("online-mode login: verifying with Mojang", "player", c.playerName)

	verifyToken := make([]byte, 4)
	_, _ = rand.Read(verifyToken)

	if err := c.sendEncryptionRequest(verifyToken); err != nil {
		return err
	}
	sharedSecret, err := c.recvAndVerifyEncryptionResponse(verifyToken)
	if err != nil {
		return err
	}

	profile, err := mojang.VerifyWithMojang(c.playerName, cfg.ServerId, sharedSecret, publicKey)
	if err != nil {
		slog.Warn("mojang verification failed", "player", c.playerName, "err", err)
		msg := []byte(`{"text":"Authentication failed: ` + err.Error() + `"}`)
		_ = c.safeWrite(CbLoginDisconnect, protocol.WriteString(string(msg)))
		return fmt.Errorf("mojang verification: %w", err)
	}
	slog.Info("mojang verified", "player", profile.Name, "uuid", profile.ID)

	if err := c.enableEncryption(sharedSecret); err != nil {
		return err
	}

	parsedUUID, err := protocol.FormatUUID(profile.ID)
	if err != nil {
		return fmt.Errorf("formatting UUID: %w", err)
	}
	uuidBytes, err := protocol.WriteUUID(parsedUUID)
	if err != nil {
		return fmt.Errorf("writing UUID: %w", err)
	}
	var uuid [16]byte
	copy(uuid[:], uuidBytes)
	c.player = player.New(c.server.nextEntityID.Add(1), profile.Name, uuid)

	if err := c.enableCompression(); err != nil {
		return err
	}

	payload := make([]byte, 0, 32+len(profile.Name))
	payload = append(payload, uuid[:]...)
	payload = append(payload, protocol.WriteString(profile.Name)...)
	payload = append(payload, protocol.WriteVarInt32(0)...) // properties count = 0
	if err := c.safeWrite(CbLoginSuccess, payload); err != nil {
		return fmt.Errorf("sending login success: %w", err)
	}
	slog.Debug("login success sent", "player", profile.Name)
	return nil
}

// Encryption Request (1.20.1 protocol 763) carries exactly three fields:
//
//	serverId    String
//	publicKey   ByteArray (VarInt length + bytes)
//	verifyToken ByteArray (VarInt length + bytes)
//
// A "should authenticate" boolean was added in 1.20.4 — DO NOT send it
// here, the 1.20.1 client rejects the packet with "found 1 bytes extra".
func (c *ClientConnection) sendEncryptionRequest(verifyToken []byte) error {
	payload := make([]byte, 0, 64)
	payload = append(payload, protocol.WriteString(cfg.ServerId)...)
	payload = append(payload, protocol.WriteVarInt32(int32(len(publicKey)))...)
	payload = append(payload, publicKey...)
	payload = append(payload, protocol.WriteVarInt32(int32(len(verifyToken)))...)
	payload = append(payload, verifyToken...)

	if err := c.safeWrite(CbLoginEncRequest, payload); err != nil {
		return fmt.Errorf("sending encryption request: %w", err)
	}
	return nil
}

// recvAndVerifyEncryptionResponse reads the Encryption Response off the still-
// plaintext connection, RSA-decrypts the shared secret + verify token, and
// confirms the verify token round-trips. Returns the AES key on success.
func (c *ClientConnection) recvAndVerifyEncryptionResponse(verifyToken []byte) ([]byte, error) {
	// Encryption Response arrives before Set Compression, so this read is
	// always uncompressed regardless of the connection's current threshold.
	id, data, err := protocol.ReadPacketSplit(c.conn, protocol.CompressionDisabled)
	if err != nil {
		if c.isClosed() {
			return nil, nil
		}
		return nil, fmt.Errorf("reading encryption response: %w", err)
	}
	if id != SbLoginEncResponse {
		return nil, fmt.Errorf("expected encryption response (0x01), got 0x%02X", id)
	}

	buf := bytes.NewBuffer(data)
	sharedEnc, err := protocol.ReadByteArrayFromBuf(buf)
	if err != nil {
		return nil, fmt.Errorf("reading shared secret: %w", err)
	}
	verifyEnc, err := protocol.ReadByteArrayFromBuf(buf)
	if err != nil {
		return nil, fmt.Errorf("reading verify token: %w", err)
	}

	sharedSecret, err := rsa.DecryptPKCS1v15(rand.Reader, private, sharedEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypting shared secret: %w", err)
	}
	verifyBack, err := rsa.DecryptPKCS1v15(rand.Reader, private, verifyEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypting verify token: %w", err)
	}
	if !bytes.Equal(verifyBack, verifyToken) {
		return nil, fmt.Errorf("verify token mismatch")
	}
	return sharedSecret, nil
}

func (c *ClientConnection) enableEncryption(sharedSecret []byte) error {
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		return fmt.Errorf("creating cipher: %w", err)
	}
	enc := encryption.NewCFB8Encrypt(block, sharedSecret)
	dec := encryption.NewCFB8Decrypt(block, sharedSecret)
	encrypted := encryption.WrapEncryptedConn(c.conn, enc, dec)

	// Read side runs in this goroutine (readLoop calls down through
	// handler_login), so swapping c.conn here is safe — the next ReadPacket
	// call uses the wrapped conn.
	c.conn = encrypted

	// Write side runs in writerLoop. Sending the swap via the channel
	// guarantees pending plaintext frames are flushed *before* the cipher
	// state begins.
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.isClosed() {
		return fmt.Errorf("connection closed")
	}
	select {
	case c.outbound <- outboundMsg{swapConn: encrypted}:
	default:
		return fmt.Errorf("send queue full")
	}
	slog.Debug("encrypted connection established", "player", c.playerName)
	return nil
}
