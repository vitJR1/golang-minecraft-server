package encryption

import (
	"crypto/cipher"
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
	buf := make([]byte, len(p))
	e.encrypt.XORKeyStream(buf, p)
	return e.Conn.Write(buf)
}
