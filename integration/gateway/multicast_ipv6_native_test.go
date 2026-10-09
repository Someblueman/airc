//go:build darwin || linux

package gateway_test

import "testing"

func TestIPv6MulticastNativeUnicodeCursorAndReplay(t *testing.T) {
	exercisePacketNativeUnicodeCursorAndReplay(t, newIPv6MulticastFixture(t), "mcast6")
}
