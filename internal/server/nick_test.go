package server

import (
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestPreRegistrationNickChangeReleasesEarlierClaim(t *testing.T) {
	address := rawServer(t)
	first, firstReader := rawConn(t, address)
	io.WriteString(first, "NICK aaa\r\nNICK bbb\r\nUSER u 0 * :u\r\n")
	expectLine(t, firstReader, first, " 001 ")

	// The abandoned nickname must be free again while the client is still connected.
	second, secondReader := rawConn(t, address)
	io.WriteString(second, "NICK aaa\r\nUSER u 0 * :u\r\n")
	expectLine(t, secondReader, second, " 001 ")
}

func TestEphemeralSessionDoesNotClaimOrReleaseNick(t *testing.T) {
	address := rawServer(t)
	live, liveReader := rawConn(t, address)
	io.WriteString(live, "NICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, liveReader, live, " 001 ")

	one, oneReader := rawConn(t, address)
	io.WriteString(one, "EPHEMERAL\r\nNICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, oneReader, one, " 766 ")
	expectLine(t, oneReader, one, " 001 ")
	io.WriteString(one, "QUIT\r\n")
	if _, err := oneReader.ReadString('\n'); err != nil && err != io.EOF {
		t.Fatal(err)
	}

	// The live session still owns the nickname after the one-shot session left.
	intruder, intruderReader := rawConn(t, address)
	io.WriteString(intruder, "NICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, intruderReader, intruder, " 433 ")

	// And it still receives direct messages.
	sender, senderReader := rawConn(t, address)
	io.WriteString(sender, "NICK sender\r\nUSER u 0 * :u\r\nPRIVMSG agent :hello\r\n")
	expectLine(t, senderReader, sender, " 762 ")
	expectLine(t, liveReader, live, "PRIVMSG agent :hello")
}

func TestEphemeralMustBeRequestedBeforeRegistration(t *testing.T) {
	address := rawServer(t)
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "NICK late\r\nUSER u 0 * :u\r\n")
	expectLine(t, reader, conn, " 001 ")
	io.WriteString(conn, "EPHEMERAL\r\n")
	expectLine(t, reader, conn, " 462 ")
}

func TestMultilineTagValidation(t *testing.T) {
	address := rawServer(t)
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "EPHEMERAL\r\nNICK tagger\r\nUSER u 0 * :u\r\n")
	line := expectLine(t, reader, conn, " 005 ")
	if !strings.Contains(line, "MULTILINE=1") {
		t.Fatalf("capability line = %q", line)
	}
	expectLine(t, reader, conn, " 001 ")

	// Each request must draw the specific numeric, skipping leftover registration lines.
	send := func(tag, preview, want string) {
		io.WriteString(conn, "@+airc/body="+tag+" PRIVMSG #c :"+preview+"\r\n")
		expectLine(t, reader, conn, want)
	}
	send("!!!", "x", " 417 ")
	send(protocol.EncodeBody(strings.Repeat("a\n", 2100)), "x", " 417 ")
	send(protocol.EncodeBody("\n \n"), "x", " 412 ")
	send(protocol.EncodeBody("ok\nfine"), "ignored preview", " 762 ")
}
