//go:build darwin || linux

package gateway_test

import "testing"

func TestIPv6MulticastUnverifiedInputHasNoResponseOrNativeIntake(t *testing.T) {
	exercisePacketUnverifiedInput(t, newIPv6MulticastFixture(t))
}
