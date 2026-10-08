package adapter

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

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

func TestIPv4MulticastFiltersDestinationAndReturnsUnicast(t *testing.T) {
	owner, client, iface := multicastIPv4Fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	other := &net.UDPAddr{IP: net.IPv4(239, 255, 1, 3), Port: group.Port}
	otherOwner, err := ListenIPv4Multicast(other, iface.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer otherOwner.Close()
	for _, destination := range []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 1), Port: group.Port}, other} {
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

func TestIPv4MulticastRejectsReplyStormAndJoinsClose(t *testing.T) {
	owner, _, _ := multicastIPv4Fixture(t)
	for _, destination := range []net.Addr{nil, (*net.UDPAddr)(nil), owner.LocalAddr(), &net.UDPAddr{IP: net.IPv4bcast, Port: 9001}, &net.UDPAddr{IP: net.IPv4zero, Port: 9001}, &net.UDPAddr{IP: net.IPv6loopback, Port: 9001}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}} {
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
