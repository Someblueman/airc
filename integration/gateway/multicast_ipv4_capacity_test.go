//go:build darwin || linux

package gateway_test

import "testing"

func TestIPv4MulticastOversizedHistoryReturnsEncryptedCapacityAndCanResume(t *testing.T) {
	exercisePacketHistoryCapacity(t, newIPv4MulticastFixture(t))
}

func TestIPv4MulticastActiveWatchAndIncompleteRequestJoinOnSIGINT(t *testing.T) {
	exercisePacketWatchShutdown(t, newIPv4MulticastFixture(t), listenUDPShutdownPort)
}
