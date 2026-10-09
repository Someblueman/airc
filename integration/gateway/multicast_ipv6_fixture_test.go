//go:build darwin || linux

package gateway_test

import (
	"net"
	"runtime"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/net/ipv6"
)

func newIPv6MulticastFixture(t *testing.T) udpFixture {
	t.Helper()
	return newPacketFixtureConfigured(t, "ipv6_multicast", func(c *config.Config, source, _ *net.UDPConn) {
		name := "lo"
		if runtime.GOOS == "darwin" {
			name = "lo0"
		}
		iface, err := net.InterfaceByName(name)
		if err != nil {
			t.Fatal(err)
		}
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
		c.Listeners[0].IPv6Multicast = &config.IPv6Multicast{Interface: name}
		c.ReturnRoutes[0].Listener = "ipv6_multicast"
	}, listenIPv6MulticastFixtureSocket)
}

func listenIPv6MulticastFixtureSocket() (*net.UDPConn, error) {
	return net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
}

func listenIPv6MulticastShutdownPort(port int) (*net.UDPConn, error) {
	return net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified, Port: port})
}
