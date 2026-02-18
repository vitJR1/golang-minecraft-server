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
	// шифруем кусками и гарантируем запись всех байт,
	// иначе состояние stream уедет.
	total := 0
	tmp := make([]byte, 4096)

	for total < len(p) {
		chunk := len(p) - total
		if chunk > len(tmp) {
			chunk = len(tmp)
		}

		// encrypt chunk -> tmp[:chunk]
		e.encrypt.XORKeyStream(tmp[:chunk], p[total:total+chunk])

		// writeAll for this encrypted chunk
		written := 0
		for written < chunk {
			n, err := e.Conn.Write(tmp[written:chunk])
			if err != nil {
				return total + written, err
			}
			if n == 0 {
				return total + written, io.ErrUnexpectedEOF
			}
			written += n
		}

		total += chunk
	}

	return total, nil
}

//func (e *encryptedConn) Write(p []byte) (int, error) {
//	buf := make([]byte, len(p))
//	e.encrypt.XORKeyStream(buf, p)
//
//	n, err := e.Conn.Write(buf)
//	if err != nil {
//		return n, err
//	}
//	if n != len(buf) {
//		// Это ПЛОХО: stream продвинулся, а байты не ушли.
//		return n, fmt.Errorf("PARTIAL WRITE: wrote %d of %d (encryption stream would desync)", n, len(buf))
//	}
//	return n, nil
//}
