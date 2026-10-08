package adapter

import (
	"net"
	"testing"
	"time"
)

func TestIPv4MulticastFilteredInputHonorsDeadline(t *testing.T) {
	owner, client, _ := multicastIPv4Fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	if _, err := client.WriteToUDP([]byte("unicast is not ingress"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: group.Port}); err != nil {
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

func TestIPv4MulticastRejectsInvalidMembership(t *testing.T) {
	_, _, iface := multicastIPv4Fixture(t)
	for _, group := range []*net.UDPAddr{nil, {IP: net.IPv4(239, 255, 1, 2)}, {IP: net.IPv4(127, 0, 0, 1), Port: 9001}, {IP: net.IPv4(224, 0, 0, 1), Port: 9001}, {IP: net.IPv6loopback, Port: 9001}, {IP: net.IPv4(239, 255, 1, 2), Port: 65536}} {
		if conn, err := ListenIPv4Multicast(group, iface.Name); err == nil {
			_ = conn.Close()
			t.Fatal("accepted invalid membership", group)
		}
	}
	if conn, err := ListenIPv4Multicast(&net.UDPAddr{IP: net.IPv4(239, 255, 1, 2), Port: 9001}, ""); err == nil {
		_ = conn.Close()
		t.Fatal("accepted implicit interface")
	}
	if conn, err := ListenIPv4Multicast(&net.UDPAddr{IP: net.IPv4(239, 255, 1, 2), Port: 9001}, "airc-no-such-interface"); err == nil {
		_ = conn.Close()
		t.Fatal("silently selected another interface")
	}
}
