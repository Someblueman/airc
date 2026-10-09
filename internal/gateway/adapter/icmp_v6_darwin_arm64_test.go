//go:build darwin && arm64

package adapter

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

func icmpV6Fixture(t *testing.T, channel uint16) *ICMPv6Packets {
	t.Helper()
	address, err := ParseICMPv6Addr(fmt.Sprintf("[::1]:%d", channel))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := ListenICMPv6(address, "lo0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func icmpV6MaxAGD1(t *testing.T) []byte {
	t.Helper()
	fragments, err := fragmentAGD1(bytes.Repeat([]byte{0x5a}, agd1PayloadBytes))
	if err != nil || len(fragments) != 1 || len(fragments[0]) != agd1DatagramBytes {
		t.Fatalf("could not create maximum AGD1 datagram: %v", err)
	}
	return fragments[0]
}

func TestICMPv6DatagramsCarryMaximumAGD1BothWays(t *testing.T) {
	a := icmpV6Fixture(t, 23917)
	b := icmpV6Fixture(t, 23918)
	body := icmpV6MaxAGD1(t)
	if n, err := a.WriteTo(body, b.LocalAddr()); err != nil || n != len(body) {
		t.Fatalf("A->B write = %d, %v", n, err)
	}
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, agd1DatagramBytes)
	n, source, err := b.ReadFrom(got)
	if err != nil || n != len(body) || !bytes.Equal(got[:n], body) {
		t.Fatalf("B read = %d, %v", n, err)
	}
	peer, ok := source.(*ICMPv6Addr)
	if !ok || peer.Channel != 23917 || !peer.IP.Equal(net.IPv6loopback) {
		t.Fatalf("B source = %#v", source)
	}
	if n, err := b.WriteTo(body, source); err != nil || n != len(body) {
		t.Fatalf("B->A write = %d, %v", n, err)
	}
	_ = a.SetReadDeadline(time.Now().Add(time.Second))
	got = make([]byte, agd1DatagramBytes)
	n, source, err = a.ReadFrom(got)
	if err != nil || n != len(body) || !bytes.Equal(got[:n], body) {
		t.Fatalf("A read = %d, %v", n, err)
	}
	peer, ok = source.(*ICMPv6Addr)
	if !ok || peer.Channel != 23918 || !peer.IP.Equal(net.IPv6loopback) {
		t.Fatalf("A source = %#v", source)
	}
}

