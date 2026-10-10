//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func roomWaitingAdmin(t *testing.T, f *reactionTransportFixture) *irc.Client {
	t.Helper()
	admin := dial(t, f.socket, "fixture-control", "", false, true)
	token, err := auth.ReadToken(filepath.Join(filepath.Dir(f.socket), "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateAdmin(token); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "381" })
	return admin
}

func checkRoomWaitingHTTP2(t *testing.T, f *reactionTransportFixture, request core.Request, query string, expected any) {
	t.Helper()
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	status, body := bearerRead(t, &h2, query)
	if status != http.StatusOK {
		t.Fatalf("HTTP/2 read status=%d body=%s", status, body)
	}
	request.RequestID += "-signed-http2"
	status, signed := contextHTTPCall(t, &h2, f.cred.token(t, request, false), false)
	if status != http.StatusOK {
		t.Fatalf("signed HTTP/2 read status=%d result=%+v", status, signed)
	}
	switch want := expected.(type) {
	case core.RoomResult:
		var got core.RoomResult
		if err := json.Unmarshal(body, &got); err != nil || got != want || signed.Room == nil || *signed.Room != want {
			t.Fatalf("HTTP/2 room mismatch: body=%s signed=%+v", body, signed)
		}
	case core.WaitingResult:
		var got core.WaitingResult
		if err := json.Unmarshal(body, &got); err != nil || got.Target != want.Target || got.ParentID != want.ParentID || !sameSignals(got.Signals, want.Signals) || signed.Waiting == nil || signed.Waiting.Target != want.Target || signed.Waiting.ParentID != want.ParentID || !sameSignals(signed.Waiting.Signals, want.Signals) {
			t.Fatalf("HTTP/2 waiting mismatch: body=%s signed=%+v", body, signed)
		}
	default:
		t.Fatalf("unexpected snapshot type %s", reflect.TypeOf(expected))
	}
}

func sameSignals(a, b []irc.ChatEntry) bool {
	if len(a) != len(b) {
		return false
	}
	byActor := func(values []irc.ChatEntry) map[string]irc.ChatEntry {
		result := make(map[string]irc.ChatEntry, len(values))
		for _, value := range values {
			result[value.From+"\x00"+value.AccountID] = value
		}
		return result
	}
	return reflect.DeepEqual(byActor(a), byActor(b))
}

func bearerRoom(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	return bearerRead(t, f, path)
}
func bearerRead(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	return bearerReadToken(t, f, path, f.bearer)
}
func bearerReadToken(t *testing.T, f *reactionTransportFixture, path, token string) (int, []byte) {
	request, err := http.NewRequest(http.MethodGet, f.httpURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	wantProtocol := 1
	if f.http.Transport.(*http.Transport).ForceAttemptHTTP2 {
		wantProtocol = 2
	}
	if response.ProtoMajor != wantProtocol {
		t.Fatalf("HTTP protocol=%s want major%d", response.Proto, wantProtocol)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}
