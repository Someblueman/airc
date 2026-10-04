//go:build darwin || linux

package gateway_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
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
		t.Fatalf("raw QUIC response=%+v", response)
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
	send := core.Request{Version: 1, RequestID: "quic-send", Operation: core.Send, Target: room, Message: "raw QUIC whole body 🙂", IssuedAt: &issued, ExpiresAt: &expiry}
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
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 2 || !reflect.DeepEqual(historyResponse.Result.Page.Messages, native) || native[0].ID != first.MessageID || native[0].Message != send.Message || native[0].From != "fixture-author" || native[0].Target != room.Name || native[1].ID != accepted.MessageID || native[1].Message != reply.Message || native[1].ReplyTo != first.MessageID || native[1].ThreadID != first.MessageID {
		t.Fatalf("raw QUIC replay/send/reply native facts=%+v", native)
	}
}

func TestRawQUICUniStreamRoundTrip(t *testing.T) {
	f := newRawQUICFixture(t, false)
	request := core.Request{Version: 1, RequestID: "quic-uni", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "raw QUIC uni 🙂"}
	stream, err := f.conn.OpenUniStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(rawQUICFrame(t, rawQUICRequest(t, f, request, true, false))); err != nil {
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
	result := rawQUICAssertResult(t, readRawQUICFrame(t, responseStream), request.RequestID)
	if !result.Durable {
		t.Fatalf("raw QUIC uni admission=%+v", result)
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

func TestRawQUICTamperedEnvelopeReturnsAuthenticationFailure(t *testing.T) {
	f := newRawQUICFixture(t, false)
	request := core.Request{Version: 1, RequestID: "quic-tampered", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
	frame := rawQUICRequest(t, f, request, false, false)
	if len(frame.Envelope) < 2 {
		t.Fatal("fixture produced an unexpectedly short envelope")
	}
	replacement := byte('A')
	if frame.Envelope[len(frame.Envelope)-1] == replacement {
		replacement = 'B'
	}
	frame.Envelope = frame.Envelope[:len(frame.Envelope)-1] + string(replacement)
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Write(rawQUICFrame(t, frame)); err != nil {
		t.Fatal(err)
	}
	response := readRawQUICFrame(t, stream)
	if response.Event != "error" || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
		t.Fatalf("raw QUIC tamper response=%+v", response)
	}
	rpcNoJobs(t, f.config.StateDir)
}
