package server

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
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
		fmt.Printf("Unknown login packet: 0x%02X\n", packetID)
		return nil
	}

	name, err := protocol.ReadStringFromBuf(packet)
	if err != nil {
		return fmt.Errorf("reading player name: %w", err)
	}
	c.playerName = name
	fmt.Printf("Player connecting: %s\n", c.playerName)

	if c.isClosed() {
		return fmt.Errorf("server closed during login")
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
	c.server.Players.Add(c)

	if err := c.sendPlayPackets(); err != nil {
		if c.isClosed() {
			fmt.Printf("Client disconnected during play packets: %v\n", err)
			return nil
		}
		return fmt.Errorf("sending play packets: %w", err)
	}
	fmt.Printf("Player %s fully initialized and in game!\n", c.playerName)
	return nil
}

// CompressionThreshold matches the vanilla server default. Packets at or
// above this size go through zlib; smaller packets pass uncompressed.
const CompressionThreshold = 256

// enableCompression sends Set Compression (Cb 0x03) over the still-
// uncompressed wire, then flips the connection's threshold so every
// subsequent packet in both directions uses compressed framing.
//
// Synchronization: safeWrite holds c.writeMu while it sends Set Compression;
// we also acquire writeMu when updating c.compressionThreshold so the
// happens-before edge propagates to keepAlive's reads of the same field.
// readLoop runs in the same goroutine as handleLogin, so no extra
// coordination is needed for the read path.
func (c *ClientConnection) enableCompression() error {
	if err := c.safeWrite(CbLoginSetCompr, protocol.WriteVarInt32(CompressionThreshold)); err != nil {
		return fmt.Errorf("sending set compression: %w", err)
	}
	c.writeMu.Lock()
	c.compressionThreshold = CompressionThreshold
	c.writeMu.Unlock()
	return nil
}

// runOfflineLogin skips Mojang verification: derives a v3 UUID from the
// player's name and immediately sends LoginSuccess in plaintext.
func (c *ClientConnection) runOfflineLogin() error {
	uuidStr := protocol.OfflineUUID(c.playerName)
	fmt.Println("Player uuid:", uuidStr)

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
	fmt.Printf("Online mode: will verify %s with Mojang\n", c.playerName)

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
		fmt.Printf("Mojang verification failed: %v\n", err)
		msg := []byte(`{"text":"Authentication failed: ` + err.Error() + `"}`)
		_ = c.safeWrite(CbLoginDisconnect, protocol.WriteString(string(msg)))
		return fmt.Errorf("mojang verification: %w", err)
	}
	fmt.Printf("Authentication successful: %s (%s)\n", profile.Name, profile.ID)

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
	fmt.Println("Sent login success")
	return nil
}

func (c *ClientConnection) sendEncryptionRequest(verifyToken []byte) error {
	payload := make([]byte, 0, 64)
	payload = append(payload, protocol.WriteString(cfg.ServerId)...)
	payload = append(payload, protocol.WriteVarInt32(int32(len(publicKey)))...)
	payload = append(payload, publicKey...)
	payload = append(payload, protocol.WriteVarInt32(int32(len(verifyToken)))...)
	payload = append(payload, verifyToken...)
	payload = append(payload, 0x01) // should authenticate

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

	c.writeMu.Lock()
	c.conn = encryption.WrapEncryptedConn(c.conn, enc, dec)
	c.writeMu.Unlock()
	fmt.Println("Encrypted connection established")
	return nil
}
