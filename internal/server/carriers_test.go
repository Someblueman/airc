package server

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestCarriersAdvertisedInISupportAndStatus(t *testing.T) {
	carriers, err := protocol.ParseCarriers("irc:6667,framed:6683,http:6680")
	if err != nil {
		t.Fatal(err)
	}
	address, _ := startServer(t, Config{Carriers: carriers}, nil)
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "NICK finder\r\nUSER u 0 * :u\r\n")
	if line := expectLine(t, reader, conn, " 005 "); !strings.Contains(line, " CARRIERS=irc:6667,framed:6683,http:6680 ") {
		t.Fatalf("005 without ordered carriers: %q", line)
	}
	expectLine(t, reader, conn, " 001 ")
	io.WriteString(conn, "STATUS\r\n")
	line := expectLine(t, reader, conn, " 770 ")
	_, payload, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " :")
	var status protocol.ServerStatus
	if err := json.Unmarshal([]byte(payload), &status); err != nil {
		t.Fatal(err, line)
	}
	if strings.Join(status.Carriers, ",") != "irc:6667,framed:6683,http:6680" {
		t.Fatalf("STATUS carriers %v", status.Carriers)
	}

	// Without a list nothing is advertised.
	plain, _ := startServer(t, Config{}, nil)
	conn, reader = rawConn(t, plain)
	io.WriteString(conn, "NICK finder\r\nUSER u 0 * :u\r\n")
	if line := expectLine(t, reader, conn, " 005 "); strings.Contains(line, "CARRIERS") {
		t.Fatalf("advertised carriers without a list: %q", line)
	}
}
