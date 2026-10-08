//go:build linux

package gateway_test

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"testing"
)

func newUDPLiteFixture(t *testing.T) udpFixture {
	t.Helper()
	return newPacketFixtureConfigured(t, "udp_lite", func(c *config.Config, _, _ *net.UDPConn) {
		c.Listeners[0].ID = "udp_lite"
		c.Listeners[0].UDPLite = &config.UDPLite{ChecksumCoverage: "full"}
		c.ReturnRoutes[0].Listener = "udp_lite"
	}, listenNativeUDPLiteFixtureSocket)
}

// Native test clients own their protocol-136 descriptors independently of the service.
func listenNativeUDPLiteFixtureSocket() (*net.UDPConn, error) {
	return bindNativeUDPLiteFixtureSocket([4]byte{127, 0, 0, 1}, 0)
}

func bindNativeUDPLiteFixtureSocket(address [4]byte, port int) (*net.UDPConn, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDPLITE)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "native-udp-lite-client")
	defer file.Close()
	for _, option := range []int{10, 11} {
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
	if !ok {
		packet.Close()
		return nil, errors.New("native UDP-Lite descriptor did not expose packet ownership")
	}
	return conn, nil
}
