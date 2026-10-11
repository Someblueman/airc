//go:build darwin || linux

package adapter

import (
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testnet"
	"golang.org/x/net/ipv6"
)

func multicastIPv6Fixture(t *testing.T) (*IPv6MulticastPackets, *net.UDPConn, *net.Interface) {
	t.Helper()
	iface, source := testnet.IPv6Multicast(t)
	client, err := net.ListenUDP("udp6", &net.UDPAddr{IP: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	packets := ipv6.NewPacketConn(client)
	if err := packets.SetMulticastInterface(iface); err != nil {
		t.Fatal(err)
	}
	if err := packets.SetMulticastLoopback(true); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified})
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.LocalAddr().(*net.UDPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := ListenIPv6Multicast(&net.UDPAddr{IP: net.ParseIP("ff11::1234"), Port: port}, iface.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return owner, client, iface
}
