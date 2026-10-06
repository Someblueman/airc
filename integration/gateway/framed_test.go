//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

func TestFramedCLISharedOperationsAndMultiplexedWatch(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			conn, credentials, stop := framedFixture(t, secure)
			terminalSeconds := int64(3)
			if secure {
				terminalSeconds = 5
			}
			duplexOperations(t, credentials, secure, "framed_stream", terminalSeconds, func(r adapter.FrameRequest) { writeFrame(t, conn, r) }, func() adapter.FrameResponse { return readFrame(t, conn) }, stop)
		})
	}
}
