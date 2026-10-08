//go:build darwin || linux

package gateway_test

import "testing"

func TestIPv4MulticastNativeUnicodeCursorAndReplay(t *testing.T) {
	exercisePacketNativeUnicodeCursorAndReplay(t, newIPv4MulticastFixture(t), "mcast")
}
