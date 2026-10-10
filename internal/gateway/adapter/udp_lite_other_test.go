//go:build !linux

package adapter

import (
	"net"
	"testing"
)

func TestUDPLiteUnsupportedPlatformDoesNotFallback(t *testing.T) {
	if conn, err := ListenUDPLite(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}); err == nil {
		conn.Close()
		t.Fatal("UDP-Lite silently fell back on unsupported platform")
	}
}
