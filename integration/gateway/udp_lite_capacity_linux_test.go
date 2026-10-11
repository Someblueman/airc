//go:build linux

package gateway_test

import (
	"net"
	"testing"
)

func TestUDPLiteOversizedHistoryReturnsBoundedPageAndCanResume(t *testing.T) {
	exercisePacketHistoryCapacity(t, newUDPLiteFixture(t))
}

func TestUDPLiteActiveWatchAndIncompleteRequestJoinOnSIGINT(t *testing.T) {
	exercisePacketWatchShutdown(t, newUDPLiteFixture(t), func(port int) (*net.UDPConn, error) {
		return bindNativeUDPLiteFixtureSocket([4]byte{}, port)
	})
}
