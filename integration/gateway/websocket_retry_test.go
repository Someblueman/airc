//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/coder/websocket"
)

func TestWebSocketCLIStableRetryAfterDisconnect(t *testing.T) {
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", nil)
	request := core.Request{Version: 1, RequestID: "disconnected-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "one durable intent across disconnected WebSockets"}
	token := f.credentials.token(t, request, true)
	first := adapter.FrameRequest{Version: 1, CorrelationID: "before", Profile: "encrypted", Envelope: token}
	body, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	err = f.conn.Write(ctx, websocket.MessageText, body)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	// Disconnect without observing intake. Repeat the exact intent, not a new timestamp.
	_ = f.conn.CloseNow()
	retry := websocketDial(t, f, nil)
	defer retry.CloseNow()
	first.CorrelationID = "after"
	websocketWrite(t, retry, first)
	if r := websocketRead(t, retry); r.Result == nil || r.Result.Result == nil || !r.Result.Result.Durable {
		t.Fatal("retry was not durably admitted", r)
	}
	var accepted core.Result
	for n := 0; n < 50; n++ {
		status := core.Request{Version: 1, RequestID: "status", Operation: core.ReceiptGet, LookupID: request.RequestID}
		websocketWrite(t, retry, adapter.FrameRequest{Version: 1, CorrelationID: "status-" + strconv.Itoa(n), Profile: "encrypted", Envelope: f.credentials.token(t, status, true)})
		r := websocketRead(t, retry)
		if r.Result == nil || r.Result.Result == nil {
			t.Fatal("receipt lookup lost durable retry", r)
		}
		if r.Result.Result.State == core.Accepted {
			accepted = *r.Result.Result
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if accepted.MessageID == "" || accepted.SendAttempts != 1 {
		t.Fatal("disconnect retried native action", accepted)
	}
	history := core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: request.Target, Limit: 10, Start: "retained"}
	websocketWrite(t, retry, adapter.FrameRequest{Version: 1, CorrelationID: "history", Profile: "encrypted", Envelope: f.credentials.token(t, history, true)})
	r := websocketRead(t, retry)
	if r.Result == nil || r.Result.Page == nil || len(r.Result.Page.Messages) != 1 || r.Result.Page.Messages[0].ID != accepted.MessageID || r.Result.Page.Messages[0].Message != request.Message {
		t.Fatal("native history contains missing/duplicate action", r)
	}
	f.stop()
}
