//go:build darwin || linux

package adapter

import (
	"net"
	"runtime"
	"testing"

	"golang.org/x/net/ipv4"
)

func multicastIPv4Fixture(t *testing.T) (*IPv4MulticastPackets, *net.UDPConn, *net.Interface) {
	t.Helper()
	name := "lo"
	if runtime.GOOS == "darwin" {
		name = "lo0"
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	packets := ipv4.NewPacketConn(client)
	if err := packets.SetMulticastInterface(iface); err != nil {
		t.Fatal(err)
	}
	if err := packets.SetMulticastLoopback(true); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.LocalAddr().(*net.UDPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := ListenIPv4Multicast(&net.UDPAddr{IP: net.IPv4(239, 255, 1, 2), Port: port}, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return owner, client, iface
}
