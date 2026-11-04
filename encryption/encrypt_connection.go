package encryption

import (
	"crypto/cipher"
	"io"
	"net"
)

type encryptedConn struct {
	net.Conn
	encrypt cipher.Stream
	decrypt cipher.Stream

	out []byte // шифротекст для записи; пишет только писатель соединения
}

func WrapEncryptedConn(conn net.Conn, encrypt, decrypt cipher.Stream) net.Conn {
	return &encryptedConn{
		Conn:    conn,
		encrypt: encrypt,
		decrypt: decrypt,
	}
}

func (e *encryptedConn) Read(p []byte) (int, error) {
	n, err := e.Conn.Read(p)
	if n > 0 {
		e.decrypt.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (e *encryptedConn) Write(p []byte) (int, error) {
	n, err := e.WriteVectored([][]byte{p})
	return int(n), err
}

// maxKeptOut bounds the encryption buffer kept between writes: a burst (a
// join's chunk stream) may grow it, but it isn't held on to after.
const maxKeptOut = 256 << 10

// WriteVectored шифрует все буферы подряд в один (переиспользуемый) буфер и
// пишет его целиком одним вызовом — так очередь соединения (server/net/conn)
// отправляет пачку кадров одной записью и через шифрование, как writev без
// него. Запись всегда полная (цикл до последнего байта), иначе состояние
// stream уедет: шифр уже продвинут на все байты.
func (e *encryptedConn) WriteVectored(bufs [][]byte) (int64, error) {
	total := 0
	for _, b := range bufs {
		total += len(b)
	}
	if cap(e.out) < total {
		e.out = make([]byte, total)
	}
	out := e.out[:total]
	off := 0
	for _, b := range bufs {
		e.encrypt.XORKeyStream(out[off:off+len(b)], b)
		off += len(b)
	}
	if cap(e.out) > maxKeptOut {
		e.out = nil
	}

	written := 0
	for written < total {
		n, err := e.Conn.Write(out[written:])
		if err != nil {
			return int64(written), err
		}
		if n == 0 {
			return int64(written), io.ErrUnexpectedEOF
		}
		written += n
	}
	return int64(total), nil
}
