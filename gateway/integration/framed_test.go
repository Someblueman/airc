//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/adapter"
)

// The correlated transport shared by framed, Unix and WebSocket listeners
// (runCorrelated) runs every operation and multiplexes a watch whose stream
// ends with its read lease. Plaintext framed carriers run TestCarrierSuite.
func TestFramedCLISharedOperationsAndMultiplexedWatch(t *testing.T) {
	t.Parallel()
	shortGatewayTimer(t, readLeaseTimer)
	conn, credentials, _, stop := framedFixture(t, true)
	duplexOperations(t, credentials, true, "framed_stream", 3, func(r frameRequest) { writeFrame(t, conn, r) }, func() adapter.FrameResponse { return readFrame(t, conn) }, stop)
}
