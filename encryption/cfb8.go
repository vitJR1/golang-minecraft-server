package encryption

import (
	"crypto/cipher"
)

type cfb8 struct {
	block   cipher.Block
	iv      []byte
	decrypt bool
}

func NewCFB8(block cipher.Block, iv []byte, decrypt bool) cipher.Stream {
	ivCopy := make([]byte, len(iv))
	copy(ivCopy, iv)

	return &cfb8{
		block:   block,
		iv:      ivCopy,
		decrypt: decrypt,
	}
}

func (c *cfb8) XORKeyStream(dst, src []byte) {
	if len(dst) < len(src) {
		panic("cfb8: dst too small")
	}

	encrypted := make([]byte, c.block.BlockSize())

	for i := 0; i < len(src); i++ {
		c.block.Encrypt(encrypted, c.iv)

		out := src[i] ^ encrypted[0]
		dst[i] = out

		copy(c.iv, c.iv[1:])
		if c.decrypt {
			c.iv[len(c.iv)-1] = src[i] // ciphertext byte
		} else {
			c.iv[len(c.iv)-1] = out // ciphertext byte
		}
	}
}
