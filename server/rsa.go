package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
)

const serverID = "qcjn223_1232ty_my_goland_server_id" // Minecraft server ID (can be empty for offline mode)

type EncryptionRequest struct {
	ServerID  string
	PublicKey []byte
	Nonce     []byte
	privKey   *rsa.PrivateKey
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

func NewEncryptionRequest() (*EncryptionRequest, *rsa.PrivateKey, error) {
	pubKey, privKey, err := generateRSA()
	if err != nil {
		return nil, nil, err
	}

	// Генерируем случайный nonce (16 байт)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generating nonce: %w", err)
	}

	return &EncryptionRequest{
		ServerID:  serverID,
		PublicKey: pubKey,
		Nonce:     nonce,
	}, privKey, nil
}
