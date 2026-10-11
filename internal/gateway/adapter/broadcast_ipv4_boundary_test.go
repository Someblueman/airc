//go:build darwin || linux

package adapter

import (
	"net"
	"net/netip"
	"runtime"
	"testing"
)

func TestIPv4BroadcastRejectsSubnetNetworkReply(t *testing.T) {
	owner := &IPv4BroadcastPackets{subnet: netip.MustParsePrefix("192.0.2.0/24")}
	if _, err := owner.WriteTo([]byte("must not reply"), &net.UDPAddr{IP: net.ParseIP("192.0.2.0"), Port: 9001}); err == nil {
		t.Fatal("accepted subnet network address as a reply target")
	}
}

func TestIPv4BroadcastRejectsUnownedSubnet(t *testing.T) {
	name := "lo"
	if runtime.GOOS == "darwin" {
		name = "lo0"
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := ListenIPv4Broadcast(&net.UDPAddr{IP: net.IPv4(192, 0, 2, 255), Port: 9001}, iface.Name, "192.0.2.0/24"); err == nil {
		owner.Close()
		t.Fatal("accepted subnet absent from interface")
	}
}

func TestIPv4BroadcastRejectsNonBroadcastInterface(t *testing.T) {
	name := "lo"
	if runtime.GOOS == "darwin" {
		name = "lo0"
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if iface.Flags&net.FlagBroadcast != 0 {
		t.Fatal("loopback fixture unexpectedly has broadcast flag")
	}
	if conn, err := ListenIPv4Broadcast(&net.UDPAddr{IP: net.IPv4(127, 255, 255, 255), Port: 9001}, name, "127.0.0.0/8"); err == nil {
		conn.Close()
		t.Fatal("accepted non-broadcast interface")
	}
}
