//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRoomUpdateAcrossEncryptedTransportsAndReceipt(t *testing.T) {
	f := newReactionTransportFixtureWithAdmin(t, true)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	if err := admin.Raw("MODE #gateway-fixture +o fixture-author"); err != nil {
		t.Fatal(err)
	}
	if err := admin.Raw("MODE #gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	grant := next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "783" })
	if !strings.Contains(strings.Join(grant.Params, " "), "fixture-author") {
		t.Fatalf("fixture-author operator grant was not visible: %+v", grant)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "324" })
	seconds, limit := int64(5), 20
	request := core.Request{Version: 1, RequestID: "room-update", Operation: core.RoomUpdate, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Room: &core.RoomPatch{SlowSeconds: &seconds, HistoryLimit: &limit}}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request.IssuedAt, request.ExpiresAt = &issued, &expiry
	token := f.cred.validityToken(t, request, issued, expiry, true)
	for _, call := range []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(s string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, s) }},
		{"framed", func(s string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, s, "room-update-framed")
		}},
		{"websocket", func(s string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, s, "room-update-websocket")
		}},
	} {
		status, result := call.fn(token)
		if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil {
			t.Fatalf("%s room update failed: status=%d result=%+v", call.name, status, result)
		}
	}
	var receipt core.Result
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, result := reactionFramedCall(t, f.framed, f.cred.token(t, core.Request{Version: 1, RequestID: "room-update-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, true), "room-update-receipt")
		if status == http.StatusOK && result.Result != nil && result.Result.Room != nil {
			receipt = *result.Result
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("room update receipt unavailable: status=%d result=%+v", status, result)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if receipt.Room == nil || receipt.Room.Room.SlowSeconds != seconds || receipt.Room.Room.HistoryLimit != limit || receipt.MessageID != "" || receipt.Persisted != nil || receipt.RecipientConnected != nil {
		t.Fatalf("room receipt mismatch: %+v", receipt)
	}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, core.Request{Version: 1, RequestID: "room-update-read", Operation: core.RoomRead, Target: request.Target}, true))
	if status != http.StatusOK || result.Room == nil || result.Room.SlowSeconds != seconds || result.Room.HistoryLimit != limit {
		t.Fatalf("room read did not reflect update: status=%d result=%+v", status, result)
	}
}
