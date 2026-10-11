//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// checkPollTransports requires both HTTP routes to return want for pollID.
func checkPollTransports(t *testing.T, f *reactionTransportFixture, want core.PollResult, pollID string) {
	t.Helper()
	request := core.Request{Version: 1, RequestID: "poll-results", Operation: core.PollResults, Target: contractRoom(), PollID: pollID}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusOK || result.Operation != core.PollResults || result.Poll == nil || !reflect.DeepEqual(*result.Poll, want) {
		t.Fatalf("poll result: status=%d result=%+v want=%+v", status, result, want)
	}
	status, body := pollHeaderGET(t, f, "/v1/poll?kind=room&target="+url.QueryEscape("#gateway-fixture")+"&poll_id="+url.QueryEscape(pollID))
	var got core.PollResult
	if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("GET poll mismatch: status=%d body=%s want=%+v", status, body, want)
	}
}
