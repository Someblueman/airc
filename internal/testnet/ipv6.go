package testnet

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

// RequireIPv6Loopback skips the test when the host cannot bind the IPv6
// loopback address: a kernel built without IPv6, or no ::1 configured.
func RequireIPv6Loopback(t testing.TB) {
	t.Helper()
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err == nil {
		_ = listener.Close()
		return
	}
	if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EPROTONOSUPPORT) {
		t.Skip("the host has no IPv6 loopback:", err)
	}
	t.Fatal(err)
}
