//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestChannelsReadAcrossDetachedAndBearerTransports(t *testing.T) {
	f := newReactionTransportFixture(t)
	live := seedChannels(t, f.socket)
	beforeHistory := channelHistorySnapshot(t, f.socket)
	want := nativeChannels(t, f.socket)
	if len(want) < 3 {
		t.Fatalf("native CHANNELS snapshot too small: %+v", want)
	}
	checkChannel := func(name string, messages, members int, topic string) {
		t.Helper()
		for _, channel := range want {
			if channel.Name == name {
				if channel.Messages != messages || channel.Members != members || channel.Topic != topic || (messages > 0 && channel.LastActivity.IsZero()) {
					t.Fatalf("native channel %s metadata=%+v", name, channel)
				}
				return
			}
		}
		t.Fatalf("native CHANNELS omitted %s: %+v", name, want)
	}
	checkChannel("#channels-history", 2, 0, "")
	checkChannel("#channels-topic", 0, 0, "whole topic 🙂")
	checkChannel("#channels-Case", 0, 0, "case-sensitive upper")
	checkChannel("#channels-case", 0, 0, "case-sensitive lower")
	checkChannel("#channels-live", 0, 1, "")
	for _, channel := range want {
		if channel.Name == "channels-topic" {
			t.Fatal("native discovery leaked direct-message target")
		}
	}
	request := core.Request{Version: 1, RequestID: "channels-read", Operation: core.ChannelsRead}
	cases := []struct {
		name string
		call func(string) (int, dispatch.DetachedResult)
	}{
		{"encrypted-http", func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, f, token, true) }},
		{"encrypted-framed", func(token string) (int, dispatch.DetachedResult) {
			return contextFramedCall(t, f.framed, token, "channels-framed", true)
		}},
		{"encrypted-websocket", func(token string) (int, dispatch.DetachedResult) {
			return contextWebSocketCall(t, f.ws, token, "channels-websocket", true)
		}},
	}
	for _, tc := range cases {
		status, result := tc.call(f.cred.token(t, request, true))
		if status != http.StatusOK || result.Channels == nil || !channelsEqual(result.Channels.Channels, want) {
			t.Fatalf("%s channels=%+v want=%+v status=%d", tc.name, result.Channels, want, status)
		}
	}
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	status, signed := contextHTTPCall(t, &h2, f.cred.token(t, request, false), false)
	if status != http.StatusOK || signed.Channels == nil || !channelsEqual(signed.Channels.Channels, want) {
		t.Fatalf("signed HTTP/2 channels=%+v want=%+v status=%d", signed.Channels, want, status)
	}
	query := "?" + url.Values{}.Encode()
	status, body := bearerChannels(t, &h2, query, f.bearer)
	var bearer core.ChannelsResult
	if status != http.StatusOK || json.Unmarshal(body, &bearer) != nil || !channelsEqual(bearer.Channels, want) {
		t.Fatalf("bearer channels=%+v want=%+v status=%d body=%s", bearer.Channels, want, status, body)
	}
	for _, bad := range []string{"?kind=room", "?target=%23gateway-fixture", "?unknown=x"} {
		if status, _ := bearerChannels(t, f, bad, f.bearer); status != http.StatusBadRequest {
			t.Fatalf("strict channels query %q status=%d", bad, status)
		}
	}
	if status, _ := bearerChannels(t, f, "", "invalid-token"); status != http.StatusUnauthorized {
		t.Fatalf("invalid channels credential status=%d", status)
	}
	if status, _ := bearerChannels(t, f, "", f.bearer); status != http.StatusOK {
		t.Fatalf("repeat channels read status=%d", status)
	}
	if status, _ := bearerChannels(t, f, "/v1/requests/channels-read", f.bearer); status != http.StatusNotFound {
		t.Fatalf("channels read unexpectedly created a job: status=%d", status)
	}
	if err := live.Part("#channels-live", "done"); err != nil {
		t.Fatal(err)
	}
	next(t, live, func(e *irc.PartEvent) bool { return e.Agent == "channels-live" })
	after := nativeChannels(t, f.socket)
	status, fresh := contextHTTPCall(t, f, f.cred.token(t, request, true), true)
	if status != http.StatusOK || fresh.Channels == nil || !channelsEqual(fresh.Channels.Channels, after) || channelsEqual(after, want) {
		t.Fatalf("post-PART snapshot=%+v native=%+v before=%+v status=%d", fresh.Channels, after, want, status)
	}
	if !reflect.DeepEqual(beforeHistory, channelHistorySnapshot(t, f.socket)) {
		t.Fatal("room discovery changed native history")
	}
}
