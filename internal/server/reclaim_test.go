package server

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

// registerPersistent registers a persistent guest and returns the resume
// secret the server issued it (767).
func registerPersistent(t *testing.T, conn net.Conn, reader *bufio.Reader, nick string) string {
	t.Helper()
	io.WriteString(conn, "NICK "+nick+"\r\nUSER u 0 * :u\r\n")
	line := expectLine(t, reader, conn, " 767 ")
	fields := strings.Fields(line)
	if len(fields) < 4 || !protocol.ValidMessageID(fields[3]) {
		t.Fatalf("767 did not carry a secret: %q", line)
	}
	expectLine(t, reader, conn, " 001 ")
	return fields[3]
}

// quiet checks that the server sends conn nothing before answering a PING:
// no QUIT, JOIN or presence notice reached it.
func quiet(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	io.WriteString(conn, "PING :quiet\r\n")
	if line := expectLine(t, reader, conn, " "); !strings.Contains(line, "PONG") {
		t.Fatalf("unexpected line before the PONG: %q", line)
	}
}

func TestGuestReclaimsAStaleSessionWithItsSecret(t *testing.T) {
	address := rawServer(t)
	stale, staleReader := rawConn(t, address)
	secret := registerPersistent(t, stale, staleReader, "bot")
	io.WriteString(stale, "JOIN #room\r\nAWAY :busy\r\n")
	expectLine(t, staleReader, stale, " 306 ")

	member, memberReader := rawConn(t, address)
	io.WriteString(member, "NICK member\r\nUSER u 0 * :u\r\nJOIN #room\r\nMONITOR + bot\r\n")
	expectLine(t, memberReader, member, " 730 ")

	// A wrong secret, and an ephemeral session, are refused as before.
	wrong, wrongReader := rawConn(t, address)
	io.WriteString(wrong, "RESUME bot "+strings.Repeat("0", 32)+"\r\nNICK bot\r\nUSER u 0 * :u\r\n")
	expectLine(t, wrongReader, wrong, " 433 ")
	ephemeral, ephemeralReader := rawConn(t, address)
	io.WriteString(ephemeral, "EPHEMERAL\r\nRESUME bot "+secret+"\r\nNICK bot\r\nUSER u 0 * :u\r\n")
	expectLine(t, ephemeralReader, ephemeral, " 433 ")

	// The right secret takes the session over: the nickname, its channel
	// and its away state, shown as if freshly joined.
	fresh, freshReader := rawConn(t, address)
	io.WriteString(fresh, "RESUME bot "+secret+"\r\n")
	renewed := registerPersistent(t, fresh, freshReader, "bot")
	if renewed == secret {
		t.Fatal("the secret was not rotated by the reclaim")
	}
	expectLine(t, freshReader, fresh, ":bot!u@localhost JOIN #room")
	expectLine(t, freshReader, fresh, " 353 ")
	lines := readUntilClosed(t, stale, staleReader)
	if !strings.Contains(strings.Join(lines, ""), "ERROR :Closing link: session reclaimed") {
		t.Fatalf("the stale session was not told why it closed: %q", lines)
	}

	// The room and the monitor saw neither a departure nor an arrival.
	quiet(t, member, memberReader)
	io.WriteString(member, "PRIVMSG #room :still here?\r\nWHOIS bot\r\n")
	expectLine(t, freshReader, fresh, "PRIVMSG #room :still here?")
	expectLine(t, memberReader, member, " 301 ")

	// The old secret is spent: it cannot take the new session over.
	again, againReader := rawConn(t, address)
	io.WriteString(again, "RESUME bot "+secret+"\r\nNICK bot\r\nUSER u 0 * :u\r\n")
	expectLine(t, againReader, again, " 433 ")
	quiet(t, fresh, freshReader)
}

