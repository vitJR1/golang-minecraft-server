package mojang

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
)

type MojangProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func VerifyWithMojang(username, serverId string, sharedSecret, publicKey []byte) (*MojangProfile, error) {
	h := sha1.New()
	h.Write([]byte(serverId))
	h.Write(sharedSecret)
	h.Write(publicKey)
	sum := h.Sum(nil)

	// Mojang требует отрицательный hash для отрицательных big.Int
	hash := new(big.Int).SetBytes(sum)
	if (sum[0] & 0x80) != 0 {
		// отрицательный → доп. код
		hash = hash.Sub(hash, new(big.Int).Lsh(big.NewInt(1), uint(len(sum)*8)))
	}

	serverHash := hash.Text(16) // hex без ведущих нулей
	url := fmt.Sprintf("https://sessionserver.mojang.com/session/minecraft/hasJoined?username=%s&serverId=%s",
		username, serverHash)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mojang auth failed: %d %s", resp.StatusCode, string(body))
	}

	var profile MojangProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, err
	}
	return &profile, nil
}
