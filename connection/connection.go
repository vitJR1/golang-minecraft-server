package connection

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"minecraft-server/encryption"
	"minecraft-server/mojang"
	"minecraft-server/nbt"
	"minecraft-server/utils"
	"net"
	"sync/atomic"
	"time"
)

type ClientConnection struct {
	conn       net.Conn
	state      string
	playerName string
	playerID   int32
	closed     int32 // atomic flag
	done       chan struct{}
}

var publicASN1, private, _ = generateRSA()

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

	// Устанавливаем таймаут на запись
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer c.conn.SetWriteDeadline(time.Time{})

	err := utils.WritePacket(c.conn, packetID, payload)
	if err != nil {
		// Проверяем тип ошибки
		if c.isClosed() {
			return fmt.Errorf("connection closed during write")
		}

		// Проверяем на специфические ошибки сети
		if opErr, ok := err.(*net.OpError); ok {
			if opErr.Err.Error() == "write: broken pipe" ||
				opErr.Err.Error() == "connection reset by peer" {
				c.cleanup()
				return fmt.Errorf("client disconnected: %v", err)
			}
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

			err := c.safeWrite(0x21, payload)
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
	if packetID != 0x00 {
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
	case 0x00: // Status Request
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

	case 0x01: // Ping
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
	case 0x00: // Login Start
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

		serverID := ""
		verifyToken := make([]byte, 4)
		rand.Read(verifyToken)

		// Отправляем Encryption Request
		payload := utils.WriteString(serverID)
		payload = append(payload, utils.WriteVarInt32(int32(len(publicASN1)))...)
		payload = append(payload, publicASN1...)
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
		profile, err := mojang.VerifyWithMojang(c.playerName, serverID, sharedSecret, publicASN1)
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
		if err := c.safeWrite(0x02, payload); err != nil {
			return fmt.Errorf("sending login success: %w", err)
		}
		fmt.Println("Sent login success")

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
	case 0x15: // Keep Alive response
		id, err := utils.ReadLong(packet)
		if err != nil {
			return fmt.Errorf("reading keep alive response: %w", err)
		}
		fmt.Printf("Received keep alive response: %d\n", id)

	case 0x1A: // Chat message
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

	case 0x1B: // Player position
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

	default:
		fmt.Printf("Unknown play packet: 0x%02X, length: %d\n", packetID, packet.Len())
	}

	return nil
}

func (c *ClientConnection) sendPlayPackets() error {
	registryNBT := buildRegistryNBT()

	// Проверяем соединение перед каждой отправкой
	packets := []struct {
		name string
		f    func() error
	}{
		{"Join Game", func() error { return c.sendJoinGame(c.playerID, registryNBT) }},
		//{"Chunk data", c.sendChunk},
		//{"Light data", c.sendLightData},
		{"Player position", func() error { return c.sendPlayerPosition(0, 64, 0) }},
		//{"Player abilities", c.sendPlayerAbilities},
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
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Println("All play packets sent successfully")
	return nil
}

// Методы для отправки пакетов
func (c *ClientConnection) sendJoinGame(playerID int32, registryNBT []byte) error {
	payload := []byte{}

	payload = append(payload, utils.WriteVarInt32(playerID)...)
	payload = append(payload, 0)   // hardcore
	payload = append(payload, 1)   // gamemode
	payload = append(payload, 255) // previous gamemode

	payload = append(payload, utils.WriteVarInt32(1)...)
	payload = append(payload, utils.WriteString("minecraft:overworld")...)

	payload = append(payload, registryNBT...)

	payload = append(payload, utils.WriteString("minecraft:overworld")...)
	payload = append(payload, utils.WriteString("minecraft:overworld")...)

	payload = append(payload, utils.WriteLong(0)...)
	payload = append(payload, utils.WriteVarInt32(20)...)
	payload = append(payload, utils.WriteVarInt32(10)...)
	payload = append(payload, utils.WriteVarInt32(10)...)

	payload = append(payload, 0) // reduced debug info
	payload = append(payload, 1) // respawn screen
	payload = append(payload, 0) // is debug
	payload = append(payload, 1) // is flat

	payload = append(payload, 0)                         // no death location
	payload = append(payload, utils.WriteVarInt32(0)...) // portal cooldown

	return c.safeWrite(0x28, payload)
}

func (c *ClientConnection) sendChunk() error {
	payload := []byte{}

	chunkX := int32(0)
	chunkZ := int32(0)

	payload = append(payload, utils.WriteVarInt32(chunkX)...)
	payload = append(payload, utils.WriteVarInt32(chunkZ)...)
	payload = append(payload, 1) // Ground-Up Continuous

	// Primary Bit Mask (24 sections)
	primaryBitMask := int32(0)
	for i := 0; i < 24; i++ {
		primaryBitMask |= 1 << i
	}
	payload = append(payload, utils.WriteVarInt32(primaryBitMask)...)

	// Heightmaps
	heightmaps := buildHeightmapsNBT()
	payload = append(payload, heightmaps...)

	// Biomes (1024 ints)
	for i := 0; i < 1024; i++ {
		payload = append(payload, utils.WriteVarInt32(0)...)
	}

	// Chunk data
	chunkData := buildEmptyChunkData()
	payload = append(payload, utils.WriteVarInt32(int32(len(chunkData)))...)
	payload = append(payload, chunkData...)

	// Block Entities
	payload = append(payload, utils.WriteVarInt32(0)...)

	return c.safeWrite(0x22, payload)
}

func writeEmptyBitSet(buf *bytes.Buffer) {
	utils.WriteVarInt32ToBuffer(buf, 0) // length = 0 long'ов
}

func (c *ClientConnection) sendLightData() error {
	payload := bytes.NewBuffer(nil)

	utils.WriteVarInt32ToBuffer(payload, 0) // chunkX
	utils.WriteVarInt32ToBuffer(payload, 0) // chunkZ
	payload.WriteByte(1)                    // trust edges

	writeEmptyBitSet(payload) // sky mask
	writeEmptyBitSet(payload) // block mask
	writeEmptyBitSet(payload) // empty sky mask
	writeEmptyBitSet(payload) // empty block mask

	utils.WriteVarInt32ToBuffer(payload, 0) // sky arrays count
	utils.WriteVarInt32ToBuffer(payload, 0) // block arrays count

	return c.safeWrite(0x24, payload.Bytes())
}

func (c *ClientConnection) sendPlayerPosition(x, y, z float64) error {
	payload := []byte{}

	payload = append(payload, utils.WriteDouble(x)...)
	payload = append(payload, utils.WriteDouble(y)...)
	payload = append(payload, utils.WriteDouble(z)...)
	payload = append(payload, utils.WriteFloat(0)...) // yaw
	payload = append(payload, utils.WriteFloat(0)...) // pitch

	payload = append(payload, 0)                         // flags
	payload = append(payload, utils.WriteVarInt32(1)...) // teleport id
	payload = append(payload, 0)                         // dismount

	return c.safeWrite(0x38, payload)
}

func (c *ClientConnection) sendPlayerAbilities() error {
	payload := []byte{}
	payload = append(payload, 0x02|0x04)                 // flags: allow flying + flying
	payload = append(payload, utils.WriteFloat(0.05)...) // flying speed
	payload = append(payload, utils.WriteFloat(0.1)...)  // walking speed
	return c.safeWrite(0x32, payload)
}

// Вспомогательные функции
func buildRegistryNBT() []byte {
	w := nbt.New()
	w.WriteRootCompound()

	// Dimension Type Registry
	w.StartCompound("minecraft:dimension_type")
	w.String("type", "minecraft:dimension_type")
	w.StartList("value", nbt.TagCompound, 1)
	w.StartCompound("")
	w.String("name", "minecraft:overworld")
	w.Int("id", 0)
	w.StartCompound("element")
	w.Bool("piglin_safe", false)
	w.Bool("natural", true)
	w.Float("ambient_light", 0.0)
	w.String("infiniburn", "#minecraft:infiniburn_overworld")
	w.Bool("respawn_anchor_works", false)
	w.Bool("has_skylight", true)
	w.Bool("bed_works", true)
	w.String("effects", "minecraft:overworld")
	w.Bool("has_raids", true)
	w.Int("min_y", -64)
	w.Int("height", 384)
	w.Int("logical_height", 384)
	w.Float("coordinate_scale", 1.0)
	w.Bool("ultrawarm", false)
	w.Bool("has_ceiling", false)
	w.EndCompound() // element
	w.EndCompound() // dimension entry
	w.EndList()     // value list
	w.EndCompound() // dimension_type registry

	// Biome Registry
	w.StartCompound("minecraft:worldgen/biome")
	w.String("type", "minecraft:worldgen/biome")
	w.StartList("value", nbt.TagCompound, 1)
	w.StartCompound("")
	w.String("name", "minecraft:plains")
	w.Int("id", 0)
	w.StartCompound("element")
	w.String("precipitation", "rain")
	w.Float("temperature", 0.8)
	w.Float("downfall", 0.4)
	w.StartCompound("effects")
	w.Int("sky_color", 7907327)
	w.Int("water_color", 4159204)
	w.Int("water_fog_color", 329011)
	w.Int("fog_color", 12638463)
	w.EndCompound() // effects
	w.EndCompound() // element
	w.EndCompound() // biome entry
	w.EndList()     // value list
	w.EndCompound() // biome registry

	// Chat Type Registry
	w.StartCompound("minecraft:chat_type")
	w.String("type", "minecraft:chat_type")
	w.StartList("value", nbt.TagCompound, 1)

	w.StartCompound("")
	w.String("name", "minecraft:chat")
	w.Int("id", 0)

	w.StartCompound("element")
	w.StartCompound("chat")
	w.String("translation_key", "chat.type.text")
	w.StartList("parameters", nbt.TagString, 2)
	w.String("", "sender")
	w.String("", "content")
	w.EndList()     // parameters
	w.EndCompound() // chat
	w.EndCompound() // element

	w.EndCompound() // entry
	w.EndList()     // list
	w.EndCompound() // chat_type registry

	w.EndCompound() // root
	return w.Bytes()
}

func buildHeightmapsNBT() []byte {
	w := nbt.New()
	w.WriteRootCompound()
	heightmap := make([]int64, 36)
	w.LongArray("MOTION_BLOCKING", heightmap)
	w.EndCompound()
	return w.Bytes()
}

func buildEmptyChunkData() []byte {
	data := []byte{}
	for sectionY := 0; sectionY < 24; sectionY++ {
		data = append(data, utils.WriteUInt16(0)...)
		data = append(data, utils.WriteVarInt32(0)...) // Palette size
		data = append(data, utils.WriteVarInt32(0)...) // Single value (air)
		data = append(data, utils.WriteVarInt32(0)...) // Data array length
		data = append(data, utils.WriteVarInt32(0)...) // Biome palette size
		data = append(data, utils.WriteVarInt32(0)...) // Single biome value
		data = append(data, utils.WriteVarInt32(0)...) // Biome data length
	}
	return data
}

func generateRSA() ([]byte, *rsa.PrivateKey, error) {
	// Генерируем RSA ключи
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, nil, fmt.Errorf("generating RSA key: %w", err)
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling public key: %w", err)
	}
	return pubASN1, priv, nil
}
