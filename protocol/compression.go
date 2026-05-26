package protocol

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// CompressPayload zlib-compresses an uncompressed packet body (the bytes of
// VarInt(packetID) + payload).
func CompressPayload(uncompressed []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(uncompressed); err != nil {
		return nil, fmt.Errorf("zlib write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("zlib close: %w", err)
	}
	return buf.Bytes(), nil
}

// DecompressPayload zlib-decompresses to a packet body of expectedSize bytes.
// Mismatch with the declared size is treated as corruption rather than silently
// returning fewer bytes.
func DecompressPayload(compressed []byte, expectedSize int) ([]byte, error) {
	if expectedSize < 0 {
		return nil, errors.New("negative expected size")
	}
	r, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("zlib reader: %w", err)
	}
	defer r.Close()

	out := make([]byte, expectedSize)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("zlib decompress: %w", err)
	}
	// Reader must be exhausted: trailing bytes mean expectedSize was wrong.
	tail := make([]byte, 1)
	if n, _ := r.Read(tail); n > 0 {
		return nil, fmt.Errorf("zlib payload larger than declared %d bytes", expectedSize)
	}
	return out, nil
}
