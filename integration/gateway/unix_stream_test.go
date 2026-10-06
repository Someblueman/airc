//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

func TestUnixStreamSharedOperations(t *testing.T) {
	conn, credentials, stop, _, _ := unixStreamFixture(t)
	defer conn.Close()
	duplexOperations(t, credentials, false, "framed_stream", 3, func(r adapter.FrameRequest) {
		writeFrame(t, conn, r)
	}, func() adapter.FrameResponse {
		return readFrame(t, conn)
	}, stop)
}
