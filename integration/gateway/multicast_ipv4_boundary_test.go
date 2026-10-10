//go:build darwin || linux

package gateway_test

import "testing"

func TestIPv4MulticastUnverifiedInputHasNoResponseOrNativeIntake(t *testing.T) {
	exercisePacketUnverifiedInput(t, newIPv4MulticastFixture(t))
}
