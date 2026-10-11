//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestPollResultsOverUnixFramed(t *testing.T) {
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	want, pollID := seedAndClosePollAt(t, upstream, 60)
	evictPollHistory(t, upstream, pollID)
	request := core.Request{Version: 1, RequestID: "unix-poll-results", Operation: core.PollResults, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, PollID: pollID}
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "unix-poll-results", Profile: "plain", Request: credentials.token(t, request, true)})
	response := readFrame(t, conn)
	if response.Event != "result" || response.Error != nil || response.Result == nil || response.Result.Poll == nil {
		t.Fatalf("Unix poll result failed: %+v", response)
	}
	if response.Result.Operation != core.PollResults || !reflect.DeepEqual(*response.Result.Poll, want) || response.Result.Poll.Target != *request.Target || response.Result.Poll.ID != pollID || !response.Result.Poll.Closed || len(response.Result.Poll.Options) != 2 || response.Result.Poll.Votes[0] != 1 {
		t.Fatalf("Unix poll result mismatch: %+v", response.Result.Poll)
	}
	stop()
}
