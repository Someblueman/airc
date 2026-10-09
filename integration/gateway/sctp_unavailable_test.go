//go:build !linux || !arm64

package gateway_test

import "testing"

func TestSCTPNativeOrderedAcceptanceRequiresLinuxARM64(t *testing.T) {
	t.Skip("native ordered SCTP acceptance is qualified only on Linux ARM64")
}
