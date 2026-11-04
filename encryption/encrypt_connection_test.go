package encryption

import (
	"bytes"
	"crypto/aes"
	"io"
	"net"
	"testing"
)

// TestWriteVectoredMatchesStream: a batch written in one call is the same
// ciphertext as the frames written one by one, and the cipher stays in step
// across batches (the client decrypts it as one stream).
func TestWriteVectoredMatchesStream(t *testing.T) {
	key := []byte("0123456789abcdef")
	block, _ := aes.NewCipher(key)
	cli, srv := net.Pipe()
	defer cli.Close()
	enc := WrapEncryptedConn(srv, NewCFB8Encrypt(block, key), NewCFB8Decrypt(block, key)).(*encryptedConn)

	batches := [][][]byte{
		{[]byte("first frame"), bytes.Repeat([]byte{7}, 5000), []byte("x")},
		{bytes.Repeat([]byte{1, 2, 3}, 400<<10)}, // bigger than maxKeptOut
		{[]byte("after the big one")},
	}
	var plain []byte
	for _, b := range batches {
		for _, p := range b {
			plain = append(plain, p...)
		}
	}
	go func() {
		for _, b := range batches {
			if _, err := enc.WriteVectored(b); err != nil {
				t.Error(err)
			}
		}
		_ = srv.Close()
	}()
	got, err := io.ReadAll(cli)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, len(plain))
	NewCFB8Encrypt(block, key).XORKeyStream(want, plain)
	if !bytes.Equal(got, want) {
		t.Fatal("batched ciphertext differs from the one-stream encryption")
	}
}
