//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestRoomReadReturnsNativeSettings maps room.read to the room's native slow
// mode and history limit.
func TestRoomReadReturnsNativeSettings(t *testing.T) {
	t.Parallel()
	f := newReactionFixture(t, true, nil, true)
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	if err := admin.Chat(irc.ChatRequest{Action: "room", Target: "#gateway-fixture", Seconds: 17, Limit: 12}); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.ChatEvent) bool { return e.Action == "room" && e.SlowSeconds == 17 && e.HistoryLimit == 12 })
	next(t, admin, func(e *irc.EndOfChatEvent) bool { return true })
	request := core.Request{Version: 1, RequestID: "room-read", Operation: core.RoomRead, Target: contractRoom()}
	expected := core.RoomResult{Target: *request.Target, SlowSeconds: 17, HistoryLimit: 12}
	if status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true)); status != http.StatusOK || result.Room == nil || *result.Room != expected {
		t.Fatalf("room read: status=%d result=%+v want=%+v", status, result, expected)
	}
	status, body := headerRead(t, f, "/v1/room?kind=room&target="+url.QueryEscape(request.Target.Name))
	var room core.RoomResult
	if err := json.Unmarshal(body, &room); status != http.StatusOK || err != nil || room != expected {
		t.Fatalf("GET room mismatch: status=%d body=%s want=%+v", status, body, expected)
	}
}
