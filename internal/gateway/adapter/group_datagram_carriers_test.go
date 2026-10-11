//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testnet"
)

// groupCarrier is a datagram carrier that receives on a configured group
// address and replies only by unicast.
type groupCarrier struct {
	name string
	// fixture returns a group owner, a client able to reach it and the
	// owner's interface.
	fixture func(t *testing.T) (net.PacketConn, *net.UDPConn, *net.Interface)
	// misdirected are destinations the client reaches that are not the
	// owner's group; some may need their own membership to receive.
	misdirected func(t *testing.T, group *net.UDPAddr, client *net.UDPConn, iface *net.Interface) []*net.UDPAddr
	// unsafeReplies must never be written: groups, broadcast, unspecified,
	// the other family, port zero.
	unsafeReplies func(group net.Addr) []net.Addr
	// listen opens an owner; invalid are memberships it must refuse and
	// valid a group whose interface choice must be explicit.
	listen  func(group *net.UDPAddr, iface string) (net.PacketConn, error)
	iface   func(t *testing.T) string
	invalid []*net.UDPAddr
	valid   *net.UDPAddr
}

func loopbackName() string {
	if runtime.GOOS == "darwin" {
		return "lo0"
	}
	return "lo"
}

func groupCarriers() []groupCarrier {
	return []groupCarrier{
		{
			name: "ipv4-multicast",
			fixture: func(t *testing.T) (net.PacketConn, *net.UDPConn, *net.Interface) {
				owner, client, iface := multicastIPv4Fixture(t)
				return owner, client, iface
			},
			misdirected: func(t *testing.T, group *net.UDPAddr, _ *net.UDPConn, iface *net.Interface) []*net.UDPAddr {
				other := &net.UDPAddr{IP: net.IPv4(239, 255, 1, 3), Port: group.Port}
				otherOwner, err := ListenIPv4Multicast(other, iface.Name)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = otherOwner.Close() })
				return []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 1), Port: group.Port}, other}
			},
			unsafeReplies: func(group net.Addr) []net.Addr {
				return []net.Addr{nil, (*net.UDPAddr)(nil), group, &net.UDPAddr{IP: net.IPv4bcast, Port: 9001}, &net.UDPAddr{IP: net.IPv4zero, Port: 9001}, &net.UDPAddr{IP: net.IPv6loopback, Port: 9001}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}}
			},
			listen: func(group *net.UDPAddr, iface string) (net.PacketConn, error) {
				return ListenIPv4Multicast(group, iface)
			},
			iface:   func(*testing.T) string { return loopbackName() },
			invalid: []*net.UDPAddr{nil, {IP: net.IPv4(239, 255, 1, 2)}, {IP: net.IPv4(127, 0, 0, 1), Port: 9001}, {IP: net.IPv4(224, 0, 0, 1), Port: 9001}, {IP: net.IPv6loopback, Port: 9001}, {IP: net.IPv4(239, 255, 1, 2), Port: 65536}},
			valid:   &net.UDPAddr{IP: net.IPv4(239, 255, 1, 2), Port: 9001},
		},
		{
			name: "ipv6-multicast",
			fixture: func(t *testing.T) (net.PacketConn, *net.UDPConn, *net.Interface) {
				owner, client, iface := multicastIPv6Fixture(t)
				return owner, client, iface
			},
			misdirected: func(t *testing.T, group *net.UDPAddr, _ *net.UDPConn, iface *net.Interface) []*net.UDPAddr {
				other := &net.UDPAddr{IP: net.ParseIP("ff11::1235"), Port: group.Port}
				otherOwner, err := ListenIPv6Multicast(other, iface.Name)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = otherOwner.Close() })
				other.Zone = iface.Name
				return []*net.UDPAddr{{IP: net.IPv6loopback, Port: group.Port}, other}
			},
			unsafeReplies: func(group net.Addr) []net.Addr {
				return []net.Addr{nil, (*net.UDPAddr)(nil), group, &net.UDPAddr{IP: net.IPv4bcast, Port: 9001}, &net.UDPAddr{IP: net.IPv6unspecified, Port: 9001}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9001}, &net.UDPAddr{IP: net.IPv6loopback, Port: 0}, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 9001}, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 9001, Zone: "other"}, &net.UDPAddr{IP: net.IPv6loopback, Port: 9001, Zone: "lo0"}}
			},
			listen: func(group *net.UDPAddr, iface string) (net.PacketConn, error) {
				return ListenIPv6Multicast(group, iface)
			},
			iface: func(t *testing.T) string {
				iface, _ := testnet.IPv6Multicast(t)
				return iface.Name
			},
			invalid: []*net.UDPAddr{nil, {IP: net.ParseIP("ff11::1234")}, {IP: net.IPv6loopback, Port: 9001}, {IP: net.ParseIP("ff02::1"), Port: 9001}, {IP: net.ParseIP("ff11::1234"), Port: 65536}},
			valid:   &net.UDPAddr{IP: net.ParseIP("ff11::1234"), Port: 9001},
		},
		{
			// Needs AGW_BROADCAST_INTERFACE and AGW_BROADCAST_SUBNET for the
			// delivery scenarios; membership refusals need no fixture.
			name: "ipv4-broadcast",
			fixture: func(t *testing.T) (net.PacketConn, *net.UDPConn, *net.Interface) {
				owner, client, iface := broadcastIPv4Fixture(t)
				return owner, client, iface
			},
			misdirected: func(_ *testing.T, group *net.UDPAddr, client *net.UDPConn, _ *net.Interface) []*net.UDPAddr {
				return []*net.UDPAddr{{IP: client.LocalAddr().(*net.UDPAddr).IP, Port: group.Port}}
			},
			unsafeReplies: func(group net.Addr) []net.Addr {
				return []net.Addr{nil, (*net.UDPAddr)(nil), group, &net.UDPAddr{IP: net.IPv4bcast, Port: 9001}, &net.UDPAddr{IP: net.IPv4zero, Port: 9001}, &net.UDPAddr{IP: net.IPv6loopback, Port: 9001}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}}
			},
			listen: func(group *net.UDPAddr, iface string) (net.PacketConn, error) {
				return ListenIPv4Broadcast(group, iface, "127.0.0.0/8")
			},
			iface:   func(*testing.T) string { return loopbackName() },
			invalid: []*net.UDPAddr{nil, {IP: net.IPv4(127, 255, 255, 255)}, {IP: net.IPv4(127, 0, 0, 1), Port: 9001}, {IP: net.IPv4(224, 0, 0, 1), Port: 9001}, {IP: net.IPv6loopback, Port: 9001}, {IP: net.IPv4(127, 255, 255, 255), Port: 65536}},
			valid:   &net.UDPAddr{IP: net.IPv4(127, 255, 255, 255), Port: 9001},
		},
	}
}

