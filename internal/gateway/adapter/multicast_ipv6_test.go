//go:build darwin || linux

package adapter

import (
	"errors"
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

func TestIPv6MulticastFiltersDestinationAndReturnsUnicast(t *testing.T) {
	owner, client, iface := multicastIPv6Fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	other := &net.UDPAddr{IP: net.ParseIP("ff11::1235"), Port: group.Port}
	otherOwner, err := ListenIPv6Multicast(other, iface.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer otherOwner.Close()
	other.Zone = iface.Name
	for _, destination := range []*net.UDPAddr{{IP: net.IPv6loopback, Port: group.Port}, other} {
		if _, err := client.WriteToUDP([]byte("wrong destination"), destination); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.WriteToUDP([]byte("configured group"), group); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 1001)
	n, source, err := owner.ReadFrom(body)
	if err != nil || string(body[:n]) != "configured group" || source.String() != client.LocalAddr().String() {
		t.Fatal("wrong membership delivery", n, source, err)
	}
	if _, err := owner.WriteTo([]byte("unicast reply"), source); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	n, _, err = client.ReadFromUDP(body)
	if err != nil || string(body[:n]) != "unicast reply" {
		t.Fatal("missing unicast response", n, err)
	}
	copyAddress := owner.LocalAddr().(*net.UDPAddr)
	copyAddress.IP[0] = 1
	if !owner.LocalAddr().(*net.UDPAddr).IP.Equal(group.IP) {
		t.Fatal("caller mutated retained membership")
	}
}

func TestIPv6MulticastRejectsReplyStormAndJoinsClose(t *testing.T) {
	owner, _, _ := multicastIPv6Fixture(t)
	for _, destination := range []net.Addr{nil, (*net.UDPAddr)(nil), owner.LocalAddr(), &net.UDPAddr{IP: net.IPv4bcast, Port: 9001}, &net.UDPAddr{IP: net.IPv6unspecified, Port: 9001}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9001}, &net.UDPAddr{IP: net.IPv6loopback, Port: 0}, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 9001}, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 9001, Zone: "other"}, &net.UDPAddr{IP: net.IPv6loopback, Port: 9001, Zone: "lo0"}} {
		if _, err := owner.WriteTo([]byte("forbidden reply"), destination); err == nil {
			t.Fatal("accepted unsafe reply", destination)
		}
	}
	joined := make(chan error, 1)
	go func() { _, _, err := owner.ReadFrom(make([]byte, 1001)); joined <- err }()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-joined:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal("unexpected close result", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not join blocked read")
	}
}
