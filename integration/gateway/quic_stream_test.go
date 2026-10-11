//go:build darwin || linux

package gateway_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func rawQUICBidiCall(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	defer stream.CancelRead(0)
	if err := stream.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(rawQUICFrame(t, rawQUICRequest(t, f, request, encrypted, false))); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	response := readRawQUICFrame(t, stream)
	if response.Version != 1 || response.CorrelationID != "quic-"+request.RequestID || response.Event != "result" || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("raw QUIC result=%+v", response)
	}
	return response
}

func rawQUICAssertResult(t *testing.T, response adapter.FrameResponse, id string) core.Result {
	t.Helper()
	if response.Version != 1 || response.CorrelationID != "quic-"+id || response.Event != "result" || response.Result == nil || response.Result.Result == nil {
		t.Fatalf("raw QUIC response=%+v error=%+v", response, response.Error)
	}
	result := *response.Result.Result
	if result.RequestID != id {
		t.Fatalf("raw QUIC request id=%q want=%q", result.RequestID, id)
	}
	return result
}

func rawQUICAccepted(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	first := rawQUICAssertResult(t, rawQUICBidiCall(t, f, request, encrypted), request.RequestID)
	if !first.Durable {
		t.Fatalf("raw QUIC admission=%+v", first)
	}
	var last core.Result
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		id := request.RequestID + "-receipt"
		receipt := core.Request{Version: 1, RequestID: id, Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := rawQUICBidiCall(t, f, receipt, encrypted)
		if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("raw QUIC receipt response=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("raw QUIC request did not reach accepted: %+v", last)
	return core.Result{}
}

func TestRawQUICBidiSignedEncryptedSendReplyReceiptReplay(t *testing.T) {
	f := newRawQUICFixture(t, false)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	send := core.Request{Version: 1, RequestID: "quic-send", Operation: core.Send, Target: room, Message: "raw QUIC whole body\n🙂", IssuedAt: &issued, ExpiresAt: &expiry}
	first := rawQUICAccepted(t, f, send, false)
	if first.MessageID == "" {
		t.Fatal("raw QUIC send had no native message ID")
	}
	second := rawQUICAccepted(t, f, send, true)
	if second.MessageID != first.MessageID || second.SendAttempts != 1 {
		t.Fatalf("raw QUIC replay changed durable result: first=%+v second=%+v", first, second)
	}
	reply := core.Request{Version: 1, RequestID: "quic-reply", Operation: core.Reply, Target: room, ParentID: first.MessageID, Message: "raw QUIC reply 🙂"}
	accepted := rawQUICAccepted(t, f, reply, true)
	if accepted.MessageID == "" {
		t.Fatal("raw QUIC reply had no native message ID")
	}
	historyResponse := rawQUICBidiCall(t, f, core.Request{Version: 1, RequestID: "quic-history", Operation: core.HistoryRead, Target: room, Start: "recent", Limit: 16}, true)
	if historyResponse.Result == nil || historyResponse.Result.Page == nil {
		t.Fatalf("raw QUIC history omitted send/reply: %+v", historyResponse.Result)
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != 2 || !reflect.DeepEqual(historyResponse.Result.Page.Messages, native) || native[0].ID != first.MessageID || native[0].Message != send.Message || native[0].From != "fixture-author" || native[0].Target != room.Name || native[1].ID != accepted.MessageID || native[1].Message != reply.Message || native[1].ReplyTo != first.MessageID || native[1].ThreadID != first.MessageID {
		t.Fatalf("raw QUIC replay/send/reply native facts=%+v", native)
	}
	history := core.Request{Version: 1, RequestID: "quic-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, message := range native {
		page := rawQUICBidiCall(t, f, history, true).Result.Page
		want := "more"
		if i == len(native)-1 {
			want = "ok"
		}
		if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], message) || page.Status != want || page.Cursor == "" || page.Gap {
			t.Fatal("raw QUIC bounded native page mismatch", page)
		}
		history.Start, history.Cursor = "", page.Cursor
	}
	for range 2 {
		page := rawQUICBidiCall(t, f, history, true).Result.Page
		if page == nil || len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
			t.Fatal("raw QUIC caught-up resume mismatch", page)
		}
		history.Cursor = page.Cursor
	}

}

func TestRawQUICUniStreamRoundTrip(t *testing.T) {
	f := newRawQUICFixture(t, false)
	call := func(request core.Request) adapter.FrameResponse {
		t.Helper()
		stream, err := f.conn.OpenUniStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		frame := rawQUICRequest(t, f, request, true, false)
		if err := stream.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(rawQUICFrame(t, frame)); err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		responseStream, err := f.conn.AcceptUniStream(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer responseStream.CancelRead(0)
		if err := responseStream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		response := readRawQUICFrame(t, responseStream)
		if response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
			t.Fatal("paired uni response mismatch", response)
		}
		return response
	}
	body := make([]byte, 3500)
	for i := range body {
		body[i] = 'x'
	}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "uni-large", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: string(body), IssuedAt: &issued, ExpiresAt: &expiry}
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

func TestRawQUICRejectsZeroLengthFrame(t *testing.T) {
	f := newRawQUICFixture(t, false)
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if _, err := stream.Read(make([]byte, 1)); err == nil {
		t.Fatal("raw QUIC zero frame unexpectedly produced a response")
	}
	rpcNoJobs(t, f.config.StateDir)
}

func TestRawQUICInvalidRequestReturnsError(t *testing.T) {
	f := newRawQUICFixture(t, false)
	request := core.Request{Version: 1, RequestID: "quic-tampered", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
	frame := rawQUICRequest(t, f, request, false, false)
	frame.Request = `{"version":1,"request_id":"quic-tampered","operation":"send"}`
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Write(rawQUICFrame(t, frame)); err != nil {
		t.Fatal(err)
	}
	response := readRawQUICFrame(t, stream)
	if response.Event != "error" || response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatalf("raw QUIC invalid request response=%+v", response)
	}
	rpcNoJobs(t, f.config.StateDir)
	if messages := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatalf("invalid QUIC request posted native messages: %+v", messages)
	}
}
