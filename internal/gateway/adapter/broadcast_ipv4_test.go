//go:build darwin || linux

package adapter

import (
	"encoding/binary"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/config"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"
)

func broadcastIPv4Fixture(t *testing.T) (*IPv4BroadcastPackets, *net.UDPConn, *net.Interface) {
	t.Helper()
	name, subnetText := os.Getenv("AGW_BROADCAST_INTERFACE"), os.Getenv("AGW_BROADCAST_SUBNET")
	if name == "" && subnetText == "" {
		t.Skip("requires explicitly authorized broadcast interface/subnet fixture")
	}
	if err := (&config.IPv4Broadcast{Interface: name, Subnet: subnetText}).Validate("255.255.255.255:9001"); err != nil {
		t.Fatal(err)
	}
	subnet, err := netip.ParsePrefix(subnetText)
	if err != nil {
		t.Fatal(err)
	}
	bytes := subnet.Addr().As4()
	binary.BigEndian.PutUint32(bytes[:], binary.BigEndian.Uint32(bytes[:])|(uint32(1)<<uint(32-subnet.Bits())-1))
	broadcast := netip.AddrFrom4(bytes)
	if err := (&config.IPv4Broadcast{Interface: name, Subnet: subnetText}).Validate(net.JoinHostPort(broadcast.String(), "9001")); err != nil {
		t.Fatal(err)
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := iface.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	var source net.IP
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err == nil && prefix.Addr().Is4() && prefix.Masked() == subnet {
			source = net.ParseIP(prefix.Addr().String())
			break
		}
	}
	if source == nil {
		t.Fatal("fixture interface does not own subnet")
	}
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
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
	owner, err := ListenIPv4Broadcast(&net.UDPAddr{IP: net.ParseIP(broadcast.String()), Port: port}, name, subnetText)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return owner, client, iface
}

func TestIPv4BroadcastFiltersDestinationAndReturnsUnicast(t *testing.T) {
	owner, client, _ := broadcastIPv4Fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	wrong := &net.UDPAddr{IP: client.LocalAddr().(*net.UDPAddr).IP, Port: group.Port}
	if _, err := client.WriteToUDP([]byte("wrong destination"), wrong); err != nil {
		t.Fatal(err)
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

func TestIPv4BroadcastRejectsReplyStormAndJoinsClose(t *testing.T) {
	owner, _, _ := broadcastIPv4Fixture(t)
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
