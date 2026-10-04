//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRoomReadAcrossEncryptedTransportsAndBearer(t *testing.T) {
	f := newReactionTransportFixtureWithAdmin(t, true)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	if err := admin.Chat(irc.ChatRequest{Action: "room", Target: "#gateway-fixture", Seconds: 17, Limit: 12}); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.ChatEvent) bool { return e.Action == "room" && e.SlowSeconds == 17 && e.HistoryLimit == 12 })
	next(t, admin, func(e *irc.EndOfChatEvent) bool { return true })
	request := core.Request{Version: 1, RequestID: "room-read", Operation: core.RoomRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	expected := core.RoomResult{Target: *request.Target, SlowSeconds: 17, HistoryLimit: 12}
	for _, call := range []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "room-framed")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "room-websocket")
		}},
	} {
		status, result := call.fn(f.cred.token(t, request, true))
		if status != http.StatusOK || result.Room == nil || result.Room.Target != *request.Target {
			t.Fatalf("%s room read failed: status=%d result=%+v", call.name, status, result)
		}
		if *result.Room != expected {
			t.Fatalf("%s room snapshot differs: got=%+v want=%+v", call.name, *result.Room, expected)
		}
	}
	query := "/v1/room?kind=room&target=" + url.QueryEscape(request.Target.Name)
	status, body := bearerRoom(t, f, query)
	if status != http.StatusOK {
		t.Fatalf("bearer room read failed: status=%d body=%s", status, body)
	}
	var room core.RoomResult
	if err := json.Unmarshal(body, &room); err != nil || room != expected {
		t.Fatalf("bearer room mismatch: err=%v room=%+v expected=%+v", err, room, expected)
	}
	checkRoomWaitingHTTP2(t, f, request, query, expected)
	for _, bad := range []string{query + "&unknown=x", query + "&target=%23other-private", "/v1/room?kind=direct&target=fixture-author"} {
		if status, _ := bearerRoom(t, f, bad); status == http.StatusOK {
			t.Fatalf("invalid room query accepted: %s", bad)
		}
	}
	if status, _ := bearerReadToken(t, f, query, "not-a-token"); status != http.StatusUnauthorized {
		t.Fatalf("invalid room bearer status=%d, want 401", status)
	}
	if status, _ := bearerRead(t, f, "/v1/requests/room-read"); status != http.StatusNotFound {
		t.Fatalf("room read created delivery receipt: status=%d", status)
	}
}
