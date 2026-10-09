//go:build !linux || !arm64

package gateway_test

import "testing"

func TestSCTPNativeOrderedAndUnorderedAcceptanceRequiresLinuxARM64(t *testing.T) {
	t.Skip("native ordered and unordered SCTP acceptance is qualified only on Linux ARM64")
}