// TestGroupDatagramCarriers exercises the group receive filter, unicast-only
// replies, joined close and membership validation once per group carrier.
func TestGroupDatagramCarriers(t *testing.T) {
	for _, c := range groupCarriers() {
		t.Run(c.name, func(t *testing.T) {
			t.Run("filters-destination-and-returns-unicast", func(t *testing.T) { testGroupDelivery(t, c) })
			t.Run("filtered-input-honors-deadline", func(t *testing.T) { testGroupFilteredDeadline(t, c) })
			t.Run("rejects-reply-storm-and-joins-close", func(t *testing.T) { testGroupReplyStormAndClose(t, c) })
			t.Run("rejects-invalid-membership", func(t *testing.T) { testGroupMembership(t, c) })
		})
	}
}

func testGroupDelivery(t *testing.T, c groupCarrier) {
	owner, client, iface := c.fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	for _, destination := range c.misdirected(t, group, client, iface) {
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

// testGroupFilteredDeadline: a filtered unicast datagram neither reaches the
// caller nor extends its read deadline.
func testGroupFilteredDeadline(t *testing.T, c groupCarrier) {
	owner, client, iface := c.fixture(t)
	group := owner.LocalAddr().(*net.UDPAddr)
	if _, err := client.WriteToUDP([]byte("unicast is not ingress"), c.misdirected(t, group, client, iface)[0]); err != nil {
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

func testGroupReplyStormAndClose(t *testing.T, c groupCarrier) {
	owner, _, _ := c.fixture(t)
	for _, destination := range c.unsafeReplies(owner.LocalAddr()) {
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

func testGroupMembership(t *testing.T, c groupCarrier) {
	iface := c.iface(t)
	for _, group := range c.invalid {
		if conn, err := c.listen(group, iface); err == nil {
			_ = conn.Close()
			t.Fatal("accepted invalid membership", group)
		}
	}
	if conn, err := c.listen(c.valid, ""); err == nil {
		_ = conn.Close()
		t.Fatal("accepted implicit interface")
	}
	if conn, err := c.listen(c.valid, "airc-no-such-interface"); err == nil {
		_ = conn.Close()
		t.Fatal("silently selected another interface")
	}
}
