// Package login holds the pure half of the login state (protocol 763): the
// server's RSA keys, the Encryption Request payload and decrypting the
// response, the player UUIDs, the Login Success payload and the Login
// Disconnect texts. Reading the socket, Mojang and the session state stay in
// the server (handler_login.go).
package login

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/server/net/packets"
)

// Keys is the server's RSA keypair for online-mode logins.
type Keys struct {
	Public  []byte // DER (PKIX): what the Encryption Request sends
	Private *rsa.PrivateKey
}

// NewKeys generates a fresh 1024-bit keypair, like vanilla.
func NewKeys() (Keys, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return Keys{}, fmt.Errorf("generating RSA key: %w", err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return Keys{}, fmt.Errorf("marshaling public key: %w", err)
	}
	return Keys{Public: pub, Private: priv}, nil
}

// EncryptionRequestPayload builds Encryption Request. On 1.20.1 it carries
// exactly three fields:
//
//	serverId    String
//	publicKey   ByteArray (VarInt length + bytes)
//	verifyToken ByteArray (VarInt length + bytes)
//
// A "should authenticate" boolean was added in 1.20.4 — DO NOT send it
// here, the 1.20.1 client rejects the packet with "found 1 bytes extra".
func EncryptionRequestPayload(serverID string, publicKey, verifyToken []byte) []byte {
	payload := make([]byte, 0, 64)
	payload = append(payload, protocol.WriteString(serverID)...)
	payload = append(payload, protocol.WriteVarInt32(int32(len(publicKey)))...)
	payload = append(payload, publicKey...)
	payload = append(payload, protocol.WriteVarInt32(int32(len(verifyToken)))...)
	payload = append(payload, verifyToken...)
	return payload
}

// Decrypt RSA-decrypts the response's shared secret and verify token and
// checks the token round-tripped. Returns the shared secret (the AES key).
func (k Keys) Decrypt(resp packets.EncryptionResponse, verifyToken []byte) ([]byte, error) {
	secret, err := rsa.DecryptPKCS1v15(rand.Reader, k.Private, resp.SharedSecret)
	if err != nil {
		return nil, fmt.Errorf("decrypting shared secret: %w", err)
	}
	back, err := rsa.DecryptPKCS1v15(rand.Reader, k.Private, resp.VerifyToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting verify token: %w", err)
	}
	if !bytes.Equal(back, verifyToken) {
		return nil, fmt.Errorf("verify token mismatch")
	}
	return secret, nil
}

// OfflineUUID is the UUID an offline-mode player gets (vanilla
// "OfflinePlayer:<name>", see protocol.OfflineUUID).
func OfflineUUID(name string) ([16]byte, error) {
	return uuidBytes(protocol.OfflineUUID(name))
}

// ProfileUUID parses a Mojang profile id (32 hex digits, no dashes).
func ProfileUUID(id string) ([16]byte, error) {
	s, err := protocol.FormatUUID(id)
	if err != nil {
		return [16]byte{}, fmt.Errorf("formatting UUID: %w", err)
	}
	return uuidBytes(s)
}

func uuidBytes(s string) ([16]byte, error) {
	var uuid [16]byte
	b, err := protocol.WriteUUID(s)
	if err != nil {
		return uuid, fmt.Errorf("writing UUID: %w", err)
	}
	copy(uuid[:], b)
	return uuid, nil
}

// SuccessPayload builds Login Success: UUID + name + an empty properties
// array (the count is required online and offline).
func SuccessPayload(uuid [16]byte, name string) []byte {
	payload := make([]byte, 0, 32+len(name))
	payload = append(payload, uuid[:]...)
	payload = append(payload, protocol.WriteString(name)...)
	payload = append(payload, protocol.WriteVarInt32(0)...) // properties count = 0
	return payload
}

// DisconnectPayload builds Login Disconnect with text as the reason (a
// plain chat component; the text is JSON-escaped).
func DisconnectPayload(text string) []byte {
	encoded, _ := json.Marshal(map[string]string{"text": text})
	return protocol.WriteString(string(encoded))
}

// The Login Disconnect reasons.

// IPBanned: the auth plugin's failure tracking banned the address.
func IPBanned(until time.Time) string {
	return "IP banned (auth failures) until " + until.Format("2006-01-02 15:04:05")
}

// Banned: the player is on the ban list.
func Banned(reason string, expires time.Time) string {
	return "You are banned from this server.\nReason: " + reason + ". Expires: " + expires.Format(time.RFC1123)
}

// ServerFull: the player cap is reached.
func ServerFull(online, max int) string {
	return fmt.Sprintf("Server is full (%d/%d)", online, max)
}

// AuthFailed: Mojang didn't verify the session.
func AuthFailed(err error) string { return "Authentication failed: " + err.Error() }

// InvalidName: the Login Start name fails ValidName.
const InvalidName = "Invalid username"

// MaxNameLength is the longest username vanilla accepts.
const MaxNameLength = 16

// ValidName is vanilla 1.20.1's StringUtil.isValidPlayerName plus the
// non-empty check: 1..16 characters, none of them a control character,
// space or beyond ASCII. Longer names used to reach protocol.WriteString in
// the play packets (a panic past 32767 bytes) or break every client's tab
// list.
func ValidName(name string) bool {
	if name == "" || len(name) > MaxNameLength {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] <= ' ' || name[i] >= 0x7F {
			return false
		}
	}
	return true
}
