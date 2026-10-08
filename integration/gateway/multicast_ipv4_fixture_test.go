//go:build darwin || linux

package gateway_test

import (
	"net"
	"runtime"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/net/ipv4"
)

func newIPv4MulticastFixture(t *testing.T) udpFixture {
	t.Helper()
	return newPacketFixtureConfigured(t, "ipv4_multicast", func(c *config.Config, source, _ *net.UDPConn) {
		name := "lo"
		if runtime.GOOS == "darwin" {
			name = "lo0"
		}
		iface, err := net.InterfaceByName(name)
		if err != nil {
			t.Fatal(err)
		}
		packets := ipv4.NewPacketConn(source)
		if err := packets.SetMulticastInterface(iface); err != nil {
			t.Fatal(err)
		}
		if err := packets.SetMulticastLoopback(true); err != nil {
			t.Fatal(err)
		}
		if err := packets.SetMulticastTTL(1); err != nil {
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
		c.Listeners[0].ID = "ipv4_multicast"
		c.Listeners[0].Address = (&net.UDPAddr{IP: net.IPv4(239, 255, 1, 2), Port: port}).String()
		c.Listeners[0].IPv4Multicast = &config.IPv4Multicast{Interface: name}
		c.ReturnRoutes[0].Listener = "ipv4_multicast"
	})
}
