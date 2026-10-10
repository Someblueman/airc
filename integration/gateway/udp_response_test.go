//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"testing"
	"time"
)

func readUDPResponse(t *testing.T, f udpFixture, deadline time.Time) adapter.FrameResponse {
	t.Helper()
	token := string(readAGD1Payload(t, f.mailbox, deadline))
	plain, err := auth.OpenResponse(token, "udp-response", f.responseSigning, "udp-reader", f.responseRecipient, auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: "udp-return"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	return response
}
