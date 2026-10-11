//go:build darwin || linux

package gateway_test

import "testing"

func TestIPv6MulticastOversizedHistoryReturnsBoundedPageAndCanResume(t *testing.T) {
	exercisePacketHistoryCapacity(t, newIPv6MulticastFixture(t))
}

func TestIPv6MulticastActiveWatchAndIncompleteRequestJoinOnSIGINT(t *testing.T) {
	exercisePacketWatchShutdown(t, newIPv6MulticastFixture(t), listenIPv6MulticastShutdownPort)
}
