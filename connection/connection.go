package connection

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"minecraft-server/mojang"
	"minecraft-server/nbt"
	"minecraft-server/utils"
	"net"
)

func HandleConn(conn net.Conn) {
	defer conn.Close()

	state := "handshake"

	for {
		packet, err := utils.ReadPacket(conn)
		if err != nil {
			fmt.Println("Client disconnected:", err)
			return
		}

		packetID, _ := utils.ReadVarInt(packet)

		switch state {
		case "handshake":
			_, _ = utils.ReadVarInt(packet)          // protocol version
			_, _ = utils.ReadString(packet)          // server address
			_, _ = utils.ReadUShort(packet)          // port
			nextState, _ := utils.ReadVarInt(packet) // 1=status, 2=login
			if nextState == 1 {
				state = "status"
			} else {
				state = "login"
			}

		case "status":
			switch packetID {
			case 0x00:
				resp := map[string]any{
					"version": map[string]any{"name": "1.20.1", "protocol": 763},
					"players": map[string]any{"max": 20, "online": 0},
					"description": map[string]any{
						"text": "§aGoLang test server 🚀",
					},
				}
				data, _ := json.Marshal(resp)
				_ = utils.WritePacket(conn, 0x00, append(utils.WriteVarInt32(int32(len(data))), data...))
			case 0x01:
				payload := make([]byte, 8)
				if _, err := io.ReadFull(packet, payload); err == nil {
					_ = utils.WritePacket(conn, 0x01, payload)
				}
				return
			}
		case "login":
			if packetID == 0x00 { // Login Start
				playerName, err := utils.ReadString(packet)
				if err != nil {
					fmt.Println("read name:", err)
					return
				}

				fmt.Printf("Lic. player connecting: %s\n", playerName)

				// === Генерируем RSA ключи для шифрования ===
				priv, _ := rsa.GenerateKey(rand.Reader, 1024)
				pubASN1, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
				if err != nil {
					fmt.Println("marshal pubkey:", err)
					return
				}

				serverID := "" // всегда пусто
				verifyToken := make([]byte, 4)
				rand.Read(verifyToken)

				payload := utils.WriteString(serverID)
				payload = append(payload, utils.WriteVarInt32(int32(len(pubASN1)))...)
				payload = append(payload, pubASN1...)
				payload = append(payload, utils.WriteVarInt32(int32(len(verifyToken)))...)
				payload = append(payload, verifyToken...)

				if err := utils.WritePacket(conn, 0x01, payload); err != nil {
					fmt.Println("write encryption req:", err)
					return
				}

				// === Читаем Encryption Response ===
				id, data, err := utils.ReadPacket2(conn)
				if err != nil {
					fmt.Println("read encryption resp:", err)
					return
				}
				if id != 0x01 {
					fmt.Println("Expected Encryption Response, got", id)
					return
				}

				sharedEnc, rest, _ := utils.ReadByteArray(data)
				verifyEnc, _, _ := utils.ReadByteArray(rest)

				sharedSecret, err := rsa.DecryptPKCS1v15(rand.Reader, priv, sharedEnc)

				if err != nil {
					fmt.Println("decrypt shared:", err)
					return
				}

				verifyBack, err := rsa.DecryptPKCS1v15(rand.Reader, priv, verifyEnc)
				if err != nil {
					fmt.Println("decrypt verify:", err)
					return
				}

				if !bytes.Equal(verifyBack, verifyToken) {
					fmt.Println("verify token mismatch!")
					return
				}

				block, err := aes.NewCipher(sharedSecret)
				if err != nil {
					fmt.Println("aes:", err)
					return
				}

				encryptStream := cipher.NewCFBEncrypter(block, sharedSecret)
				decryptStream := cipher.NewCFBDecrypter(block, sharedSecret)

				conn = utils.WrapEncryptedConn(conn, encryptStream, decryptStream)

				fmt.Printf("Checking player with mojang\n")

				// === Проверяем игрока через Mojang ===
				profile, err := mojang.VerifyWithMojang(playerName, serverID, sharedSecret, pubASN1)
				if err != nil {
					msg := []byte(`{"text":"Auth failed: ` + err.Error() + `"}`)
					_ = utils.WritePacket(conn, 0x00, append(utils.WriteVarInt32(int32(len(msg))), msg...))
					return
				}

				fmt.Printf("✅ Auth OK: %s (%s)\n", profile.Name, profile.ID)

				// === Отправляем Login Success ===
				payload = append(utils.WriteString(profile.ID), utils.WriteString(profile.Name)...)
				if err := utils.WritePacket(conn, 0x02, payload); err != nil {
					return
				}

				playerID := int32(1) // можно юзать счетчик для нескольких игроков

				state = "play"

				if err := sendChunk(conn); err != nil {
					return
				}

				registryNBT := buildRegistryNBT() // должен вернуть []byte полноценного NBT

				if err := sendJoinGame(conn, playerID, registryNBT); err != nil {
					return
				}

				if err := sendPlayerPosition(conn, 0, 64, 0); err != nil {
					return
				}

				fmt.Printf("Player logged in: %s\n", profile.Name)

				continue
			}
		}
	}
}

