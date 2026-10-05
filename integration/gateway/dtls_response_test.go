//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/pion/dtls/v3"
	"testing"
	"time"
)

func readDTLSResponse(t *testing.T, conn *dtls.Conn, readDeadline time.Time) adapter.FrameResponse {
	t.Helper()
	data := readAGD1Payload(t, conn, readDeadline)
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	return response
}
