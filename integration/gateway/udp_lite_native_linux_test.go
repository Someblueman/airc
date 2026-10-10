//go:build linux

package gateway_test

import "testing"

func TestUDPLiteNativeUnicodeCursorAndReplay(t *testing.T) {
	exercisePacketNativeUnicodeCursorAndReplay(t, newUDPLiteFixture(t), "udp-lite")
}
