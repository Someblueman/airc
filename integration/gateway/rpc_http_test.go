//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRPCCapabilitiesAndIDsOverHTTP1AndHTTP2(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", http2), func(t *testing.T) {
			f := newRPCHTTPFixture(t, http2)
			body := rpcRequestBody(t, "cap-string", "airc.capabilities", map[string]any{})
			response, data := rpcHTTPSend(t, f, "/v1/rpc", body, http2)
			wantProto := 1
			if http2 {
				wantProto = 2
			}
			if response.StatusCode != http.StatusOK || response.ProtoMajor != wantProto {
				t.Fatalf("RPC capabilities status/protocol=%d/%d want 200/%d", response.StatusCode, response.ProtoMajor, wantProto)
			}
			decoded := decodeRPC(t, data)
			assertRPCID(t, decoded.ID, "cap-string")
			if len(decoded.Result) == 0 || len(decoded.Error) != 0 {
				t.Fatalf("capabilities response=%s error=%s", data, rpcErrorText(t, decoded))
			}
			var capabilities struct {
				Operations []core.Operation `json:"operations"`
			}
			if err := json.Unmarshal(decoded.Result, &capabilities); err != nil || !slices.Equal(capabilities.Operations, dispatch.ImplementedOperations()) {
				t.Fatalf("capabilities operations=%s err=%v", decoded.Result, err)
			}
			numeric, data := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, 17, "airc.capabilities", nil), http2)
			if numeric.StatusCode != http.StatusOK {
				t.Fatalf("numeric capabilities status=%d body=%s", numeric.StatusCode, data)
			}
			assertRPCID(t, decodeRPC(t, data).ID, 17)
			nullID, data := rpcHTTPSend(t, f, "/v1/rpc", rpcNullIDBody(t, "airc.capabilities", nil), http2)
			if nullID.StatusCode != http.StatusOK {
				t.Fatalf("null-id capabilities status=%d body=%s", nullID.StatusCode, data)
			}
			assertRPCID(t, decodeRPC(t, data).ID, json.RawMessage("null"))
		})
	}
}

