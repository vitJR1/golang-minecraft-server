package encryption

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"testing"
)

func newCFB8Pair(t *testing.T) (enc, dec *CFB8) {
	t.Helper()
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	// Minecraft convention: the shared secret doubles as both key and IV.
	return NewCFB8Encrypt(block, key), NewCFB8Decrypt(block, key)
}

func TestCFB8RoundTrip(t *testing.T) {
	enc, dec := newCFB8Pair(t)

	plaintext := []byte("Hello, encrypted Minecraft world!")
	ciphertext := make([]byte, len(plaintext))
	enc.XORKeyStream(ciphertext, plaintext)

	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext equals plaintext — encryption is a no-op")
	}

	decoded := make([]byte, len(plaintext))
	dec.XORKeyStream(decoded, ciphertext)

	if !bytes.Equal(decoded, plaintext) {
		t.Errorf("plaintext=%q, decoded=%q", plaintext, decoded)
	}
}

func TestCFB8VariousSizes(t *testing.T) {
	// 1 byte exercises the per-byte loop; 16/17 straddle the AES block size;
	// 4096/4097 straddle the encryptedConn's internal chunk; 33 hits the ring
	// buffer rollover. If any size desyncs, decryption garbles bytes past the
	// faulty point.
	for _, size := range []int{1, 7, 15, 16, 17, 32, 33, 4095, 4096, 4097, 10000} {
		t.Run("", func(t *testing.T) {
			enc, dec := newCFB8Pair(t)

			plaintext := make([]byte, size)
			if _, err := rand.Read(plaintext); err != nil {
				t.Fatal(err)
			}

			ciphertext := make([]byte, size)
			enc.XORKeyStream(ciphertext, plaintext)

			decoded := make([]byte, size)
			dec.XORKeyStream(decoded, ciphertext)

			if !bytes.Equal(decoded, plaintext) {
				t.Errorf("size=%d: round-trip mismatch", size)
			}
		})
	}
}

func TestCFB8ChunkedMatchesSinglePass(t *testing.T) {
	// Crucial property: encrypting N bytes in one call must produce the same
	// ciphertext as encrypting them in many smaller calls. The encryptedConn
	// wrapper splits writes into 4 KiB chunks; if cipher state advanced
	// differently in the chunked path, the wire stream would desync.
	plaintext := make([]byte, 5000)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}

	for _, chunkSize := range []int{1, 7, 17, 64, 1024, 4096} {
		t.Run("", func(t *testing.T) {
			encAll, _ := newCFB8Pair(t)
			cipherAll := make([]byte, len(plaintext))
			encAll.XORKeyStream(cipherAll, plaintext)

			// Same key/IV: rebuild a fresh encrypter and feed chunks.
			encChunked, _ := newCFB8Pair(t)
			// newCFB8Pair generates fresh randomness, so override with the same
			// key the all-at-once side used. Easier: just rebuild with that key.
			_ = encChunked

			// Instead, re-roll both with the same fixed key to compare paths.
			key := make([]byte, 16)
			rand.Read(key)
			block, _ := aes.NewCipher(key)

			encA := NewCFB8Encrypt(block, key)
			cA := make([]byte, len(plaintext))
			encA.XORKeyStream(cA, plaintext)

			encB := NewCFB8Encrypt(block, key)
			cB := make([]byte, len(plaintext))
			for i := 0; i < len(plaintext); i += chunkSize {
				end := i + chunkSize
				if end > len(plaintext) {
					end = len(plaintext)
				}
				encB.XORKeyStream(cB[i:end], plaintext[i:end])
			}

			if !bytes.Equal(cA, cB) {
				t.Errorf("chunkSize=%d: chunked output differs from single-pass at first mismatch",
					chunkSize)
			}
		})
	}
}

func TestCFB8OutputDiffersFromInput(t *testing.T) {
	// Sanity check that encryption actually scrambles bytes — guards against
	// somebody accidentally turning XORKeyStream into a copy.
	enc, _ := newCFB8Pair(t)
	plaintext := bytes.Repeat([]byte{0x00}, 64)
	ciphertext := make([]byte, 64)
	enc.XORKeyStream(ciphertext, plaintext)

	zeroes := 0
	for _, b := range ciphertext {
		if b == 0 {
			zeroes++
		}
	}
	if zeroes > 8 {
		// Statistically vanishing for random AES output; if we see this,
		// XORKeyStream is probably broken.
		t.Errorf("ciphertext of all-zeros plaintext has %d zero bytes (suspiciously many)", zeroes)
	}
}
