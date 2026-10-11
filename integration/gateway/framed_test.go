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
			conn, credentials, _, stop := framedFixture(t, secure)
			duplexOperations(t, credentials, secure, "framed_stream", 3, func(r frameRequest) { writeFrame(t, conn, r) }, func() adapter.FrameResponse { return readFrame(t, conn) }, stop)
		})
	}
}
