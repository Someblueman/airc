//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func waitHTTPRoomResult(t *testing.T, f *reactionTransportFixture, id string, state core.State) core.Result {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, data := headerRead(t, f, "/v1/requests/"+id)
		var result core.Result
		if status == http.StatusOK && json.Unmarshal(data, &result) == nil && result.State == state {
			if state == core.Accepted && (result.Room == nil || result.Accepted == nil || !*result.Accepted || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil || result.Card != nil || result.Topic != nil) {
				t.Fatalf("room acknowledgement facts: %+v", result)
			}
			return result
		}
		if time.Now().After(deadline) {
			t.Fatalf("room %s wanted %s: status=%d body=%s", id, state, status, data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRoomUpdateHTTPPartialAndNativeDenials maps room.update to the native
// room settings: each saved acknowledgement is the native configuration, a
// partial patch keeps the other setting, room.read reflects it, and native
// ceiling and operator refusals are classified without changing the room.
func TestRoomUpdateHTTPPartialAndNativeDenials(t *testing.T) {
	f := newReactionFixture(t, true, nil, true)
	promoteRoomOperator(t, f.socket)
	target := contractRoom()
	update := func(id string, slow *int64, limit *int, header bool) core.RoomResult {
		t.Helper()
		request := core.Request{Version: 1, RequestID: id, Operation: core.RoomUpdate, Target: target, Room: &core.RoomPatch{SlowSeconds: slow, HistoryLimit: limit}}
		if header {
			headerPost(t, f, request)
		} else if status, admission := reactionHTTPCall(t, f, f.cred.token(t, request, true)); status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil || !admission.Result.Durable {
			t.Fatalf("request-route room admission: %d %+v", status, admission)
		}
		saved := waitHTTPRoomResult(t, f, id, core.Accepted).Room.Room
		if native := queryNativeRoom(t, f.socket, target.Name); saved != native {
			t.Fatalf("room acknowledgement differs from native: saved=%+v native=%+v", saved, native)
		}
		return saved
	}
	zero, historyZero, slow, limit := int64(0), 0, int64(3600), 32
	if got := update("room-http-zero", &zero, &historyZero, true); got.SlowSeconds != 0 || got.HistoryLimit != 0 {
		t.Fatalf("native zero settings: %+v", got)
	}
	if got := update("room-http-slow-only", &slow, nil, false); got.SlowSeconds != 3600 || got.HistoryLimit != 0 {
		t.Fatalf("partial slow update: %+v", got)
	}
	saved := update("room-http-history-only", nil, &limit, true)
	if saved.SlowSeconds != 3600 || saved.HistoryLimit != 32 {
		t.Fatalf("partial history update: %+v", saved)
	}
	if status, read := reactionHTTPCall(t, f, f.cred.token(t, core.Request{Version: 1, RequestID: "room-http-read", Operation: core.RoomRead, Target: target}, true)); status != http.StatusOK || read.Room == nil || *read.Room != saved {
		t.Fatalf("room read did not reflect update: status=%d result=%+v", status, read)
	}
	tooLarge := 33
	ceiling := core.Request{Version: 1, RequestID: "room-http-native-ceiling", Operation: core.RoomUpdate, Target: target, Room: &core.RoomPatch{HistoryLimit: &tooLarge}}
	headerPost(t, f, ceiling)
	if denied := waitHTTPRoomResult(t, f, ceiling.RequestID, core.Rejected); denied.Room != nil || denied.Failure == nil || denied.Failure.Code != core.InvalidRequest || denied.Failure.UpstreamCode != 461 || queryNativeRoom(t, f.socket, target.Name) != saved {
		t.Fatalf("native ceiling rejection lost facts: %+v", denied)
	}
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	if err := admin.Raw("MODE #gateway-fixture -o fixture-author"); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "MODE" && len(e.Params) > 1 && e.Params[1] == "-o" })
	noAuthority := core.Request{Version: 1, RequestID: "room-http-no-operator", Operation: core.RoomUpdate, Target: target, Room: &core.RoomPatch{SlowSeconds: &zero}}
	headerPost(t, f, noAuthority)
	if denied := waitHTTPRoomResult(t, f, noAuthority.RequestID, core.Blocked); denied.Room != nil || denied.Failure == nil || denied.Failure.Code != core.Forbidden || denied.Failure.UpstreamCode != 481 || queryNativeRoom(t, f.socket, target.Name) != saved {
		t.Fatalf("operator denial lost facts: %+v", denied)
	}
	promoteRoomOperator(t, f.socket)
	if restored := update("room-http-authority-recovery", &zero, nil, true); restored.SlowSeconds != 0 || restored.HistoryLimit != 32 {
		t.Fatalf("operator recovery: %+v", restored)
	}
}
