package server

import (
	"bufio"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func wireNames(t *testing.T, conn net.Conn, reader *bufio.Reader, room string) []string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "NAMES "+room+"\r\n"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		command, err := protocol.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		if command.Name == "353" {
			if len(command.Params) != 3 || command.Params[1] != "=" || command.Params[2] != room {
				t.Fatal("invalid names response", line)
			}
			names = append(names, strings.Fields(command.Trailing)...)
		}
		if command.Name == "366" {
			if len(command.Params) != 2 || command.Params[1] != room {
				t.Fatal("invalid names completion", line)
			}
			return names
		}
	}
}

func TestNamesVisibilityCannotChangeWhileJoined(t *testing.T) {
	address, _ := adminWireServer(t)
	hidden, hr := wireRegister(t, address, "hidden-member", false)
	visible, vr := wireRegister(t, address, "visible-member", false)
	for _, peer := range []struct {
		conn   net.Conn
		reader *bufio.Reader
	}{{hidden, hr}, {visible, vr}} {
		if _, err := io.WriteString(peer.conn, "JOIN #members\r\n"); err != nil {
			t.Fatal(err)
		}
		expectLine(t, peer.reader, peer.conn, "JOIN #members")
		expectLine(t, peer.reader, peer.conn, " 366 ")
	}
	before := wireNames(t, visible, vr, "#members")
	if !slices.Contains(before, "hidden-member") || !slices.Contains(before, "visible-member") {
		t.Fatal("positive membership control", before)
	}
	if _, err := io.WriteString(hidden, "OBSERVE #other\r\n"); err != nil {
		t.Fatal(err)
	}
	expectLine(t, hr, hidden, " 484 ")
	stillVisible := wireNames(t, visible, vr, "#members")
	if !slices.Contains(stillVisible, "hidden-member") {
		t.Fatal("rejected observation hid joined member", stillVisible)
	}
	if _, err := io.WriteString(hidden, "PART #members\r\nOBSERVE #other\r\n"); err != nil {
		t.Fatal(err)
	}
	expectLine(t, hr, hidden, "PART #members")
	expectLine(t, hr, hidden, " 765 ")
	if _, err := io.WriteString(hidden, "JOIN #members\r\n"); err != nil {
		t.Fatal(err)
	}
	expectLine(t, hr, hidden, " 484 ")
	after := wireNames(t, visible, vr, "#members")
	if len(after) != 1 || after[0] != "visible-member" {
		t.Fatal("hidden member exposed", after)
	}
	if got := wireNames(t, visible, vr, "#unknown"); len(got) != 0 {
		t.Fatal("unknown room fabricated members", got)
	}
	if _, err := io.WriteString(visible, "PART #members\r\n"); err != nil {
		t.Fatal(err)
	}
	expectLine(t, vr, visible, "PART #members")
	if got := wireNames(t, visible, vr, "#members"); len(got) != 0 {
		t.Fatal("all-hidden room exposed members", got)
	}
}
