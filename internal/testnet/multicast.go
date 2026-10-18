// Package testnet provides explicit network fixture selection for tests.
package testnet

import (
	"net"
	"os"
	"runtime"
	"testing"
)

// IPv6Multicast defaults to Darwin/Linux loopback. Linux qualification can set
// AGW_IPV6_MULTICAST_INTERFACE and AGW_IPV6_MULTICAST_SOURCE to a private multicast
// interface and its IPv6 unicast address; the fixture never changes host networking.
func IPv6Multicast(t testing.TB) (*net.Interface, net.IP) {
	t.Helper()
	name := os.Getenv("AGW_IPV6_MULTICAST_INTERFACE")
	if name == "" {
		name = "lo"
		if runtime.GOOS == "darwin" {
			name = "lo0"
		}
	}
	address := os.Getenv("AGW_IPV6_MULTICAST_SOURCE")
	if address == "" {
		RequireIPv6Loopback(t) // an explicit source is the operator's to qualify
		address = "::1"
	}
	source := net.ParseIP(address)
	if source == nil || source.To4() != nil || source.IsUnspecified() || source.IsMulticast() {
		t.Fatal("AGW_IPV6_MULTICAST_SOURCE must be an IPv6 unicast address")
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return iface, source
}