func buildRegistryNBT() []byte {
	return BuildRoot(func(w *nbt.Writer) {
		// dimension_type
		w.StartCompound("minecraft:dimension_type")
		w.StartCompound("overworld")
		w.Float("coordinate_scale", 1.0)
		w.Int("min_y", 0)
		w.Int("height", 384)
		w.Int("logical_height", 384)
		w.Float("ambient_light", 0.0)
		w.String("natural", "true")
		w.EndCompound() // overworld
		w.EndCompound() // dimension_type

		// worldgen/biome
		w.StartCompound("minecraft:worldgen/biome")
		w.StartCompound("plains")
		w.String("precipitation", "rain")
		w.Float("temperature", 0.8)
		w.Float("downfall", 0.4)
		w.StartCompound("effects")
		w.Int("sky_color", 7907327)
		w.Int("water_color", 4159204)
		w.Int("fog_color", 12638463)
		w.EndCompound() // effects
		w.String("category", "plains")
		w.EndCompound() // plains
		w.EndCompound() // biome
	})
}

func BuildRoot(f func(w *nbt.Writer)) []byte {
	w := nbt.New()

	// root compound
	w.StartCompound("")

	f(w)

	w.EndCompound()

	return w.Bytes()
}

func sendChunk(conn net.Conn) error {
	payload := []byte{}

	chunkX := int32(0)
	chunkZ := int32(0)

	payload = append(payload, utils.WriteVarInt32(chunkX)...)
	payload = append(payload, utils.WriteVarInt32(chunkZ)...)
	payload = append(payload, 1) // Ground-Up Continuous true

	payload = append(payload, utils.WriteVarInt32(1)...) // Primary Bit Mask: только 1 секция

	// Heightmaps (пустой NBT)
	payload = append(payload, BuildRoot(func(w *nbt.Writer) {})...)

	// Biomes (1024 ints) → все 0
	for i := 0; i < 1024; i++ {
		payload = append(payload, utils.WriteVarInt32(0)...)
	}

	// Data Length (VarInt)
	// Chunk data (пустой секции 16x16x16 air)
	sectionData := make([]byte, 16*16*16/2) // для Minecraft 1.20 air блок = 0, packed 4 bit
	dataBuf := append(utils.WriteVarInt32(int32(len(sectionData))), sectionData...)
	payload = append(payload, dataBuf...)

	// Block Entities
	payload = append(payload, utils.WriteVarInt32(0)...) // count 0

	return utils.WritePacket(conn, 0x22, payload)
}

func sendJoinGame(conn net.Conn, playerID int32, registryNBT []byte) error {
	payload := []byte{}

	payload = append(payload, utils.WriteVarInt32(playerID)...)
	payload = append(payload, 0) // hardcore
	payload = append(payload, 1) // survival
	payload = append(payload, 255)

	payload = append(payload, utils.WriteVarInt32(1)...)
	payload = append(payload, utils.WriteString("minecraft:overworld")...)

	payload = append(payload, registryNBT...)

	payload = append(payload, utils.WriteString("minecraft:overworld")...)
	payload = append(payload, utils.WriteString("minecraft:overworld")...)

	payload = append(payload, utils.WriteLong(0)...)
	payload = append(payload, utils.WriteVarInt32(20)...)
	payload = append(payload, utils.WriteVarInt32(10)...)
	payload = append(payload, utils.WriteVarInt32(10)...) // simulation distance

	payload = append(payload, 0)
	payload = append(payload, 1)
	payload = append(payload, 0)
	payload = append(payload, 1)

	payload = append(payload, 0) // no death location

	return utils.WritePacket(conn, 0x28, payload)
}

func sendPlayerPosition(conn net.Conn, x, y, z float64) error {
	payload := []byte{}

	payload = append(payload, utils.WriteDouble(x)...)
	payload = append(payload, utils.WriteDouble(y)...)
	payload = append(payload, utils.WriteDouble(z)...)
	payload = append(payload, utils.WriteFloat(0)...)
	payload = append(payload, utils.WriteFloat(0)...)

	payload = append(payload, 0)                         // flags
	payload = append(payload, utils.WriteVarInt32(1)...) // teleport id
	payload = append(payload, 0)                         // dismount

	return utils.WritePacket(conn, 0x38, payload)
}
