package connection

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"minecraft-server/cfg"
	"minecraft-server/chunk"
	"minecraft-server/encryption"
	"minecraft-server/mojang"
	"minecraft-server/nbt"
	"minecraft-server/server"
	"minecraft-server/utils"
	"net"
	"sync/atomic"
	"time"
)

var encryptionReq, private, e = server.NewEncryptionRequest()

var publicKey = encryptionReq.PublicKey

type ClientConnection struct {
	conn       net.Conn
	state      string
	playerName string
	playerID   int32
	closed     int32 // atomic flag
	done       chan struct{}
}

func HandleConn(conn net.Conn) {
	client := &ClientConnection{
		conn:       conn,
		state:      "handshake",
		playerName: "",
		playerID:   1,
		closed:     0,
		done:       make(chan struct{}),
	}

	fmt.Printf("New connection from %s\n", conn.RemoteAddr())
	defer client.cleanup()

	// Запускаем keep-alive
	go client.keepAlive()

	// Основной цикл чтения
	client.readLoop()
}

func (c *ClientConnection) cleanup() {
	if atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		close(c.done)
		c.conn.Close()
		fmt.Printf("Connection from %s closed\n", c.conn.RemoteAddr())
	}
}

func (c *ClientConnection) isClosed() bool {
	return atomic.LoadInt32(&c.closed) == 1
}

func (c *ClientConnection) safeWrite(packetID int32, payload []byte) error {
	if c.isClosed() {
		return fmt.Errorf("connection already closed")
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer c.conn.SetWriteDeadline(time.Time{})

	if err := utils.WritePacket(c.conn, packetID, payload); err != nil {
		// timeout?
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			// можно не закрывать, а вернуть ошибку наверх
			return fmt.Errorf("write timeout: %w", err)
		}

		// клиент закрылся / pipe
		// (лучше errors.Is + syscall, но даже так лучше чем строки)
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

func (c *ClientConnection) keepAlive() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if c.state != "play" || c.isClosed() {
				continue
			}

			keepAliveID := time.Now().UnixNano()
			payload := utils.WriteLong(keepAliveID)

			err := c.safeWrite(Pong, payload)
			if err != nil {
				fmt.Printf("Keep-alive error: %v\n", err)
				return
			}
		}
	}
}

