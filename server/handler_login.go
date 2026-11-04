package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"minecraft-server/cfg"
	"minecraft-server/encryption"
	"minecraft-server/mojang"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/login"
	"minecraft-server/server/net/packets"
	"net"
	"time"
)

// loginTimeout bounds the login phase from Login Start to Login Success:
// the wait for the Encryption Response and the Mojang round trip share it,
// so a client that stalls mid-handshake (or a hung session server) can't
// hold a connection slot. A var so tests can shorten it.
var loginTimeout = 30 * time.Second

// prePlayTimeout bounds a connection from accept to the play state (status
// pings included): loginTimeout plus a few seconds for the handshake.
var prePlayTimeout = loginTimeout + 5*time.Second

// errLoginAborted: the connection closed (or the login deadline hit) while
// the login was in flight; nothing more to say to the client.
var errLoginAborted = errors.New("login aborted")

// identity is who the login proved the player to be: the Login Start name
// (offline) or the Mojang-verified profile (online).
type identity struct {
	name string
	uuid [16]byte
}

func (c *ClientConnection) handleLogin(packet *bytes.Buffer, packetID int) error {
	if packetID != packetid.SbLoginStart {
		// Login Start is the only packet the client sends unprompted (the
		// Encryption Response is read inline below; we never send a Login
		// Plugin Request). Anything else is a misbehaving client — drop it
		// instead of letting it keep the slot by trickling junk.
		return fmt.Errorf("unexpected login packet 0x%02X", packetID)
	}

	name, err := packets.ReadLoginStart(packet)
	if err != nil {
		return err
	}
	if !login.ValidName(name) {
		slog.Info("invalid username, rejecting", "len", len(name))
		_ = c.safeWrite(packetid.CbLoginDisconnect, login.DisconnectPayload(login.InvalidName))
		c.cleanup()
		return nil
	}
	c.playerName = name
	slog.Info("player connecting", "player", c.playerName)

	if c.isClosed() {
		return fmt.Errorf("server closed during login")
	}

	// IP ban from the auth plugin's failure tracking. Engages before
	// anything expensive (Mojang auth, ban-list lookup) so spammers
	// get bounced fast.
	if g := c.server.authGate; g != nil {
		if until, banned := g.IPBanned(c.IP()); banned {
			slog.Info("auth: IP banned, rejecting",
				"player", c.playerName, "until", until)
			_ = c.safeWrite(packetid.CbLoginDisconnect, login.DisconnectPayload(login.IPBanned(until)))
			c.cleanup()
			return nil
		}
	}

	deadline := time.Now().Add(loginTimeout)
	var id identity
	if cfg.OnlineMode {
		id, err = c.runOnlineLogin(deadline)
	} else {
		id, err = c.runOfflineLogin()
	}
	if err != nil {
		if errors.Is(err, errLoginAborted) {
			slog.Info("login aborted", "player", c.playerName, "err", err)
			c.cleanup()
			return nil
		}
		return err
	}
	// From here on the name is the verified one (online: Mojang's profile
	// name, which the ban list and ops are keyed by).
	c.playerName = id.name

	// Ban check on the verified name — before verification it is whatever
	// the client typed (online mode), not who it is.
	banned, err := c.server.Bans.IsBanned(context.Background(), c.playerName)
	if err != nil {
		// Backend hiccup (Postgres timeout etc.). Fail open: the ban list is
		// a moderation aid, not an auth gate, and refusing every login while
		// the DB blips would be worse. Logged so it's not silent.
		slog.Warn("ban lookup failed, allowing login", "player", c.playerName, "err", err)
	}
	if banned != nil {
		if err := c.safeWrite(packetid.CbLoginDisconnect, login.DisconnectPayload(login.Banned(banned.Reason, banned.ExpiresAt))); err != nil {
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
		_ = c.safeWrite(packetid.CbLoginDisconnect, login.DisconnectPayload(login.ServerFull(c.server.PlayerCount(), cfg.MaxPlayers)))
		c.cleanup()
		return nil
	}

	c.player = player.New(c.server.nextEntityID.Add(1), id.name, id.uuid)
	if err := c.enableCompression(); err != nil {
		return err
	}
	if err := c.safeWrite(packetid.CbLoginSuccess, login.SuccessPayload(id.uuid, id.name)); err != nil {
		return fmt.Errorf("sending login success: %w", err)
	}
	slog.Debug("login success sent", "player", id.name)

	c.state = StatePlay
	// Default landing: hub. With the auth plugin enabled, players go
	// to the dedicated auth instance instead — they're moved to hub
	// once /register or /login succeeds.
	if c.server.authGate != nil && c.server.Auth != nil {
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
	// proceed. No-op without a gate.
	if g := c.server.authGate; g != nil {
		c.authed.Store(false)
		g.Joined(c)
	}

	slog.Info("player joined", "player", c.playerName, "instance", c.instance.ID)
	return nil
}

// CompressionThreshold matches the vanilla server default. Packets at or
// above this size go through zlib; smaller packets pass uncompressed.
const CompressionThreshold = 256

// enableCompression queues Set Compression (Cb 0x03) and switches the
// connection to compressed framing (conn.Conn.EnableCompression: the switch
// can't reorder frames).
func (c *ClientConnection) enableCompression() error {
	return c.link.EnableCompression(packetid.CbLoginSetCompr, protocol.WriteVarInt32(CompressionThreshold), CompressionThreshold)
}

// runOfflineLogin skips Mojang verification: the Login Start name with its
// v3 offline UUID.
func (c *ClientConnection) runOfflineLogin() (identity, error) {
	uuid, err := login.OfflineUUID(c.playerName)
	if err != nil {
		return identity{}, err
	}
	slog.Debug("offline uuid resolved", "player", c.playerName, "uuid", protocol.UUIDString(uuid))
	return identity{name: c.playerName, uuid: uuid}, nil
}

// runOnlineLogin performs the Mojang-verified handshake: Encryption Request
// → Encryption Response → RSA decrypt → enable AES CFB8 → Mojang verify —
// all before deadline. Encryption goes on before the Mojang call, as in
// vanilla: the client encrypts from its Encryption Response on, so every
// later frame — the "Authentication failed" disconnect included — has to be
// encrypted to be readable.
func (c *ClientConnection) runOnlineLogin(deadline time.Time) (identity, error) {
	slog.Info("online-mode login: verifying with Mojang", "player", c.playerName)

	verifyToken := make([]byte, 4)
	_, _ = rand.Read(verifyToken)

	if err := c.sendEncryptionRequest(verifyToken); err != nil {
		return identity{}, err
	}
	sharedSecret, err := c.recvAndVerifyEncryptionResponse(verifyToken, deadline)
	if err != nil {
		return identity{}, err
	}
	if err := c.enableEncryption(sharedSecret); err != nil {
		return identity{}, err
	}

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	profile, err := mojang.VerifyWithMojang(ctx, c.playerName, cfg.ServerId, sharedSecret, keys.Public)
	if err == nil && !login.ValidName(profile.Name) {
		err = fmt.Errorf("mojang returned an invalid profile name %q", profile.Name)
	}
	if err != nil {
		slog.Warn("mojang verification failed", "player", c.playerName, "err", err)
		// The error itself (URL, response body) stays in the log.
		_ = c.safeWrite(packetid.CbLoginDisconnect, login.DisconnectPayload(
			login.AuthFailed(errors.New("couldn't verify your session with Mojang"))))
		c.cleanup()
		return identity{}, fmt.Errorf("%w: mojang verification: %w", errLoginAborted, err)
	}
	slog.Info("mojang verified", "player", profile.Name, "uuid", profile.ID)

	uuid, err := login.ProfileUUID(profile.ID)
	if err != nil {
		return identity{}, err
	}
	return identity{name: profile.Name, uuid: uuid}, nil
}

// sendEncryptionRequest sends Encryption Request with the server's public
// key and this connection's verify token (login.EncryptionRequestPayload).
func (c *ClientConnection) sendEncryptionRequest(verifyToken []byte) error {
	if err := c.safeWrite(packetid.CbLoginEncRequest, login.EncryptionRequestPayload(cfg.ServerId, keys.Public, verifyToken)); err != nil {
		return fmt.Errorf("sending encryption request: %w", err)
	}
	return nil
}

// recvAndVerifyEncryptionResponse reads the Encryption Response off the still-
// plaintext connection, RSA-decrypts the shared secret + verify token, and
// confirms the verify token round-trips. Returns the AES key on success. The
// read itself has no deadline (conn.Conn.ReadSplit), so a watchdog closes
// the connection at deadline; the read then fails with errLoginAborted.
func (c *ClientConnection) recvAndVerifyEncryptionResponse(verifyToken []byte, deadline time.Time) ([]byte, error) {
	watchdog := time.AfterFunc(time.Until(deadline), func() {
		slog.Info("login timed out waiting for the encryption response", "player", c.playerName)
		c.cleanup()
	})
	// Encryption Response arrives before Set Compression, so this read is
	// always uncompressed regardless of the connection's current threshold.
	id, data, err := c.link.ReadSplit()
	if !watchdog.Stop() || c.isClosed() {
		return nil, fmt.Errorf("%w: reading encryption response: %w", errLoginAborted, err)
	}
	if err != nil {
		return nil, fmt.Errorf("reading encryption response: %w", err)
	}
	if id != packetid.SbLoginEncResponse {
		return nil, fmt.Errorf("expected encryption response (0x01), got 0x%02X", id)
	}

	resp, err := packets.ReadEncryptionResponse(bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	return keys.Decrypt(resp, verifyToken)
}

func (c *ClientConnection) enableEncryption(sharedSecret []byte) error {
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		return fmt.Errorf("creating cipher: %w", err)
	}
	enc := encryption.NewCFB8Encrypt(block, sharedSecret)
	dec := encryption.NewCFB8Decrypt(block, sharedSecret)
	// The read side switches at once (this runs on the read loop), the
	// write side once the plaintext frames queued before it are flushed.
	if err := c.link.Encrypt(func(raw net.Conn) net.Conn {
		return encryption.WrapEncryptedConn(raw, enc, dec)
	}); err != nil {
		return err
	}
	slog.Debug("encrypted connection established", "player", c.playerName)
	return nil
}
