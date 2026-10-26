//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
)

func readUDPResponse(t *testing.T, f udpFixture, deadline time.Time) adapter.FrameResponse {
	t.Helper()
	return decodeFrameResponse(t, readAGD1Payload(t, f.mailbox, deadline))
}

// decodeFrameResponse decodes one plain response frame.
func decodeFrameResponse(t *testing.T, payload []byte) adapter.FrameResponse {
	t.Helper()
	var response adapter.FrameResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	return response
}
