package server

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestMonitorAndAwayLifecycle(t *testing.T) {
	address := rawServer(t)
	watch, wr := wireRegister(t, address, "watcher", true)
	io.WriteString(watch, "MONITOR + alice,bob\r\n")
	expectLine(t, wr, watch, " 731 watcher :alice")
	expectLine(t, wr, watch, " 731 watcher :bob")
	alice, ar := wireRegister(t, address, "alice", false)
	expectLine(t, wr, watch, " 730 watcher :alice!")
	io.WriteString(alice, "AWAY :working elsewhere\r\n")
	expectLine(t, ar, alice, " 306 ")
	io.WriteString(watch, "WHOIS alice\r\nDIRECTORY alice\r\n")
	expectLine(t, wr, watch, " 301 watcher alice :working elsewhere")
	if line := expectLine(t, wr, watch, " 773 "); !strings.Contains(line, `"state":"away"`) {
		t.Fatal(line)
	}
	io.WriteString(alice, "AWAY\r\nNICK bob\r\n")
	expectLine(t, ar, alice, " 305 ")
	expectLine(t, wr, watch, " 731 watcher :alice")
	expectLine(t, wr, watch, " 730 watcher :bob!")
	alice.Close()
	expectLine(t, wr, watch, " 731 watcher :bob")
	transient, tr := wireRegister(t, address, "alice", true)
	io.WriteString(transient, "AWAY :invalid\r\n")
	expectLine(t, tr, transient, " 484 ")
	io.WriteString(watch, "MONITOR S\r\n")
	// If a one-shot connection emitted an online notification, this check fails.
	for {
		line, err := wr.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, " 730 ") {
			t.Fatal(line)
		}
		if strings.Contains(line, " 731 ") {
			break
		}
	}
}

func TestNoticeRetainsKindOnWireAndInHistory(t *testing.T) {
	address := rawServer(t)
	c, r := wireRegister(t, address, "sender", false)
	io.WriteString(c, "JOIN #room\r\nNOTICE #room :bot: ping\r\n")
	line := expectLine(t, r, c, " NOTICE #room :bot: ping")
	cmd, err := protocol.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.DecodeChat(cmd.Tags[protocol.ChatTag]).Kind != "notice" {
		t.Fatal(line)
	}
	io.WriteString(c, "HISTORY #room\r\n")
	line = expectLine(t, r, c, " 760 ")
	cmd, _ = protocol.Parse(line)
	m, err := protocol.DecodeMessageMetadata(cmd.Trailing)
	if err != nil || m.Kind != "notice" {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestMonitorBoundAndClear(t *testing.T) {
	address := rawServer(t)
	c, r := wireRegister(t, address, "reader", true)
	names := make([]string, 128)
	for i := range names {
		names[i] = fmt.Sprintf("agent%d", i)
	}
	io.WriteString(c, "MONITOR + "+strings.Join(names, ",")+"\r\nMONITOR + overflow\r\n")
	expectLine(t, r, c, " 734 ")
	io.WriteString(c, "MONITOR C\r\nMONITOR + overflow\r\nMONITOR L\r\n")
	expectLine(t, r, c, " 731 reader :overflow")
	expectLine(t, r, c, " 732 reader :overflow")
	expectLine(t, r, c, " 733 ")
}