func (c *ClientConnection) readLoop() {
	defer c.cleanup()

	for {
		if c.isClosed() {
			return
		}

		// Устанавливаем таймаут на чтение
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		packet, err := utils.ReadPacket(c.conn)
		if err != nil {
			c.handleReadError(err)
			return
		}

		// Сбрасываем таймаут
		c.conn.SetReadDeadline(time.Time{})

		// Обрабатываем пакет
		if err := c.processPacket(packet); err != nil {
			fmt.Printf("Error processing packet: %v\n", err)

			// В play состоянии ошибки не обязательно фатальны
			if c.state != "play" {
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

	//case netErr, ok := err.(net.Error); ok && netErr.Timeout():
	//	fmt.Printf("Read timeout for %s, but connection alive\n", c.playerName)
	//	// Не закрываем соединение при таймауте
	//	return

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
	fmt.Println("Processing packet in state:", c.state)
	packetID, err := utils.ReadVarInt(packet)
	fmt.Println("Reading packet ID:", packetID)
	if err != nil {
		return fmt.Errorf("reading packet ID: %w", err)
	}

	switch c.state {
	case "handshake":
		return c.handleHandshake(packet, packetID)
	case "status":
		return c.handleStatus(packet, packetID)
	case "login":
		return c.handleLogin(packet, packetID)
	case "play":
		return c.handlePlay(packet, packetID)
	default:
		return fmt.Errorf("unknown state: %s", c.state)
	}
}

func (c *ClientConnection) handleHandshake(packet *bytes.Buffer, packetID int) error {
	if packetID != Check {
		return fmt.Errorf("expected handshake packet (0x00), got 0x%02X", packetID)
	}

	// protocol version
	_, err := utils.ReadVarInt(packet)
	if err != nil {
		return fmt.Errorf("reading protocol version: %w", err)
	}

	// server address
	_, err = utils.ReadStringFromBuf(packet)
	if err != nil {
		return fmt.Errorf("reading server address: %w", err)
	}

	// port
	_, err = utils.ReadUShortFromBuf(packet)
	if err != nil {
		return fmt.Errorf("reading port: %w", err)
	}

	// next state
	nextState, err := utils.ReadVarInt(packet)
	if err != nil {
		return fmt.Errorf("reading next state: %w", err)
	}

	if nextState == 1 {
		c.state = "status"
		fmt.Println("Switched to status state")
	} else {
		c.state = "login"
		fmt.Println("Switched to login state")
	}

	return nil
}

func (c *ClientConnection) handleStatus(packet *bytes.Buffer, packetID int) error {
	switch packetID {
	case Status: // Status Request
		fmt.Println("Received status request")
		resp := map[string]any{
			"version": map[string]any{
				"name":     "1.20.1",
				"protocol": 763,
			},
			"players": map[string]any{
				"max":    20,
				"online": 0,
			},
			"description": map[string]any{
				"text": "§aGoLang test server 🚀",
			},
		}
		data, _ := json.Marshal(resp)
		response := utils.WriteString(string(data))
		return c.safeWrite(0x00, response)

	case Ping: // Ping
		fmt.Println("Received ping")
		payload := make([]byte, 8)
		if _, err := packet.Read(payload); err != nil {
			return fmt.Errorf("reading ping payload: %w", err)
		}

		// Отправляем pong
		if err := c.safeWrite(0x01, payload); err != nil {
			return err
		}

		// Пинг завершен, закрываем соединение
		c.cleanup()
		return nil

	default:
		return fmt.Errorf("unknown status packet: 0x%02X", packetID)
	}
}

func (c *ClientConnection) handleLogin(packet *bytes.Buffer, packetID int) error {
	switch packetID {
	case LoginStart: // Login Start
		var err error
		c.playerName, err = utils.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading player name: %w", err)
		}

		fmt.Printf("Player connecting: %s\n", c.playerName)

		// Проверяем, не закрыто ли соединение
		if c.isClosed() {
			return fmt.Errorf("connection closed during login")
		}

		if cfg.OnlineMode {
			fmt.Printf("Online mode enabled, will verify player %s with Mojang\n", c.playerName)
			verifyToken := make([]byte, 4)
			rand.Read(verifyToken)

			// Отправляем Encryption Request
			payload := utils.WriteString(cfg.ServerId)
			payload = append(payload, utils.WriteVarInt32(int32(len(publicKey)))...)
			payload = append(payload, publicKey...)
			payload = append(payload, utils.WriteVarInt32(int32(len(verifyToken)))...)
			payload = append(payload, verifyToken...)

			if err := c.safeWrite(0x01, payload); err != nil {
				return fmt.Errorf("sending encryption request: %w", err)
			}
			fmt.Println("Sent encryption request")

			// Читаем Encryption Response через обычное соединение (еще не зашифровано)
			id, data, err := utils.ReadPacket2(c.conn)
			if err != nil {
				// Если клиент отключился во время чтения, просто выходим
				if c.isClosed() {
					return nil
				}
				return fmt.Errorf("reading encryption response: %w", err)
			}
			if id != 0x01 {
				return fmt.Errorf("expected encryption response (0x01), got 0x%02X", id)
			}
			fmt.Println("Received encryption response")

			// Парсим ответ
			dataBuf := bytes.NewBuffer(data)
			sharedEnc, err := utils.ReadByteArrayFromBuf(dataBuf)
			if err != nil {
				return fmt.Errorf("reading shared secret: %w", err)
			}

			verifyEnc, err := utils.ReadByteArrayFromBuf(dataBuf)
			if err != nil {
				return fmt.Errorf("reading verify token: %w", err)
			}

			// Расшифровываем
			sharedSecret, err := rsa.DecryptPKCS1v15(rand.Reader, private, sharedEnc)
			if err != nil {
				return fmt.Errorf("decrypting shared secret: %w", err)
			}

			verifyBack, err := rsa.DecryptPKCS1v15(rand.Reader, private, verifyEnc)
			if err != nil {
				return fmt.Errorf("decrypting verify token: %w", err)
			}

			if !bytes.Equal(verifyBack, verifyToken) {
				return fmt.Errorf("verify token mismatch")
			}
			fmt.Println("Encryption verification successful")

			// Проверяем через Mojang
			fmt.Printf("Verifying player %s with Mojang...\n", c.playerName)
			profile, err := mojang.VerifyWithMojang(c.playerName, cfg.ServerId, sharedSecret, publicKey)
			if err != nil {
				fmt.Printf("Mojang verification failed: %v\n", err)
				msg := []byte(`{"text":"Authentication failed: ` + err.Error() + `"}`)
				c.safeWrite(0x00, utils.WriteString(string(msg)))
				return fmt.Errorf("mojang verification: %w", err)
			}
			fmt.Printf("✅ Authentication successful: %s (%s)\n", profile.Name, profile.ID)

			//Создаем шифрованное соединение ПОСЛЕ успешной верификации
			block, err := aes.NewCipher(sharedSecret)
			if err != nil {
				return fmt.Errorf("creating cipher: %w", err)
			}

			encryptStream := encryption.NewCFB8(block, sharedSecret, false)
			decryptStream := encryption.NewCFB8(block, sharedSecret, true)
			c.conn = encryption.WrapEncryptedConn(c.conn, encryptStream, decryptStream)
			fmt.Println("Encrypted connection established")

			// Отправляем Login Success
			payload = utils.WriteString(profile.ID)
			payload = append(payload, utils.WriteString(profile.Name)...)
			if err := c.safeWrite(LoginSuccess, payload); err != nil {
				return fmt.Errorf("sending login success: %w", err)
			}
			fmt.Println("Sent login success")
		} else {
			uuid := utils.OfflineUUID(c.playerName)

			fmt.Println("Player uuid", uuid)

			payload := []byte{}
			payload = append(payload, utils.WriteString("69a19ea74f2e4a68bbe262363c7aeaf0")...)
			payload = append(payload, utils.WriteString(c.playerName)...)
			payload = append(payload, utils.WriteVarInt32(0)...) // properties count = 0

			if err := c.safeWrite(LoginSuccess, payload); err != nil {
				return fmt.Errorf("sending login success: %w", err)
			}

			c.state = "play"
		}

		// Переходим в play состояние
		c.state = "play"

		// Отправляем все необходимые пакеты для входа в игру
		// Но не фатально, если клиент отключился
		if err := c.sendPlayPackets(); err != nil {
			if c.isClosed() {
				fmt.Printf("Client disconnected during play packets: %v\n", err)
				return nil
			}
			return fmt.Errorf("sending play packets: %w", err)
		}

		fmt.Printf("Player %s fully initialized and in game!\n", c.playerName)

	default:
		fmt.Printf("Unknown login packet: 0x%02X\n", packetID)
	}

	return nil
}

func (c *ClientConnection) handlePlay(packet *bytes.Buffer, packetID int) error {
	// Проверяем, не закрыто ли соединение
	if c.isClosed() {
		return fmt.Errorf("connection closed")
	}

	switch packetID {
	case KeepAlive: // Keep Alive response
		id, err := utils.ReadLong(packet)
		if err != nil {
			return fmt.Errorf("reading keep alive response: %w", err)
		}
		fmt.Printf("Received keep alive response: %d\n", id)

	case ChatMessage: // Chat message
		message, err := utils.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading chat message: %w", err)
		}
		fmt.Printf("[CHAT] %s: %s\n", c.playerName, message)

		// Эхо-ответ (только если соединение еще открыто)
		if !c.isClosed() {
			response := fmt.Sprintf("{\"text\":\"You said: %s\"}", message)
			c.safeWrite(0x1A, utils.WriteString(response))
		}

	case PlayerPosition: // Player position
		x, err := utils.ReadDouble(packet)
		if err != nil {
			return fmt.Errorf("reading position X: %w", err)
		}
		y, err := utils.ReadDouble(packet)
		if err != nil {
			return fmt.Errorf("reading position Y: %w", err)
		}
		z, err := utils.ReadDouble(packet)
		if err != nil {
			return fmt.Errorf("reading position Z: %w", err)
		}
		onGround, err := utils.ReadBool(packet)
		if err != nil {
			return fmt.Errorf("reading onGround: %w", err)
		}
		fmt.Printf("Player position: %.2f, %.2f, %.2f, onGround: %v\n", x, y, z, onGround)

	case TeleportConfirm: // Teleport Confirm (serverbound)
		id, _ := utils.ReadVarInt(packet)
		fmt.Println("Teleport confirmed:", id)

	default:
		fmt.Printf("Unknown play packet: 0x%02X, length: %d\n", packetID, packet.Len())
	}

	return nil
}

func (c *ClientConnection) sendPlayPackets() error {
	registryNBT := nbt.BuildRegistryNBT()

	fmt.Println("registryNBT length", len(registryNBT))

	// Проверяем соединение перед каждой отправкой
	packets := []struct {
		name string
		f    func() error
	}{
		{"Join Game", func() error { return c.sendJoinGame(registryNBT) }},
		{"Chunk Data", c.sendChunkData},
		{"Update Light", func() error { return c.sendUpdateLight(0, 0) }},
		{"Player abilities", c.sendPlayerAbilities},
		{"Player position", func() error { return c.sendPlayerPosition(0, 64, 0) }},
	}

	for _, p := range packets {
		if c.isClosed() {
			return fmt.Errorf("connection closed during %s", p.name)
		}

		fmt.Printf("Sending %s...\n", p.name)
		if err := p.f(); err != nil {
			// Если ошибка из-за закрытого соединения, просто выходим
			if c.isClosed() || err.Error() == "client disconnected: write: broken pipe" {
				return fmt.Errorf("client disconnected during %s", p.name)
			}
			return fmt.Errorf("%s: %w", p.name, err)
		}

		// Небольшая задержка между пакетами
		time.Sleep(10 * time.Millisecond)
	}

	//time.Sleep(5000 * time.Millisecond)

	fmt.Println("All play packets sent successfully")
	return nil
}

// Методы для отправки пакетов
func (c *ClientConnection) sendJoinGame(registryNBT []byte) error {
	payload := []byte{}

	// 1. Entity ID (int)
	payload = append(payload, utils.WriteInt(c.playerID)...)

	// 2. Is hardcore (boolean) - 0 = false, 1 = true
	payload = append(payload, 0)

	// 3. Gamemode (unsigned byte)
	// 0: survival, 1: creative, 2: adventure, 3: spectator
	payload = append(payload, 1) // creative для теста, можно 0 для survival

	// 4. Previous gamemode (byte) - -1 = none
	payload = append(payload, 255) // -1 as byte

	// 5. Dimension count (VarInt)
	payload = append(payload, utils.WriteVarInt32(1)...)

	// 6. Dimension names (array of strings)
	payload = append(payload, utils.WriteString("minecraft:overworld")...)
	payload = append(payload, utils.WriteString("minecraft:the_nether")...)
	payload = append(payload, utils.WriteString("minecraft:the_end")...)

	// 7. Registry codec (NBT)
	payload = append(payload, registryNBT...)

	// 8. Current dimension (String)
	payload = append(payload, utils.WriteString("minecraft:overworld")...)

	// 9. World name (String)
	payload = append(payload, utils.WriteString("minecraft:overworld")...)

	// 10. Hashed seed (long)
	payload = append(payload, utils.WriteLong(0)...)

	// 11. Max players (VarInt) - игнорируется клиентом, но обязателен
	payload = append(payload, utils.WriteVarInt32(20)...)

	// 12. View distance (VarInt) - чанков
	payload = append(payload, utils.WriteVarInt32(10)...)

	// 13. Simulation distance (VarInt) - чанков
	payload = append(payload, utils.WriteVarInt32(10)...)

	// 14. Reduced debug info (boolean)
	payload = append(payload, 0)

	// 15. Enable respawn screen (boolean)
	payload = append(payload, 1)

	// 16. Is debug (boolean)
	payload = append(payload, 0)

	// 17. Is flat (boolean)
	payload = append(payload, 0)

	// 18. Death location (optional) - 0 = no death location
	payload = append(payload, 0)

	// 19. Portal cooldown (VarInt) - добавлено в 1.20
	payload = append(payload, utils.WriteVarInt32(0)...)

	return c.safeWrite(SendJoinGamePosition, payload)
}

func (c *ClientConnection) sendChunkAndUpdateLight() error {
	buf := bytes.NewBuffer(nil)

	chunkX := int32(0)
	chunkZ := int32(0)

	// 1. Chunk position
	buf.Write(utils.WriteInt(chunkX))
	buf.Write(utils.WriteInt(chunkZ))

	// 2. Heightmaps (NBT compound)
	heightmaps := nbt.BuildHeightmapsNBT()
	buf.Write(heightmaps)

	// 3. Chunk Data
	chunkData := chunk.BuildEmptyChunkData()
	utils.WriteVarInt32ToBuffer(buf, int32(len(chunkData)))
	buf.Write(chunkData)

	// 4. Block entities count
	utils.WriteVarInt32ToBuffer(buf, 0)

	// 5. Light Data (встроенный)
	buf.WriteByte(1) // trust edges = true

	utils.WriteVarInt32ToBuffer(buf, 0) // sky light mask
	utils.WriteVarInt32ToBuffer(buf, 0) // block light mask
	utils.WriteVarInt32ToBuffer(buf, 0) // empty sky mask
	utils.WriteVarInt32ToBuffer(buf, 0) // empty block mask

	utils.WriteVarInt32ToBuffer(buf, 0) // sky arrays count
	utils.WriteVarInt32ToBuffer(buf, 0) // block arrays count

	return c.safeWrite(0x22, buf.Bytes())
}

func (c *ClientConnection) sendChunkData() error {
	buf := bytes.NewBuffer(nil)

	chunkX := int32(0)
	chunkZ := int32(0)

	// Chunk X/Z — INT
	buf.Write(utils.WriteInt(chunkX))
	buf.Write(utils.WriteInt(chunkZ))

	// Heightmaps NBT
	buf.Write(nbt.BuildHeightmapsNBT())

	// Chunk Data
	chunkData := chunk.BuildEmptyChunkData()
	utils.WriteVarInt32ToBuffer(buf, int32(len(chunkData)))
	buf.Write(chunkData)

	// Block entities count
	utils.WriteVarInt32ToBuffer(buf, 0)

	return c.safeWrite(0x22, buf.Bytes())
}

func (c *ClientConnection) sendUpdateLight(chunkX, chunkZ int32) error {
	buf := bytes.NewBuffer(nil)

	// Chunk X/Z — VarInt (как в твоей таблице)
	utils.WriteVarInt32ToBuffer(buf, chunkX)
	utils.WriteVarInt32ToBuffer(buf, chunkZ)

	// В 1.20.1 world sections = 24 (min_y=-64, height=384 => 384/16 = 24)
	// BitSet содержит bits for (sections + 2) => 26 бит.
	sections := 24
	bits := sections + 2 // 26

	// mask with lowest `bits` set to 1
	emptyAll := uint64(0)
	if bits == 64 {
		emptyAll = ^uint64(0)
	} else {
		emptyAll = (uint64(1) << uint(bits)) - 1
	}

	// Sky Light Mask (BitSet) — 0
	writeBitSet(buf, []uint64{0})

	// Block Light Mask (BitSet) — 0
	writeBitSet(buf, []uint64{0})

	// Empty Sky Light Mask — all ones for 26 bits
	writeBitSet(buf, []uint64{emptyAll})

	// Empty Block Light Mask — all ones for 26 bits
	writeBitSet(buf, []uint64{emptyAll})

	// Sky Light array count — 0 (совпадает с количеством битов в SkyLightMask)
	utils.WriteVarInt32ToBuffer(buf, 0)

	// Block Light array count — 0
	utils.WriteVarInt32ToBuffer(buf, 0)

	// Update Light packet id в 1.20.1: 0x27 (как у тебя)
	return c.safeWrite(0x27, buf.Bytes())
}

// BitSet в протоколе: VarInt (кол-во long'ов) + long'и (8 байт каждый)
// Long в протоколе — big-endian (твоя utils.WriteLong как раз big-endian).
func writeBitSet(buf *bytes.Buffer, longs []uint64) {
	utils.WriteVarInt32ToBuffer(buf, int32(len(longs)))
	for _, v := range longs {
		buf.Write(utils.WriteLong(int64(v)))
	}
}

func (c *ClientConnection) sendPlayerPosition(x, y, z float64) error {
	payload := []byte{}

	//The packet sends X, Y, Z coordinates (doubles),
	//yaw/pitch (floats),
	//teleport ID (varint),
	//and a flag boolean for relative movement.

	payload = append(payload, utils.WriteDouble(x)...)
	payload = append(payload, utils.WriteDouble(y)...)
	payload = append(payload, utils.WriteDouble(z)...)
	payload = append(payload, utils.WriteFloat(0)...) // yaw
	payload = append(payload, utils.WriteFloat(0)...) // pitch

	payload = append(payload, 0)                         // flags
	payload = append(payload, utils.WriteVarInt32(1)...) // teleport id
	payload = append(payload, 0)                         // dismount

	return c.safeWrite(SendPlayerPosition, payload)
}

func (c *ClientConnection) sendPlayerAbilities() error {
	flags := byte(0x02 | 0x04) // allow flying + flying

	payload := []byte{}
	payload = append(payload, flags)
	payload = append(payload, utils.WriteFloat(0.05)...)
	payload = append(payload, utils.WriteFloat(0.1)...)

	fmt.Printf("Sending player abilities - flags: 0x%02X (allow flying: %v, flying: %v)\n",
		flags,
		flags&0x02 != 0,
		flags&0x04 != 0)

	return c.safeWrite(0x32, payload)
}
