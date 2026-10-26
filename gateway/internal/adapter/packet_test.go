package adapter

import (
	"net"
	"path/filepath"
	"testing"
)

func TestPacketDestinationValidation(t *testing.T) {
	var nilUDP *net.UDPAddr
	var nilUnix *net.UnixAddr
	if packetAddrKey(nil) != "unnamed:" || packetAddrKey(nilUDP) != "unnamed:udp" || packetAddrKey(nilUnix) != "unnamed:unix" {
		t.Fatal("nil packet addresses were not assigned bounded unnamed keys")
	}
	if validPacketDestination(&net.UDPAddr{IP: net.IPv4zero, Port: 9000}) {
		t.Fatal("accepted unspecified UDP destination")
	}
	if validPacketDestination(&net.UDPAddr{IP: net.IPv4bcast, Port: 9000}) {
		t.Fatal("accepted broadcast UDP destination")
	}
	if validPacketDestination(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: -1}) || validPacketDestination(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 65536}) || validPacketDestination(&net.UDPAddr{IP: net.IP{1, 2, 3}, Port: 9000}) {
		t.Fatal("accepted invalid UDP destination bounds")
	}
	path := filepath.Join("/tmp", "airc-reply-test.sock")
	if !validPacketDestination(&net.UnixAddr{Name: path, Net: "unixgram"}) {
		t.Fatal("rejected absolute unixgram destination")
	}
	if validPacketDestination(&net.UnixAddr{Name: filepath.Join(".", path), Net: "unixgram"}) {
		t.Fatal("accepted noncanonical unixgram destination")
	}
	if validPacketDestination(&net.UnixAddr{Name: path, Net: "unix"}) {
		t.Fatal("accepted stream unix destination")
	}
}
