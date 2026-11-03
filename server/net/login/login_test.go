package login

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/server/net/packets"
)

func TestDecryptRoundTrip(t *testing.T) {
	k, err := NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef")
	token := []byte{1, 2, 3, 4}
	encS, _ := rsa.EncryptPKCS1v15(rand.Reader, &k.Private.PublicKey, secret)
	encT, _ := rsa.EncryptPKCS1v15(rand.Reader, &k.Private.PublicKey, token)
	got, err := k.Decrypt(packets.EncryptionResponse{SharedSecret: encS, VerifyToken: encT}, token)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Decrypt = %x %v", got, err)
	}
	if _, err := k.Decrypt(packets.EncryptionResponse{SharedSecret: encS, VerifyToken: encT}, []byte{9, 9, 9, 9}); err == nil {
		t.Error("a wrong verify token must fail")
	}
}

func TestEncryptionRequestPayload(t *testing.T) {
	p := bytes.NewBuffer(EncryptionRequestPayload("", []byte{0xAA, 0xBB}, []byte{1, 2, 3, 4}))
	id, _ := protocol.ReadStringFromBuf(p)
	pub, _ := protocol.ReadByteArrayFromBuf(p)
	tok, _ := protocol.ReadByteArrayFromBuf(p)
	if id != "" || !bytes.Equal(pub, []byte{0xAA, 0xBB}) || !bytes.Equal(tok, []byte{1, 2, 3, 4}) || p.Len() != 0 {
		t.Errorf("payload fields %q %x %x, %d trailing (no 1.20.4 boolean!)", id, pub, tok, p.Len())
	}
}

func TestSuccessPayload(t *testing.T) {
	uuid, err := OfflineUUID("Steve")
	if err != nil {
		t.Fatal(err)
	}
	p := bytes.NewBuffer(SuccessPayload(uuid, "Steve"))
	var got [16]byte
	copy(got[:], p.Next(16))
	name, _ := protocol.ReadStringFromBuf(p)
	props, _ := protocol.ReadVarInt(p)
	if got != uuid || name != "Steve" || props != 0 || p.Len() != 0 {
		t.Errorf("Login Success: %x %q props=%d", got, name, props)
	}
	if protocol.UUIDString(uuid) != protocol.OfflineUUID("Steve") {
		t.Errorf("offline uuid %s, want %s", protocol.UUIDString(uuid), protocol.OfflineUUID("Steve"))
	}
}

func TestProfileUUID(t *testing.T) {
	uuid, err := ProfileUUID("069a79f444e94726a5befca90e38aaf5")
	if err != nil {
		t.Fatal(err)
	}
	if protocol.UUIDString(uuid) != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Errorf("uuid %s", protocol.UUIDString(uuid))
	}
}

func TestDisconnectPayloadEscapes(t *testing.T) {
	text := Banned(`said "hi"`, time.Unix(0, 0).UTC())
	s, _ := protocol.ReadStringFromBuf(bytes.NewBuffer(DisconnectPayload(text)))
	var v map[string]string
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, s)
	}
	if v["text"] != text || !strings.Contains(text, "\nReason: said \"hi\".") {
		t.Errorf("text %q", v["text"])
	}
	if !strings.Contains(AuthFailed(errors.New("x")), "x") || ServerFull(3, 2) != "Server is full (3/2)" {
		t.Error("reason texts")
	}
}
