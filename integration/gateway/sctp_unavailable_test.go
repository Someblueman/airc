//go:build !linux || !arm64

package gateway_test

import "testing"

func TestSCTPNativeOrderedUnorderedAndPRTTLAcceptanceRequiresLinuxARM64(t *testing.T) {
	t.Skip("native ordered, unordered, and PR-TTL SCTP acceptance is qualified only on Linux ARM64")
}
