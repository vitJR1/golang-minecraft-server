package mojang

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"time"
)

type MojangProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SessionServer is the hasJoined endpoint's base URL (tests point it at an
// httptest server).
var SessionServer = "https://sessionserver.mojang.com"

// Timeout bounds one hasJoined round trip; ctx may cut it shorter.
const Timeout = 10 * time.Second

// client never waits forever on Mojang — the login handler's goroutine is
// blocked on it.
var client = &http.Client{Timeout: Timeout}

// ServerHash is the hasJoined serverId: SHA-1 of serverId + shared secret +
// public key as Mojang's signed ("negative") hex big integer.
func ServerHash(serverId string, sharedSecret, publicKey []byte) string {
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
	return hash.Text(16) // hex без ведущих нулей
}

// VerifyWithMojang asks the session server whether username joined with
// this handshake and returns the verified profile.
func VerifyWithMojang(ctx context.Context, username, serverId string, sharedSecret, publicKey []byte) (*MojangProfile, error) {
	// Имя и hash экранируются: сырое имя вида "x&serverId=…" подменило бы
	// параметры запроса.
	q := url.Values{}
	q.Set("username", username)
	q.Set("serverId", ServerHash(serverId, sharedSecret, publicKey))
	endpoint := SessionServer + "/session/minecraft/hasJoined?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building hasJoined request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hasJoined request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mojang auth failed: %d %s", resp.StatusCode, string(body))
	}

	var profile MojangProfile
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&profile); err != nil {
		return nil, fmt.Errorf("decoding hasJoined profile: %w", err)
	}
	if profile.ID == "" || profile.Name == "" {
		return nil, fmt.Errorf("mojang auth failed: empty profile")
	}
	return &profile, nil
}
