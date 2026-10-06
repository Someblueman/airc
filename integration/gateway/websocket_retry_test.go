//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

func TestWebSocketCLIStableRetryAfterDisconnect(t *testing.T) {
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", nil)
	if !strings.HasPrefix(f.base, "ws://") {
		t.Fatal("fixture did not use plaintext WebSocket", f.base)
	}
	observer := dial(t, f.socket, "websocket-retry-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "websocket-retry-observer" })
	request := core.Request{Version: 1, RequestID: "disconnected-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "one durable intent\nacross disconnected WebSockets 🙂"}
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
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatal("WebSocket native observer mismatch", seen)
	}
	history := core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: request.Target, Limit: 1, Start: "retained"}
	websocketWrite(t, retry, adapter.FrameRequest{Version: 1, CorrelationID: "history", Profile: "encrypted", Envelope: f.credentials.token(t, history, true)})
	r := websocketRead(t, retry)
	if r.Result == nil || r.Result.Page == nil || len(r.Result.Page.Messages) != 1 || r.Result.Page.Messages[0].ID != accepted.MessageID || r.Result.Page.Messages[0].Message != seen.Message || r.Result.Page.Messages[0].From != seen.From || r.Result.Page.Messages[0].Target != seen.Target || r.Result.Page.Status != "ok" || r.Result.Page.Cursor == "" || r.Result.Page.Gap {
		t.Fatal("native history contains missing/duplicate action", r)
	}
	history.Start, history.Cursor = "", r.Result.Page.Cursor
	websocketWrite(t, retry, adapter.FrameRequest{Version: 1, CorrelationID: "caught-up", Profile: "encrypted", Envelope: f.credentials.token(t, history, true)})
	r = websocketRead(t, retry)
	if r.CorrelationID != "caught-up" || r.Error != nil || r.Result == nil || r.Result.Page == nil || len(r.Result.Page.Messages) != 0 || r.Result.Page.Status != "ok" || r.Result.Page.Cursor == "" || r.Result.Page.Gap {
		t.Fatal("WebSocket bounded cursor resume mismatch", r)
	}
	f.stop()
}
