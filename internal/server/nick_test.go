package server

import (
	"encoding/base64"
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
	// Ephemeral guests share a nickname nobody is connected with.
	one, oneReader := rawConn(t, address)
	io.WriteString(one, "EPHEMERAL\r\nNICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, oneReader, one, " 766 ")
	expectLine(t, oneReader, one, " 001 ")
	two, twoReader := rawConn(t, address)
	io.WriteString(two, "EPHEMERAL\r\nNICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, twoReader, two, " 001 ")

	// They do not claim it: a live client still can.
	live, liveReader := rawConn(t, address)
	io.WriteString(live, "NICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, liveReader, live, " 001 ")
	io.WriteString(one, "QUIT\r\n")
	if _, err := oneReader.ReadString('\n'); err != nil && err != io.EOF {
		t.Fatal(err)
	}

	// The live session still owns the nickname after the one-shot session left.
	intruder, intruderReader := rawConn(t, address)
	io.WriteString(intruder, "NICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, intruderReader, intruder, " 433 ")

	// A guest cannot speak as a nickname a connected client holds, even
	// ephemerally; it may pick another before registering.
	guest, guestReader := rawConn(t, address)
	io.WriteString(guest, "EPHEMERAL\r\nNICK agent\r\nUSER u 0 * :u\r\n")
	expectLine(t, guestReader, guest, " 433 ")
	io.WriteString(guest, "NICK other\r\n")
	expectLine(t, guestReader, guest, " 001 ")

	// And it still receives direct messages.
	sender, senderReader := rawConn(t, address)
	io.WriteString(sender, "NICK sender\r\nUSER u 0 * :u\r\nPRIVMSG agent :hello\r\n")
	expectLine(t, senderReader, sender, " 762 ")
	expectLine(t, liveReader, live, "PRIVMSG agent :hello")
}

func TestEphemeralAccountSessionBesideItsLiveClient(t *testing.T) {
	address, _ := collaborationServer(t, t.TempDir())
	createWireAccount(t, address, "alice")
	live, liveReader := rawConn(t, address)
	io.WriteString(live, "AUTH alice :"+testAdminToken+"\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, liveReader, live, " 001 ")
	// The account's own one-shot command is not a guest: it may run beside
	// its live client, and SASL reports the account ID as AUTH does.
	one, oneReader := rawConn(t, address)
	payload := base64.StdEncoding.EncodeToString([]byte("\x00alice\x00" + testAdminToken))
	io.WriteString(one, "CAP REQ :sasl\r\nAUTHENTICATE PLAIN\r\nAUTHENTICATE "+payload+"\r\n")
	line := expectLine(t, oneReader, one, " 779 ")
	if fields := strings.Fields(line); len(fields) < 4 || !protocol.ValidMessageID(fields[3]) {
		t.Fatalf("SASL login did not name the account ID: %q", line)
	}
	expectLine(t, oneReader, one, " 903 ")
	io.WriteString(one, "CAP END\r\nEPHEMERAL\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, oneReader, one, " 001 ")
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
	if !strings.Contains(line, "AIRC=1") {
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
