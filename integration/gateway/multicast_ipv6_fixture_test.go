//go:build darwin || linux

package gateway_test

import (
	"net"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testnet"
	"golang.org/x/net/ipv6"
)

func newIPv6MulticastFixture(t *testing.T) udpFixture {
	t.Helper()
	iface, address := testnet.IPv6Multicast(t)
	return newPacketFixtureConfigured(t, "ipv6_multicast", func(c *config.Config, source, _ *net.UDPConn) {
		packets := ipv6.NewPacketConn(source)
		if err := packets.SetMulticastInterface(iface); err != nil {
			t.Fatal(err)
		}
		if err := packets.SetMulticastLoopback(true); err != nil {
			t.Fatal(err)
		}
		if err := packets.SetMulticastHopLimit(1); err != nil {
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
		c.Listeners[0].ID = "ipv6_multicast"
		c.Listeners[0].Address = (&net.UDPAddr{IP: net.ParseIP("ff11::1234"), Port: port}).String()
		c.Listeners[0].IPv6Multicast = &config.IPv6Multicast{Interface: iface.Name}
	}, func() (*net.UDPConn, error) {
		return net.ListenUDP("udp6", &net.UDPAddr{IP: address})
	})
}

func listenIPv6MulticastShutdownPort(port int) (*net.UDPConn, error) {
	return net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified, Port: port})
}
