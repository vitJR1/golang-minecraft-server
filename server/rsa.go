package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
)

// NewEncryptionRequest generates a fresh 1024-bit RSA keypair and returns the
// DER-encoded public key (PKIX) plus the private key. The public key is what
// gets sent in the Encryption Request packet.
func NewEncryptionRequest() (publicKey []byte, privateKey *rsa.PrivateKey, err error) {
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
