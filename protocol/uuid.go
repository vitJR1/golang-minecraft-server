package protocol

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidUUID = errors.New("invalid UUID")

// WriteUUID converts a UUID string (with or without hyphens) to its 16-byte
// big-endian binary form, which is how UUIDs go on the Minecraft wire.
func WriteUUID(s string) ([]byte, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 36 {
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return nil, ErrInvalidUUID
		}
		s = s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	} else if len(s) != 32 {
		return nil, ErrInvalidUUID
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return nil, ErrInvalidUUID
	}
	return raw, nil
}

// FormatUUID inserts hyphens into a 32-char hex UUID (the form Mojang's API
// returns).
func FormatUUID(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 32 {
		return "", errors.New("invalid UUID length")
	}
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32], nil
}

// OfflineUUID derives the cracked-mode UUID for a player name. Vanilla uses
// MD5("OfflinePlayer:" + name) with v3 UUID variant/version bits.
func OfflineUUID(name string) string {
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	b := sum[:]
	b[6] = (b[6] & 0x0f) | 0x30
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}
