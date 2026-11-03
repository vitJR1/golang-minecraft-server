package protocol

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"sync"
)

// zlibWriters recycles zlib writers: a fresh one allocates ~800 KB of
// deflate state, which made every compressed frame cost that much garbage.
// Reset re-targets a pooled writer at no allocation cost.
var zlibWriters = sync.Pool{New: func() any { return zlib.NewWriter(nil) }}

// CompressPayload zlib-compresses an uncompressed packet body (the bytes of
// VarInt(packetID) + payload).
func CompressPayload(uncompressed []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := compressTo(&buf, uncompressed); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compressTo appends the zlib stream of the concatenated parts to buf.
func compressTo(buf *bytes.Buffer, parts ...[]byte) error {
	w := zlibWriters.Get().(*zlib.Writer)
	defer zlibWriters.Put(w)
	w.Reset(buf)
	for _, p := range parts {
		if _, err := w.Write(p); err != nil {
			return fmt.Errorf("zlib write: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("zlib close: %w", err)
	}
	return nil
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