func TestRPCRequestMutationReplayAndNotification(t *testing.T) {
	f := newRPCHTTPFixture(t, false)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "rpc-replay-send", Operation: core.Send, Target: room, Message: "rpc whole body\n🙂"}
	first, firstBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, "first", "airc.request", rpcRequestParams(request)), false)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("RPC send status=%d body=%s", first.StatusCode, firstBody)
	}
	firstResult := decodeRPC(t, firstBody)
	if len(firstResult.Error) != 0 || len(firstResult.Result) == 0 {
		t.Fatalf("RPC send response=%s", firstBody)
	}
	assertRPCID(t, firstResult.ID, "first")
	var admitted core.Result
	if json.Unmarshal(firstResult.Result, &admitted) != nil || !admitted.Durable || admitted.RequestID != request.RequestID {
		t.Fatal("RPC admission lost durable ID", admitted)
	}
	observed := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == request.Message })
	if observed.Target != room.Name || observed.From != "fixture-author" {
		t.Fatalf("native RPC send=%+v", observed)
	}
	var accepted core.Result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		receiptResponse, receiptBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, "receipt-before-replay", "airc.request", rpcRequestParams(core.Request{Version: 1, RequestID: "rpc-receipt-before-replay", Operation: core.ReceiptGet, LookupID: request.RequestID})), false)
		if receiptResponse.StatusCode != http.StatusOK {
			t.Fatalf("RPC receipt status=%d body=%s", receiptResponse.StatusCode, receiptBody)
		}
		got := decodeRPC(t, receiptBody)
		if len(got.Error) == 0 && json.Unmarshal(got.Result, &accepted) == nil && accepted.State == core.Accepted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if accepted.State != core.Accepted || accepted.MessageID != observed.ID {
		t.Fatalf("RPC send was not accepted before replay: %+v", accepted)
	}
	second, secondBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, 2, "airc.request", rpcRequestParams(request)), false)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("RPC replay status=%d body=%s", second.StatusCode, secondBody)
	}
	secondResult := decodeRPC(t, secondBody)
	assertRPCID(t, secondResult.ID, 2)
	var replay core.Result
	if len(secondResult.Error) != 0 || json.Unmarshal(secondResult.Result, &replay) != nil || replay.RequestID != request.RequestID || replay.MessageID != accepted.MessageID || replay.SendAttempts != 1 || replay.State != core.Accepted || !replay.Durable {
		t.Fatalf("RPC replay changed durable identity: first=%s accepted=%+v second=%s", firstResult.Result, accepted, secondResult.Result)
	}

	notification := core.Request{Version: 1, RequestID: "rpc-notification-send", Operation: core.Send, Target: room, Message: "rpc notification"}
	status, body := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, nil, "airc.request", rpcRequestParams(notification)), false)
	if status.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("notification status/body=%d/%q", status.StatusCode, body)
	}
	notified := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == notification.Message })
	if notified.Target != room.Name || notified.From != "fixture-author" {
		t.Fatalf("native notification=%+v", notified)
	}
	history := core.Request{Version: 1, RequestID: "rpc-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	read := func(id any) backend.HistoryPage {
		t.Helper()
		response, body := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, id, "airc.request", rpcRequestParams(history)), false)
		decoded := decodeRPC(t, body)
		assertRPCID(t, decoded.ID, id)
		var page backend.HistoryPage
		if response.StatusCode != http.StatusOK || len(decoded.Error) != 0 || json.Unmarshal(decoded.Result, &page) != nil {
			t.Fatal("RPC bounded history failed", string(body))
		}
		return page
	}
	for i, native := range []*irc.MessageEvent{observed, notified} {
		page := read(fmt.Sprint("page-", i))
		want := "more"
		if i == 1 {
			want = "ok"
		}
		if len(page.Messages) != 1 || page.Messages[0].ID != native.ID || page.Messages[0].Message != native.Message || page.Messages[0].From != native.From || page.Messages[0].Target != native.Target || page.Status != want || page.Cursor == "" || page.Gap {
			t.Fatal("RPC bounded native record mismatch", page)
		}
		history.Start, history.Cursor = "", page.Cursor
	}
	if page := read(23); len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
		t.Fatal("RPC caught-up cursor mismatch", page)
	}

}

func TestRPCBatchAndProtocolErrors(t *testing.T) {
	f := newRPCHTTPFixture(t, false)
	batch := []json.RawMessage{
		rpcRequestBody(t, "batch-one", "airc.capabilities", map[string]any{}),
		rpcRequestBody(t, 2, "airc.capabilities", map[string]any{}),
	}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	response, data := rpcHTTPSend(t, f, "/v1/rpc", body, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", response.StatusCode, data)
	}
	var replies []rpcResponse
	if err := json.Unmarshal(data, &replies); err != nil || len(replies) != 2 {
		t.Fatalf("batch replies=%s err=%v", data, err)
	}
	assertRPCID(t, replies[0].ID, "batch-one")
	assertRPCID(t, replies[1].ID, 2)
	for _, reply := range replies {
		if len(reply.Error) != 0 || len(reply.Result) == 0 {
			t.Fatalf("batch reply=%+v", reply)
		}
	}

	bad, badBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, "bad", "unknown.method", map[string]any{}), false)
	if bad.StatusCode != http.StatusBadRequest && bad.StatusCode != http.StatusOK {
		t.Fatalf("unknown method status=%d body=%s", bad.StatusCode, badBody)
	}
	badResponse := decodeRPC(t, badBody)
	if len(badResponse.Error) == 0 {
		t.Fatalf("unknown method lacked JSON-RPC error: %s", badBody)
	}
	assertRPCID(t, badResponse.ID, "bad")
	var failure struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(badResponse.Error, &failure); err != nil || failure.Code != -32601 {
		t.Fatalf("unknown method error=%s err=%v", badResponse.Error, err)
	}
}
