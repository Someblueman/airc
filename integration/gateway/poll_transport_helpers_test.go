//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func checkPollTransports(t *testing.T, f *reactionTransportFixture, want core.PollResult, pollID string) {
	t.Helper()
	request := core.Request{Version: 1, RequestID: "poll-results", Operation: core.PollResults, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, PollID: pollID}
	h2 := *f
	h2Transport := f.http.Transport.(*http.Transport).Clone()
	h2Transport.ForceAttemptHTTP2 = true
	t.Cleanup(h2Transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: h2Transport, Timeout: f.http.Timeout}
	cases := []struct {
		name string
		call func(string) (int, dispatch.DetachedResult)
	}{
		{name: "http1", call: func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, f, token, true) }},
		{name: "http2", call: func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, &h2, token, true) }},
		{name: "signed-http2", call: func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, &h2, token, false) }},
		{name: "framed", call: func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "poll-framed")
		}},
		{name: "websocket", call: func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "poll-websocket")
		}},
	}
	for _, tc := range cases {
		encrypted := tc.name != "signed-http2"
		status, result := tc.call(f.cred.token(t, request, encrypted))
		if status != http.StatusOK || result.Operation != core.PollResults || result.Poll == nil {
			t.Fatalf("%s poll result failed: status=%d result=%+v", tc.name, status, result)
		}
		if !reflect.DeepEqual(*result.Poll, want) {
			t.Fatalf("%s poll result mismatch: got=%+v want=%+v", tc.name, *result.Poll, want)
		}
	}
	query := "/v1/poll?kind=room&target=" + url.QueryEscape("#gateway-fixture") + "&poll_id=" + url.QueryEscape(pollID)
	status, body := pollHeaderGET(t, f, query)
	if status != http.StatusOK {
		t.Fatalf("authorSpec poll result failed: status=%d body=%s", status, body)
	}
	var got core.PollResult
	if err := json.Unmarshal(body, &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("authorSpec poll mismatch: err=%v got=%+v want=%+v", err, got, want)
	}
	status, body = pollHeaderGET(t, &h2, query)
	var h2got core.PollResult
	if err := json.Unmarshal(body, &h2got); status != http.StatusOK || err != nil || !reflect.DeepEqual(h2got, want) {
		t.Fatalf("HTTP/2 authorSpec poll mismatch: status=%d body=%s err=%v", status, body, err)
	}
}
