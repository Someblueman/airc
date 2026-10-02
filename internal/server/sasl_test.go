package server

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func collaborationServer(t *testing.T, dir string) (string, *Server) {
	t.Helper()
	s := New(Config{HistoryLimit: 64, PingInterval: time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for _, err := range []error{s.EnableAdmin(testAdminToken), s.RestoreAccounts(filepath.Join(dir, "accounts")), s.RestoreChat(filepath.Join(dir, "chat")), s.RestoreHistory(filepath.Join(dir, "history"))} {
		if err != nil {
			t.Fatal(err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return l.Addr().String(), s
}

func createWireAccount(t *testing.T, address, nick string) {
	t.Helper()
	c, r := rawConn(t, address)
	io.WriteString(c, "REGISTER "+nick+" :"+testAdminToken+"\r\n")
	expectLine(t, r, c, " 779 ")
	c.Close()
}

func TestSASLStandardLoginAndReservation(t *testing.T) {
	address, _ := collaborationServer(t, t.TempDir())
	createWireAccount(t, address, "alice")
	c, r := rawConn(t, address)
	io.WriteString(c, "CAP LS 302\r\nCAP REQ :sasl\r\nNICK alice\r\nUSER u 0 * :Alice\r\nAUTHENTICATE PLAIN\r\n")
	expectLine(t, r, c, "sasl=PLAIN")
	expectLine(t, r, c, " ACK :sasl")
	expectLine(t, r, c, "AUTHENTICATE +")
	payload := base64.StdEncoding.EncodeToString([]byte("\x00alice\x00" + testAdminToken))
	io.WriteString(c, "AUTHENTICATE "+payload+"\r\n")
	expectLine(t, r, c, " 903 ")
	io.WriteString(c, "CAP END\r\n")
	expectLine(t, r, c, " 001 ")
	io.WriteString(c, "AUTHENTICATE PLAIN\r\n")
	expectLine(t, r, c, " 907 ")
	guest, gr := rawConn(t, address)
	io.WriteString(guest, "CAP REQ :sasl\r\nNICK alice\r\nUSER u 0 * :u\r\nCAP END\r\n")
	expectLine(t, gr, guest, " 433 ")
	c.Close()
}

func TestSASLFailuresAbortAndBounds(t *testing.T) {
	address, _ := collaborationServer(t, t.TempDir())
	createWireAccount(t, address, "alice")
	c, r := rawConn(t, address)
	io.WriteString(c, "CAP REQ :sasl\r\nNICK alice\r\nUSER u 0 * :u\r\nAUTHENTICATE PLAIN\r\nAUTHENTICATE "+base64.StdEncoding.EncodeToString([]byte("\x00alice\x00bad"))+"\r\n")
	expectLine(t, r, c, " 904 ")
	io.WriteString(c, "CAP END\r\n")
	expectLine(t, r, c, " 498 ") // no unauthenticated welcome for reserved nick
	io.WriteString(c, "AUTHENTICATE PLAIN\r\nAUTHENTICATE *\r\n")
	expectLine(t, r, c, " 906 ")
	io.WriteString(c, "AUTHENTICATE PLAIN\r\nAUTHENTICATE "+strings.Repeat("A", 401)+"\r\n")
	expectLine(t, r, c, " 905 ")
	io.WriteString(c, "CAP REQ :sasl unsupported\r\n")
	expectLine(t, r, c, " NAK :")
	io.WriteString(c, "AUTHENTICATE PLAIN\r\nAUTHENTICATE %%%\r\n")
	expectLine(t, r, c, " 904 ")
	// A standard 400-byte chunk must wait for the explicit terminator.
	io.WriteString(c, "AUTHENTICATE PLAIN\r\nAUTHENTICATE "+strings.Repeat("A", 400)+"\r\nAUTHENTICATE +\r\n")
	expectLine(t, r, c, " 904 ")
}

func TestSASLUnfinishedLoginReceivesNoLiveTraffic(t *testing.T) {
	address, _ := collaborationServer(t, t.TempDir())
	createWireAccount(t, address, "alice")
	pending, r := rawConn(t, address)
	io.WriteString(pending, "CAP REQ :sasl\r\nNICK alice\r\nUSER u 0 * :u\r\nPING :pending\r\n")
	expectLine(t, r, pending, "PONG server :pending")
	writer, wr := wireRegister(t, address, "writer", true)
	io.WriteString(writer, "PRIVMSG alice :no live delivery before login\r\n")
	expectLine(t, wr, writer, " queued :")
	io.WriteString(pending, "PING :barrier\r\n")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "PRIVMSG") {
			t.Fatal("unauthenticated session received a message", line)
		}
		if strings.Contains(line, "PONG server :barrier") {
			break
		}
	}
	pending.Close()
	// Cleanup must release the incomplete nickname claim.
	s, sr := rawConn(t, address)
	io.WriteString(s, "AUTH alice :"+testAdminToken+"\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, sr, s, " 001 ")
}