func TestAccountReclaimsItsOwnStaleSession(t *testing.T) {
	address, _ := collaborationServer(t, t.TempDir())
	createWireAccount(t, address, "alice")
	stale, staleReader := rawConn(t, address)
	io.WriteString(stale, "AUTH alice :"+testAdminToken+"\r\n")
	registerPersistent(t, stale, staleReader, "alice")
	io.WriteString(stale, "JOIN #room\r\n")
	expectLine(t, staleReader, stale, "JOIN #room")

	// Another account cannot; nor can the account on another nickname.
	createWireAccount(t, address, "mallory")
	other, otherReader := rawConn(t, address)
	io.WriteString(other, "AUTH mallory :"+testAdminToken+"\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, otherReader, other, " 498 ")

	// The same account, authenticated before NICK, takes it over.
	fresh, freshReader := rawConn(t, address)
	io.WriteString(fresh, "AUTH alice :"+testAdminToken+"\r\n")
	registerPersistent(t, fresh, freshReader, "alice")
	expectLine(t, freshReader, fresh, ":alice!u@localhost JOIN #room")
	readUntilClosed(t, stale, staleReader)

	// A standard SASL client sends NICK during CAP negotiation, before it
	// has authenticated: the held nickname is decided at CAP END.
	sasl, saslReader := rawConn(t, address)
	io.WriteString(sasl, "CAP LS 302\r\nCAP REQ :sasl\r\nNICK alice\r\nUSER u 0 * :u\r\nAUTHENTICATE PLAIN\r\n")
	expectLine(t, saslReader, sasl, "AUTHENTICATE +")
	payload := base64.StdEncoding.EncodeToString([]byte("\x00alice\x00" + testAdminToken))
	io.WriteString(sasl, "AUTHENTICATE "+payload+"\r\n")
	expectLine(t, saslReader, sasl, " 903 ")
	io.WriteString(sasl, "CAP END\r\n")
	expectLine(t, saslReader, sasl, " 001 ")
	expectLine(t, saslReader, sasl, ":alice!u@localhost JOIN #room")
	readUntilClosed(t, fresh, freshReader)

	// Without the credential, the deferred refusal is the usual 433.
	guest, guestReader := rawConn(t, address)
	io.WriteString(guest, "CAP LS 302\r\nNICK alice\r\nUSER u 0 * :u\r\nCAP END\r\n")
	expectLine(t, guestReader, guest, " 433 ")
}

func TestObserverSessionIsReclaimedAsAnObserver(t *testing.T) {
	address := rawServer(t)
	stale, staleReader := rawConn(t, address)
	secret := registerPersistent(t, stale, staleReader, "reader")
	io.WriteString(stale, "OBSERVE #logs\r\n")
	expectLine(t, staleReader, stale, " 765 ")
	fresh, freshReader := rawConn(t, address)
	io.WriteString(fresh, "RESUME reader "+secret+"\r\n")
	registerPersistent(t, fresh, freshReader, "reader")
	expectLine(t, freshReader, fresh, " 765 ")
	readUntilClosed(t, stale, staleReader)
	// Still an observer: it receives the room and cannot post.
	poster, posterReader := rawConn(t, address)
	io.WriteString(poster, "EPHEMERAL\r\nNICK poster\r\nUSER u 0 * :u\r\nPRIVMSG #logs :line\r\n")
	expectLine(t, posterReader, poster, " 762 ")
	expectLine(t, freshReader, fresh, "PRIVMSG #logs :line")
	io.WriteString(fresh, "PRIVMSG #logs :reply\r\n")
	expectLine(t, freshReader, fresh, " 484 ")
}

func TestResumeArgumentsAndTiming(t *testing.T) {
	address := rawServer(t)
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "RESUME\r\n")
	expectLine(t, reader, conn, " 461 ")
	io.WriteString(conn, "RESUME bot nothex\r\n")
	expectLine(t, reader, conn, " 461 ")
	// A secret for a nickname nobody holds is simply unused.
	io.WriteString(conn, "RESUME bot "+strings.Repeat("0", 32)+"\r\n")
	registerPersistent(t, conn, reader, "bot")
	io.WriteString(conn, "RESUME bot "+strings.Repeat("0", 32)+"\r\n")
	expectLine(t, reader, conn, " 462 ")
	// A registered session cannot take a held nickname by renaming.
	other, otherReader := rawConn(t, address)
	registerPersistent(t, other, otherReader, "other")
	io.WriteString(other, "NICK bot\r\n")
	expectLine(t, otherReader, other, " 433 ")
}