func TestICMPv6ReadDropsWrongIDsZeroSourceAndOversizeThenReadsValid(t *testing.T) {
	receiver := icmpV6Fixture(t, 23928)
	sender := icmpV6Fixture(t, 23927)
	body := icmpV6MaxAGD1(t)
	overlongBody := append(append([]byte(nil), body...), 0x44)
	controlFragments, err := fragmentAGD1([]byte("separate valid control packet"))
	if err != nil || len(controlFragments) != 1 {
		t.Fatalf("could not make valid control packet: %v", err)
	}
	controlBody := controlFragments[0]
	writeRaw := func(id, seq int, data []byte) {
		t.Helper()
		wire, err := (&icmp.Message{Type: ipv6.ICMPTypeEchoRequest, Code: 0,
			Body: &icmp.Echo{ID: id, Seq: seq, Data: data}}).Marshal(nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sender.conn.WriteTo(wire, &net.UDPAddr{IP: net.IPv6loopback}); err != nil {
			t.Fatal(err)
		}
	}
	writeRaw(int(receiver.local.Channel)+1, int(sender.local.Channel), body)
	writeRaw(int(receiver.local.Channel), 0, body)
	writeRaw(int(receiver.local.Channel), int(sender.local.Channel), overlongBody)
	if _, err := sender.WriteTo(controlBody, receiver.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_ = receiver.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, agd1DatagramBytes)
	n, source, err := receiver.ReadFrom(got)
	if err != nil || n != len(controlBody) || !bytes.Equal(got[:n], controlBody) {
		t.Fatalf("valid control read = %d, %v", n, err)
	}
	peer, ok := source.(*ICMPv6Addr)
	if !ok || peer.Channel != sender.local.Channel {
		t.Fatalf("valid source = %#v", source)
	}
	// The kernel reflects an EchoReply after the successful request. It must not
	// be delivered as a second datagram.
	_ = receiver.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if n, _, err := receiver.ReadFrom(got); err == nil || n != 0 {
		t.Fatalf("kernel EchoReply escaped the filter: n=%d err=%v", n, err)
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("reply-filter read lost deadline: %v", err)
	}
}

func TestICMPv6SourceRequiresPinnedDestinationAndInterface(t *testing.T) {
	localIP := net.IPv6loopback
	source := &net.UDPAddr{IP: net.IPv6loopback}
	control := &ipv6.ControlMessage{Dst: net.IPv6loopback, IfIndex: 1}
	if peer, ok := validICMPv6Source(control, source, 7, localIP, 1); !ok || peer.Channel != 7 {
		t.Fatalf("valid metadata rejected: %#v %v", peer, ok)
	}
	for _, tc := range []struct {
		name     string
		control  *ipv6.ControlMessage
		source   net.Addr
		sequence int
	}{
		{"missing control", nil, source, 7},
		{"wrong destination", &ipv6.ControlMessage{Dst: net.ParseIP("::2"), IfIndex: 1}, source, 7},
		{"wrong interface", &ipv6.ControlMessage{Dst: net.IPv6loopback, IfIndex: 2}, source, 7},
		{"zero sender channel", control, source, 0},
		{"missing source", control, nil, 7},
		{"IPv4 source", control, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, 7},
		{"link-local source", control, &net.UDPAddr{IP: net.ParseIP("fe80::1")}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if peer, ok := validICMPv6Source(tc.control, tc.source, tc.sequence, localIP, 1); ok {
				t.Fatalf("accepted invalid source metadata: %#v", peer)
			}
		})
	}
}

func TestICMPv6ParseAndListenRequireValidInterfaceEndpoint(t *testing.T) {
	for _, input := range []string{"127.0.0.1:12", "[::ffff:127.0.0.1]:12", "[::]:12", "[ff02::1]:12", "[fe80::1%lo0]:12", "[::1]:0", "[::1]:012"} {
		if address, err := ParseICMPv6Addr(input); err == nil {
			t.Errorf("accepted invalid endpoint %q as %v", input, address)
		}
	}
	address, err := ParseICMPv6Addr("[2001:db8::1]:23917")
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := ListenICMPv6(address, "lo0"); err == nil {
		_ = conn.Close()
		t.Fatal("accepted an address not owned by lo0")
	}
	if err := (&config.ICMPv6{Interface: "lo0"}).Validate("[::1]:23917"); err != nil {
		t.Fatalf("test endpoint rejected by config: %v", err)
	}
}

func TestICMPv6WriteRefusesInvalidDestinationsAndBodies(t *testing.T) {
	conn := icmpV6Fixture(t, 23938)
	validDestination, err := ParseICMPv6Addr("[::1]:23939")
	if err != nil {
		t.Fatal(err)
	}
	validBody := icmpV6MaxAGD1(t)
	for _, tc := range []struct {
		name string
		body []byte
		dest net.Addr
	}{
		{"empty", nil, validDestination},
		{"oversize", bytes.Repeat([]byte{1}, agd1DatagramBytes+1), validDestination},
		{"non-ICMP destination", validBody, &net.UDPAddr{IP: net.IPv6loopback, Port: 1}},
		{"zero channel", validBody, &ICMPv6Addr{IP: net.IPv6loopback, Channel: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if n, err := conn.WriteTo(tc.body, tc.dest); err == nil || n != 0 {
				t.Fatalf("accepted invalid write: n=%d err=%v", n, err)
			}
		})
	}
}

func TestICMPv6CloseUnblocksNativeRead(t *testing.T) {
	conn := icmpV6Fixture(t, 23948)
	result := make(chan error, 1)
	go func() {
		_, _, err := conn.ReadFrom(make([]byte, agd1DatagramBytes))
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("read succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock native read")
	}
}
