//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func roomHeaderPost(t *testing.T, f *reactionTransportFixture, r core.Request, proto int) core.Result {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, f.httpURL+"/v1/messages", bytes.NewReader(requestJSON(t, r)))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, f.authorSpec)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result core.Result
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != proto || response.StatusCode != http.StatusAccepted || !result.Durable {
		t.Fatalf("room admission: proto=%d status=%d result=%+v", response.ProtoMajor, response.StatusCode, result)
	}
	return result
}

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

func TestRoomUpdateHeadersHTTP1HTTP2AndBoundaries(t *testing.T) {
	f := newReactionTransportFixtureWithAdmin(t, true)
	promoteRoomOperator(t, f.socket)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	zero, historyZero := int64(0), 0
	request := core.Request{Version: 1, RequestID: "room-http1", Operation: core.RoomUpdate, Target: target, Room: &core.RoomPatch{SlowSeconds: &zero, HistoryLimit: &historyZero}}
	roomHeaderPost(t, f, request, 1)
	first := waitHTTPRoomResult(t, f, request.RequestID, core.Accepted)
	if first.Room.Room.SlowSeconds != 0 || first.Room.Room.HistoryLimit != 0 || first.Room.Room != queryNativeRoom(t, f.socket, target.Name) {
		t.Fatalf("native zero settings: %+v", first)
	}
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	second := request
	second.RequestID = "room-http2"
	roomHeaderPost(t, &h2, second, 2)
	waitHTTPRoomResult(t, &h2, second.RequestID, core.Accepted)
	slow := int64(3600)
	signed := request
	signed.RequestID = "room-signed-http2"
	signed.Room = &core.RoomPatch{SlowSeconds: &slow}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	signed.IssuedAt, signed.ExpiresAt = &issued, &expiry
	requestBody := f.cred.token(t, signed, false)
	wire, err := http.NewRequest(http.MethodPost, h2.httpURL+"/v1/request", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	wire.Header.Set("Content-Type", "application/json")
	response, err := h2.http.Do(wire)
	if err != nil {
		t.Fatal(err)
	}
	var detached dispatch.DetachedResult
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&detached)
	response.Body.Close()
	if err != nil || response.ProtoMajor != 2 || response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted || detached.Result == nil || !detached.Result.Durable {
		t.Fatalf("signed HTTP2: err=%v proto=%d status=%d result=%+v", err, response.ProtoMajor, response.StatusCode, detached)
	}
	saved := waitHTTPRoomResult(t, &h2, signed.RequestID, core.Accepted)
	if saved.Room.Room.SlowSeconds != 3600 || saved.Room.Room.HistoryLimit != 0 || saved.Room.Room != queryNativeRoom(t, f.socket, target.Name) {
		t.Fatalf("partial slow update: %+v", saved)
	}
	limit := 32
	partial := request
	partial.RequestID = "room-history-only"
	partial.Room = &core.RoomPatch{HistoryLimit: &limit}
	roomHeaderPost(t, &h2, partial, 2)
	saved = waitHTTPRoomResult(t, &h2, partial.RequestID, core.Accepted)
	if saved.Room.Room.SlowSeconds != 3600 || saved.Room.Room.HistoryLimit != 32 || saved.Room.Room != queryNativeRoom(t, f.socket, target.Name) {
		t.Fatalf("partial history update: %+v", saved)
	}
	roomHeaderPost(t, &h2, partial, 2)
	replay := waitHTTPRoomResult(t, &h2, partial.RequestID, core.Accepted)
	if *replay.Room != *saved.Room {
		t.Fatalf("accepted replay changed snapshot: %+v", replay)
	}
	changed := partial
	changed.Room = &core.RoomPatch{SlowSeconds: &zero}
	status, _, _ := headerReactionHTTP(t, &h2, http.MethodPost, "/v1/messages", requestJSON(t, changed))
	if status != http.StatusConflict {
		t.Fatalf("changed room intent status=%d", status)
	}
	tooLarge := 33
	nativeDenied := request
	nativeDenied.RequestID = "room-native-ceiling"
	nativeDenied.Room = &core.RoomPatch{HistoryLimit: &tooLarge}
	roomHeaderPost(t, &h2, nativeDenied, 2)
	denied := waitHTTPRoomResult(t, &h2, nativeDenied.RequestID, core.Rejected)
	if denied.Room != nil || denied.Failure == nil || denied.Failure.Code != core.InvalidRequest || denied.Failure.UpstreamCode != 461 || queryNativeRoom(t, f.socket, target.Name) != saved.Room.Room {
		t.Fatalf("native ceiling rejection lost facts: %+v", denied)
	}
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	if err := admin.Raw("MODE #gateway-fixture -o fixture-author"); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "MODE" && len(e.Params) > 1 && e.Params[1] == "-o" })
	noAuthority := request
	noAuthority.RequestID = "room-no-operator"
	noAuthority.Room = &core.RoomPatch{SlowSeconds: &zero}
	roomHeaderPost(t, f, noAuthority, 1)
	denied = waitHTTPRoomResult(t, f, noAuthority.RequestID, core.Blocked)
	if denied.Room != nil || denied.Failure == nil || denied.Failure.Code != core.Forbidden || denied.Failure.UpstreamCode != 481 || queryNativeRoom(t, f.socket, target.Name) != saved.Room.Room {
		t.Fatalf("operator denial lost facts: %+v", denied)
	}
	promoteRoomOperator(t, f.socket)
	recovery := noAuthority
	recovery.RequestID = "room-authority-recovery"
	roomHeaderPost(t, f, recovery, 1)
	restored := waitHTTPRoomResult(t, f, recovery.RequestID, core.Accepted)
	if restored.Room.Room.SlowSeconds != 0 || restored.Room.Room.HistoryLimit != 32 {
		t.Fatalf("operator recovery: %+v", restored)
	}
}
