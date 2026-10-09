//go:build darwin || linux

package adapter

import (
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestIPv4BroadcastFilteredInputHonorsDeadline(t *testing.T) {
	owner, client, _ := broadcastIPv4Fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	if _, err := client.WriteToUDP([]byte("unicast is not ingress"), &net.UDPAddr{IP: client.LocalAddr().(*net.UDPAddr).IP, Port: group.Port}); err != nil {
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

func TestIPv4BroadcastRejectsSubnetNetworkReply(t *testing.T) {
	owner := &IPv4BroadcastPackets{options: config.IPv4Broadcast{Subnet: "192.0.2.0/24"}}
	if _, err := owner.WriteTo([]byte("must not reply"), &net.UDPAddr{IP: net.ParseIP("192.0.2.0"), Port: 9001}); err == nil {
		t.Fatal("accepted subnet network address as a reply target")
	}
}

func TestIPv4BroadcastRejectsInvalidMembership(t *testing.T) {
	name := "lo"
	if runtime.GOOS == "darwin" {
		name = "lo0"
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []*net.UDPAddr{nil, {IP: net.IPv4(127, 255, 255, 255)}, {IP: net.IPv4(127, 0, 0, 1), Port: 9001}, {IP: net.IPv4(224, 0, 0, 1), Port: 9001}, {IP: net.IPv6loopback, Port: 9001}, {IP: net.IPv4(127, 255, 255, 255), Port: 65536}} {
		if conn, err := ListenIPv4Broadcast(group, iface.Name, "127.0.0.0/8"); err == nil {
			_ = conn.Close()
			t.Fatal("accepted invalid membership", group)
		}
	}
	if conn, err := ListenIPv4Broadcast(&net.UDPAddr{IP: net.IPv4(127, 255, 255, 255), Port: 9001}, "", "127.0.0.0/8"); err == nil {
		_ = conn.Close()
		t.Fatal("accepted implicit interface")
	}
	if conn, err := ListenIPv4Broadcast(&net.UDPAddr{IP: net.IPv4(127, 255, 255, 255), Port: 9001}, "airc-no-such-interface", "127.0.0.0/8"); err == nil {
		_ = conn.Close()
		t.Fatal("silently selected another interface")
	}
}

func TestIPv4BroadcastRejectsUnownedSubnet(t *testing.T) {
	name := "lo"
	if runtime.GOOS == "darwin" {
		name = "lo0"
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := ListenIPv4Broadcast(&net.UDPAddr{IP: net.IPv4(192, 0, 2, 255), Port: 9001}, iface.Name, "192.0.2.0/24"); err == nil {
		owner.Close()
		t.Fatal("accepted subnet absent from interface")
	}
}

func TestIPv4BroadcastRejectsNonBroadcastInterface(t *testing.T) {
	name := "lo"
	if runtime.GOOS == "darwin" {
		name = "lo0"
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if iface.Flags&net.FlagBroadcast != 0 {
		t.Fatal("loopback fixture unexpectedly has broadcast flag")
	}
	if conn, err := ListenIPv4Broadcast(&net.UDPAddr{IP: net.IPv4(127, 255, 255, 255), Port: 9001}, name, "127.0.0.0/8"); err == nil {
		conn.Close()
		t.Fatal("accepted non-broadcast interface")
	}
}
