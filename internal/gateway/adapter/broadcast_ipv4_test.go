//go:build darwin || linux

package adapter

import (
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
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
