//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func webTransportBidiCall(t *testing.T, f webTransportFixture, request core.Request, encrypted bool) dispatch.DetachedResult {
	t.Helper()
	stream, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	frame := adapterFrameRequest(t, f, request, encrypted)
	if _, err := stream.Write(webTransportFrame(t, frame)); err != nil {
		t.Fatal(err)
	}
	response := readWebTransportFrame(t, stream)
	if response.Event != "result" || response.Result == nil || response.Result.Operation != request.Operation || response.CorrelationID != frame.CorrelationID {
		if response.Error != nil {
			t.Fatalf("WebTransport %s response error=%s upstream=%d", request.Operation, response.Error.Code, response.Error.UpstreamCode)
		}
		t.Fatalf("WebTransport %s response=%+v", request.Operation, response)
	}
	return *response.Result
}

func adapterFrameRequest(t *testing.T, f webTransportFixture, request core.Request, encrypted bool) frameRequest {
	t.Helper()
	return frameRequest{Version: 1, CorrelationID: "read-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, encrypted)}
}

func nativeWebTransportMessages(t *testing.T, socket, room string) []*irc.MessageMetadata {
	t.Helper()
	probe := dial(t, socket, "webtransport-check", "", false, true)
	defer probe.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, probe, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: room, After: "*", Limit: 16}}, IncludeOwn: true, MaxMessages: 16}, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]*irc.MessageMetadata, 0, len(entries))
	for _, entry := range entries {
		if entry.Message != nil {
			messages = append(messages, entry.Message)
		}
	}
	return messages
}
