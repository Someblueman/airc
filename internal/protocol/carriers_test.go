package protocol

import (
	"strings"
	"testing"
)

func TestCarriersRoundTripInOrder(t *testing.T) {
	list := "irc:6667,framed:6683,websocket+tls:6681,http:6680,unix:/home/a/.local/state/airc/gateway.sock"
	carriers, err := ParseCarriers(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(carriers) != 5 || carriers[0] != (Carrier{Name: "irc", Port: 6667}) || carriers[2] != (Carrier{Name: "websocket", TLS: true, Port: 6681}) || carriers[4].Path != "/home/a/.local/state/airc/gateway.sock" {
		t.Fatalf("%+v", carriers)
	}
	if got := FormatCarriers(carriers); got != list {
		t.Fatal(got)
	}
	if empty, err := ParseCarriers(""); err != nil || empty != nil {
		t.Fatal(empty, err)
	}
}

func TestCarriersRejectMalformedEntries(t *testing.T) {
	for _, bad := range []string{
		"http", "http:", "http:0", "http:65536", "http:080", "http:+80", "HTTP:80", "1http:80", "ht-tp:80",
		"http:80,http:80", "unix+tls:/tmp/s", "unix:relative", "unix:/tmp/../s", "unix:/tmp/a b", "unix:/tmp/a,b",
		"unix:/tmp/a=b", "unix:/" + strings.Repeat("a", 107), "http:80,", strings.Repeat("http:80,", 16) + "irc:1",
	} {
		if _, err := ParseCarriers(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
