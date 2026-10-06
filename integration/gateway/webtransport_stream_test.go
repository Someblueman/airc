//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func webTransportAccepted(t *testing.T, f webTransportFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	initial := webTransportBidiCall(t, f, request, encrypted)
	if initial.Result == nil || !initial.Result.Durable {
		t.Fatalf("WebTransport %s admission=%+v", request.Operation, initial.Result)
	}
	var last core.Result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		receipt := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: request.RequestID + "-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, encrypted)
		if receipt.Result != nil {
			last = *receipt.Result
		}
		if receipt.Result != nil && receipt.Result.State == core.Accepted {
			return *receipt.Result
		}
		time.Sleep(40 * time.Millisecond)
	}
	if last.Failure != nil {
		t.Fatalf("WebTransport %s did not reach accepted: state=%s failure=%s upstream=%d last=%+v", request.Operation, last.State, last.Failure.Code, last.Failure.UpstreamCode, last)
	}
	t.Fatalf("WebTransport %s did not reach accepted: last=%+v", request.Operation, last)
	return core.Result{}
}

func webTransportRequest(t *testing.T, f webTransportFixture, id, message string, encrypted bool) adapter.FrameRequest {
	t.Helper()
	request := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: message}
	return adapter.FrameRequest{Version: 1, CorrelationID: "wt-" + id, Profile: map[bool]string{false: "signed", true: "encrypted"}[encrypted], Envelope: f.credentials.token(t, request, encrypted)}
}

func webTransportResponse(t *testing.T, response adapter.FrameResponse, requestID string) {
	t.Helper()
	if response.Version != 1 || response.CorrelationID != "wt-"+requestID || response.Event != "result" || response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != requestID || !response.Result.Result.Durable {
		t.Fatalf("WebTransport response=%+v", response)
	}
}

func TestWebTransportBidiSignedAndEncrypted(t *testing.T) {
	f := newWebTransportFixture(t)
	for _, encrypted := range []bool{false, true} {
		requestID := "bidi-" + map[bool]string{false: "signed", true: "encrypted"}[encrypted]
		stream, err := f.session.OpenStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		frame := webTransportFrame(t, webTransportRequest(t, f, requestID, "webtransport bidi whole body 🙂", encrypted))
		if _, err := stream.Write(frame); err != nil {
			t.Fatal(err)
		}
		response := readWebTransportFrame(t, stream)
		webTransportResponse(t, response, requestID)
		_ = stream.Close()
	}
}

func TestWebTransportUniStreamRoundTripAndLargeBody(t *testing.T) {
	f := newWebTransportFixture(t)
	call := func(request core.Request) adapter.FrameResponse {
		t.Helper()
		stream, err := f.session.OpenUniStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		frame := adapterFrameRequest(t, f, request, true)
		if _, err := stream.Write(webTransportFrame(t, frame)); err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		responseStream, err := f.session.AcceptUniStream(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer responseStream.CancelRead(0)
		response := readWebTransportFrame(t, responseStream)
		if response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
			t.Fatal("paired uni response mismatch", response)
		}
		return response
	}
	body := make([]byte, 3500)
	for i := range body {
		body[i] = 'x'
	}
	request := core.Request{Version: 1, RequestID: "uni-large", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: string(body)}
	admitted := call(request).Result.Result
	if admitted == nil || !admitted.Durable {
		t.Fatal("uni send not durable", admitted)
	}
	var accepted *core.Result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		receipt := call(core.Request{Version: 1, RequestID: "uni-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}).Result.Result
		if receipt != nil && receipt.State == core.Accepted {
			accepted = receipt
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if accepted == nil {
		t.Fatal("uni send did not reach native acceptance")
	}
	native := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
	if native.Message != request.Message || native.From != "fixture-author" || native.Target != request.Target.Name {
		t.Fatal("uni native observer mismatch", native)
	}
	replay := call(request).Result.Result
	if replay == nil || replay.MessageID != native.ID || replay.SendAttempts != 1 {
		t.Fatal("uni replay changed native acceptance", replay)
	}
	history := core.Request{Version: 1, RequestID: "uni-history", Operation: core.HistoryRead, Target: request.Target, Limit: 1, Start: "retained"}
	page := call(history).Result.Page
	if page == nil || len(page.Messages) != 1 || page.Messages[0].ID != native.ID || page.Messages[0].Message != native.Message || page.Messages[0].From != native.From || page.Messages[0].Target != native.Target || page.Status != "ok" || page.Cursor == "" || page.Gap {
		t.Fatal("uni bounded page mismatch", page)
	}
	history.Start, history.Cursor = "", page.Cursor
	empty := call(history).Result.Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("uni cursor resume mismatch", empty)
	}
}

func TestWebTransportReplayKeepsNativeIDAndBody(t *testing.T) {
	f := newWebTransportFixture(t)
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "replay", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "WebTransport replay\n🙂", IssuedAt: &issued, ExpiresAt: &expiry}
	first := webTransportAccepted(t, f, request, true)
	second := webTransportAccepted(t, f, request, true)
	if first.MessageID == "" || first.MessageID != second.MessageID || first.SendAttempts != 1 || second.SendAttempts != 1 {
		t.Fatalf("WebTransport replay changed durable facts: first=%+v second=%+v", first, second)
	}
	native := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == first.MessageID })
	if native.Message != request.Message || native.Target != request.Target.Name || native.From != "fixture-author" {
		t.Fatalf("WebTransport replay native=%+v", native)
	}

	request.RequestID, request.Message = "replay-second", "WebTransport second\nwhole body 🙂"
	accepted := webTransportAccepted(t, f, request, true)
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatal("WebTransport second native mismatch", seen)
	}
	history := core.Request{Version: 1, RequestID: "bidi-bounded", Operation: core.HistoryRead, Target: request.Target, Limit: 1, Start: "retained"}
	for i, observed := range []*irc.MessageEvent{native, seen} {
		page := webTransportBidiCall(t, f, history, true).Page
		want := "more"
		if i == 1 {
			want = "ok"
		}
		if page == nil || len(page.Messages) != 1 || page.Messages[0].ID != observed.ID || page.Messages[0].Message != observed.Message || page.Messages[0].From != observed.From || page.Messages[0].Target != observed.Target || page.Status != want || page.Cursor == "" || page.Gap {
			t.Fatal("WebTransport bidi bounded page mismatch", page)
		}
		history.Start, history.Cursor = "", page.Cursor
	}
	empty := webTransportBidiCall(t, f, history, true).Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("WebTransport bidi cursor resume mismatch", empty)
	}
}
