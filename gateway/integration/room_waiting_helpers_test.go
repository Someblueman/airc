//go:build darwin || linux

package gateway_test

import (
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
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

func headerRead(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	return headerReadToken(t, f, path, f.authorSpec)
}
func headerReadToken(t *testing.T, f *reactionTransportFixture, path, token string) (int, []byte) {
	request, err := http.NewRequest(http.MethodGet, f.httpURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, token)
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
