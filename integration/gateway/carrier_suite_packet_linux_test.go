//go:build linux

package gateway_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/sys/unix"
)

// UDP-Lite socket options (RFC 3828, Linux udplite.h).
const (
	udpLiteSendCoverage = 10
	udpLiteRecvCoverage = 11
)

// UDP-Lite (IP protocol 136) runs the packet carrier with full checksum
// coverage. Its clients own native UDP-Lite sockets.
var _ = registerSuiteCarriers(packetSuiteCarrier("udp_lite", config.Listener{Protocol: "udp_lite", UDPLite: &config.UDPLite{ChecksumCoverage: "full"}}, packetCarrier{
	configure: func(t *testing.T, l *config.Listener) {
		probe, err := bindUDPLite([4]byte{127, 0, 0, 1}, 0)
		if errors.Is(err, unix.EPROTONOSUPPORT) || errors.Is(err, unix.ESOCKTNOSUPPORT) {
			t.Skip("the kernel has no UDP-Lite:", err)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer probe.Close()
		l.Address = probe.LocalAddr().String()
	},
	socket: func(*testing.T) (*net.UDPConn, error) { return bindUDPLite([4]byte{127, 0, 0, 1}, 0) },
	bind:   func(port int) (io.Closer, error) { return bindUDPLite([4]byte{}, port) },
	wire:   udpLitePartialCoverageWire,
}))

// udpLitePartialCoverageWire: a datagram whose checksum covers only its
// header is never admitted; full coverage is answered again.
func udpLitePartialCoverageWire(t *testing.T, e *suiteEnv, p packetCarrier) {
	t.Run("partial_checksum_coverage", func(t *testing.T) { udpLitePartialCoverage(t, e, p) })
}

func udpLitePartialCoverage(t *testing.T, e *suiteEnv, p packetCarrier) {
	client, err := dialPacketSuite(t, p, e.address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	coverage := func(value int) {
		t.Helper()
		raw, err := client.conn.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var optionErr error
		if err := raw.Control(func(fd uintptr) {
			if optionErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_UDPLITE, udpLiteSendCoverage, value); optionErr == nil {
				var actual int
				if actual, optionErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_UDPLITE, udpLiteSendCoverage); optionErr == nil && actual != value {
					optionErr = fmt.Errorf("send coverage %d, want %d", actual, value)
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if optionErr != nil {
			t.Fatal(optionErr)
		}
	}
	coverage(8)
	id := e.id("partial-coverage")
	client.write(t, id, sendBody(t, id, "#wire", "partial checksum must never post", e.guest("wire"), ""), false)
	client.silent(t)
	e.noJob(t, id)
	coverage(0)
	if r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("full-coverage"), "operation": "capabilities.get"}, e.guest("wire"), ""))); r.result == nil || r.result.Capabilities == nil {
		t.Fatalf("full coverage: %v", r)
	}
	client.silent(t)
}

// bindUDPLite opens a UDP-Lite socket with full send and receive coverage,
// bound to address and port.
func bindUDPLite(address [4]byte, port int) (*net.UDPConn, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDPLITE)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "udp-lite-client")
	defer file.Close()
	for _, option := range []int{udpLiteSendCoverage, udpLiteRecvCoverage} {
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_UDPLITE, option, 0); err != nil {
			return nil, err
		}
	}
	if err := unix.Bind(fd, &unix.SockaddrInet4{Addr: address, Port: port}); err != nil {
		return nil, err
	}
	packet, err := net.FilePacketConn(file)
	if err != nil {
		return nil, err
	}
	conn, ok := packet.(*net.UDPConn)
	if !ok || !strings.HasPrefix(conn.LocalAddr().Network(), "udp") {
		_ = packet.Close()
		return nil, errors.New("UDP-Lite descriptor is not a datagram socket")
	}
	return conn, nil
}
