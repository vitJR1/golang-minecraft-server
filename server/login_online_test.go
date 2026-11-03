package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"minecraft-server/cfg"
	"minecraft-server/encryption"
	"minecraft-server/mojang"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/servertest"
)

// withOnlineMode turns online mode on for one test with the session server
// answered by h, and shortens the login deadline.
func withOnlineMode(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	origMode, origURL, origTimeout := cfg.OnlineMode, mojang.SessionServer, loginTimeout
	cfg.OnlineMode, mojang.SessionServer, loginTimeout = true, srv.URL, 2*time.Second
	t.Cleanup(func() { cfg.OnlineMode, mojang.SessionServer, loginTimeout = origMode, origURL, origTimeout })
}

// profileJSON answers hasJoined with name's profile.
func profileJSON(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"069a79f444e94726a5befca90e38aaf5","name":"` + name + `"}`))
	}
}

// startOnline sends Handshake + Login Start and reads the Encryption Request
// (plaintext); returns its verify token.
func startOnline(t *testing.T, cli *servertest.Client, name string) []byte {
	t.Helper()
	cli.Write(t, packetid.SbHandshake, servertest.Handshake(763, "localhost", 25565, 2))
	cli.Write(t, packetid.SbLoginStart, protocol.WriteString(name))
	id, body := cli.Read(t)
	if id != packetid.CbLoginEncRequest {
		t.Fatalf("got 0x%02X, want Encryption Request", id)
	}
	if _, err := protocol.ReadStringFromBuf(body); err != nil { // server id
		t.Fatal(err)
	}
	if _, err := protocol.ReadByteArrayFromBuf(body); err != nil { // public key
		t.Fatal(err)
	}
	token, err := protocol.ReadByteArrayFromBuf(body)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// finishOnline answers the Encryption Request like a vanilla client and
// returns a client reading/writing through the AES stream from then on.
func finishOnline(t *testing.T, cli *servertest.Client, token []byte) *servertest.Client {
	t.Helper()
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	encS, err := rsa.EncryptPKCS1v15(rand.Reader, &keys.Private.PublicKey, secret)
	if err != nil {
		t.Fatal(err)
	}
	encT, err := rsa.EncryptPKCS1v15(rand.Reader, &keys.Private.PublicKey, token)
	if err != nil {
		t.Fatal(err)
	}
	var resp bytes.Buffer
	resp.Write(protocol.WriteVarInt32(int32(len(encS))))
	resp.Write(encS)
	resp.Write(protocol.WriteVarInt32(int32(len(encT))))
	resp.Write(encT)
	cli.Write(t, packetid.SbLoginEncResponse, resp.Bytes())

	block, err := aes.NewCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := encryption.WrapEncryptedConn(cli.Conn,
		encryption.NewCFB8Encrypt(block, secret), encryption.NewCFB8Decrypt(block, secret))
	return &servertest.Client{Conn: wrapped, Threshold: protocol.CompressionDisabled}
}

// readDisconnect expects a Login Disconnect and returns its text.
func readDisconnect(t *testing.T, cli *servertest.Client) string {
	t.Helper()
	id, body := cli.Read(t)
	if id != packetid.CbLoginDisconnect {
		t.Fatalf("got 0x%02X, want Login Disconnect", id)
	}
	text, err := protocol.ReadStringFromBuf(body)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// expectClosed waits for the server to drop the connection.
func expectClosed(t *testing.T, cli *servertest.Client, within time.Duration) {
	t.Helper()
	_ = cli.Conn.SetReadDeadline(time.Now().Add(within))
	if _, err := io.Copy(io.Discard, cli.Conn); err != nil {
		t.Fatalf("connection still open after %v: %v", within, err)
	}
}

// TestOnlineLoginSucceeds: the whole encrypted handshake against a stub
// session server; Login Success carries the verified profile's name.
func TestOnlineLoginSucceeds(t *testing.T) {
	withOnlineMode(t, profileJSON("Notch"))
	s := New()
	cli := servertest.Dial(t, s)
	enc := finishOnline(t, cli, startOnline(t, cli, "notch"))
	id, body := enc.Read(t)
	if id != packetid.CbLoginSetCompr {
		t.Fatalf("got 0x%02X, want Set Compression", id)
	}
	thr, _ := protocol.ReadVarInt(body)
	enc.Threshold = thr
	id, body = enc.Read(t)
	if id != packetid.CbLoginSuccess {
		t.Fatalf("got 0x%02X, want Login Success", id)
	}
	body.Next(16) // uuid
	if name, _ := protocol.ReadStringFromBuf(body); name != "Notch" {
		t.Errorf("Login Success name %q, want the profile's", name)
	}
	enc.StartDiscardDrain()
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("Notch")
		return ok
	}, "Notch to join")
}

// TestOnlineBanUsesVerifiedName: the ban list is checked against the name
// Mojang verified, not the Login Start one the client chose.
func TestOnlineBanUsesVerifiedName(t *testing.T) {
	withOnlineMode(t, profileJSON("Griefer"))
	s := New()
	if err := s.Bans.Add(context.Background(), "Griefer", "griefing", "test", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cli := servertest.Dial(t, s)
	enc := finishOnline(t, cli, startOnline(t, cli, "SomeoneElse"))
	if text := readDisconnect(t, enc); !strings.Contains(text, "banned") {
		t.Errorf("disconnect %q, want the ban message", text)
	}
}

// TestOnlineMojangFailureDisconnectEncrypted: the client encrypts from its
// Encryption Response on, so the "Authentication failed" disconnect has to
// be encrypted too — it used to go out in plaintext and the client saw a
// decoder error instead of the reason.
func TestOnlineMojangFailureDisconnectEncrypted(t *testing.T) {
	withOnlineMode(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s := New()
	cli := servertest.Dial(t, s)
	enc := finishOnline(t, cli, startOnline(t, cli, "Notch"))
	if text := readDisconnect(t, enc); !strings.Contains(text, "Authentication failed") {
		t.Errorf("disconnect %q", text)
	}
}

// TestOnlineLoginStallTimesOut: a client that never answers the Encryption
// Request used to hold its connection slot forever (the read had no
// deadline); the login deadline closes it.
func TestOnlineLoginStallTimesOut(t *testing.T) {
	withOnlineMode(t, profileJSON("Notch"))
	loginTimeout = 200 * time.Millisecond
	s := New()
	cli := servertest.Dial(t, s)
	startOnline(t, cli, "Notch")
	expectClosed(t, cli, 2*time.Second)
}

// TestOnlineMojangHangTimesOut: a session server that never answers can't
// hold the login past its deadline either.
func TestOnlineMojangHangTimesOut(t *testing.T) {
	release := make(chan struct{})
	withOnlineMode(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	loginTimeout = 300 * time.Millisecond
	s := New()
	cli := servertest.Dial(t, s)
	enc := finishOnline(t, cli, startOnline(t, cli, "Notch"))
	if text := readDisconnect(t, enc); !strings.Contains(text, "Authentication failed") {
		t.Errorf("disconnect %q", text)
	}
}

// TestUnexpectedLoginPacketDisconnects: a login-state packet other than
// Login Start used to be ignored, so a client could keep its slot by
// sending junk every 29 s; now it is dropped.
func TestUnexpectedLoginPacketDisconnects(t *testing.T) {
	s := New()
	cli := servertest.Dial(t, s)
	cli.Write(t, packetid.SbHandshake, servertest.Handshake(763, "localhost", 25565, 2))
	cli.Write(t, 0x02, []byte{0, 0}) // Login Plugin Response, never asked for
	expectClosed(t, cli, 2*time.Second)
}
