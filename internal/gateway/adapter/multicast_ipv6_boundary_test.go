//go:build darwin || linux

package adapter

import (
	"net"
	"testing"
	"time"
)

func TestIPv6MulticastFilteredInputHonorsDeadline(t *testing.T) {
	owner, client, _ := multicastIPv6Fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	if _, err := client.WriteToUDP([]byte("unicast is not ingress"), &net.UDPAddr{IP: net.IPv6loopback, Port: group.Port}); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := owner.ReadFrom(make([]byte, 1001)); err == nil {
		t.Fatal("accepted unicast ingress")
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatal("filtered read lost deadline", err)
	}
}

func TestIPv6MulticastRejectsInvalidMembership(t *testing.T) {
	_, _, iface := multicastIPv6Fixture(t)
	for _, group := range []*net.UDPAddr{nil, {IP: net.ParseIP("ff11::1234")}, {IP: net.IPv6loopback, Port: 9001}, {IP: net.ParseIP("ff02::1"), Port: 9001}, {IP: net.IPv6loopback, Port: 9001}, {IP: net.ParseIP("ff11::1234"), Port: 65536}} {
		if conn, err := ListenIPv6Multicast(group, iface.Name); err == nil {
			_ = conn.Close()
			t.Fatal("accepted invalid membership", group)
		}
	}
	if conn, err := ListenIPv6Multicast(&net.UDPAddr{IP: net.ParseIP("ff11::1234"), Port: 9001}, ""); err == nil {
		_ = conn.Close()
		t.Fatal("accepted implicit interface")
	}
	if conn, err := ListenIPv6Multicast(&net.UDPAddr{IP: net.ParseIP("ff11::1234"), Port: 9001}, "airc-no-such-interface"); err == nil {
		_ = conn.Close()
		t.Fatal("silently selected another interface")
	}
}
