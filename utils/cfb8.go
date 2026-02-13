package utils

import (
	"crypto/cipher"
)

type cfb8 struct {
	block   cipher.Block
	iv      []byte
	decrypt bool
}

func newCFB8(block cipher.Block, iv []byte, decrypt bool) cipher.Stream {
	ivCopy := make([]byte, len(iv))
	copy(ivCopy, iv)

	return &cfb8{
		block:   block,
		iv:      ivCopy,
		decrypt: decrypt,
	}
}

func (c *cfb8) XORKeyStream(dst, src []byte) {
	for i := 0; i < len(src); i++ {
		// encrypt IV
		encrypted := make([]byte, len(c.iv))
		c.block.Encrypt(encrypted, c.iv)

		b := src[i] ^ encrypted[0]
		dst[i] = b

		// shift IV
		copy(c.iv, c.iv[1:])

		if c.decrypt {
			c.iv[len(c.iv)-1] = src[i]
		} else {
			c.iv[len(c.iv)-1] = b
		}
	}
}
