//go:build linux

package adapter

import (
	"bytes"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"testing"
	"time"
)

func udpLiteFixture(t *testing.T, ip net.IP) *UDPLitePackets {
	t.Helper()
	owner, err := ListenUDPLite(&net.UDPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	if err := owner.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return owner
}
func udpLiteOption(t *testing.T, c *UDPLitePackets, option, value int) {
	t.Helper()
	raw, err := c.conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var optionErr error
	if err := raw.Control(func(fd uintptr) { optionErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_UDPLITE, option, value) }); err != nil {
		t.Fatal(err)
	}
	if optionErr != nil {
		t.Fatal(optionErr)
	}
}
func TestUDPLiteActualProtocolAndFullCoverage(t *testing.T) {
	for _, ip := range []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback} {
		t.Run(ip.String(), func(t *testing.T) {
			receiver, client := udpLiteFixture(t, ip), udpLiteFixture(t, ip)
			raw, err := receiver.conn.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var optionErr error
			if err := raw.Control(func(fd uintptr) {
				for option, want := range map[int]int{unix.SO_PROTOCOL: unix.IPPROTO_UDPLITE, unix.SO_BROADCAST: 0} {
					v, e := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, option)
					if e != nil {
						optionErr = e
						return
					}
					if v != want {
						optionErr = errors.New("wrong native socket protocol/options")
						return
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			if optionErr != nil {
				t.Fatal(optionErr)
			}
			udpLiteOption(t, client, udpLiteSendCoverage, 8)
			if _, err := client.WriteTo([]byte("partially covered payload"), receiver.LocalAddr()); err != nil {
				t.Fatal(err)
			}
			receiver.SetReadDeadline(time.Now().Add(25 * time.Millisecond))
			body := make([]byte, 1001)
			if _, _, err := receiver.ReadFrom(body); err == nil {
				t.Fatal("accepted partial checksum coverage")
			} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
				t.Fatal(err)
			}
			udpLiteOption(t, client, udpLiteSendCoverage, 0)
			receiver.SetReadDeadline(time.Now().Add(time.Second))
			want := []byte("whole UDP-Lite Καλημέρα 世界 🛰️")
			if _, err := client.WriteTo(want, receiver.LocalAddr()); err != nil {
				t.Fatal(err)
			}
			n, source, err := receiver.ReadFrom(body)
			if err != nil || !bytes.Equal(body[:n], want) || source.String() != client.LocalAddr().String() {
				t.Fatal(n, source, err)
			}
			if _, err := receiver.WriteTo(want, source); err != nil {
				t.Fatal(err)
			}
			n, _, err = client.ReadFrom(body)
			if err != nil || !bytes.Equal(body[:n], want) {
				t.Fatal(n, err)
			}
		})
	}
}
func TestUDPLiteBoundsDeadlineAndJoinedClose(t *testing.T) {
	receiver, client := udpLiteFixture(t, net.IPv4(127, 0, 0, 1)), udpLiteFixture(t, net.IPv4(127, 0, 0, 1))
	if _, err := client.WriteTo(bytes.Repeat([]byte("x"), 2000), receiver.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if n, _, err := receiver.ReadFrom(make([]byte, 1001)); err != nil || n != 1001 {
		t.Fatal(n, err)
	}
	joined := make(chan error, 1)
	go func() { _, _, err := receiver.ReadFrom(make([]byte, 1001)); joined <- err }()
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-joined:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not join read")
	}
}

func TestUDPLiteRejectsUnsafeEndpointAndReplies(t *testing.T) {
	for _, address := range []*net.UDPAddr{nil, {IP: net.IPv4bcast, Port: 9001}, {IP: net.IPv4(239, 1, 2, 3), Port: 9001}, {IP: net.IPv4(127, 0, 0, 1), Port: -1}, {IP: net.IPv4(127, 0, 0, 1), Port: 65536}, {IP: net.IPv6loopback, Zone: "lo", Port: 9001}, {IP: net.ParseIP("fe80::1"), Zone: "airc-no-such-interface", Port: 9001}} {
		if conn, err := ListenUDPLite(address); err == nil {
			conn.Close()
			t.Fatal("accepted unsafe endpoint", address)
		}
	}
	owner := udpLiteFixture(t, net.IPv4(127, 0, 0, 1))
	for _, destination := range []net.Addr{nil, (*net.UDPAddr)(nil), &net.UDPAddr{IP: net.IPv4bcast, Port: 9001}, &net.UDPAddr{IP: net.IPv4zero, Port: 9001}, &net.UDPAddr{IP: net.IPv6loopback, Port: 9001}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}, &net.UDPAddr{IP: net.IPv4(239, 1, 2, 3), Port: 9001}} {
		if _, err := owner.WriteTo([]byte("unsafe"), destination); err == nil {
			t.Fatal("accepted unsafe reply", destination)
		}
	}
}
