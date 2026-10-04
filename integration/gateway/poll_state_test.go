//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/core"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestPollResultsSurviveHistoryEvictionAndExpiry(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	want, pollID := seedAndClosePoll(t, f, 60)
	evictPollHistory(t, f.socket, pollID)
	historyRequest := core.Request{Version: 1, RequestID: "poll-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 50, Start: "retained"}
	_, history := reactionFramedCall(t, f.framed, f.cred.token(t, historyRequest, true), "poll-history")
	if history.Page == nil {
		t.Fatal("history page missing after eviction")
	}
	if len(history.Page.Messages) != 32 || history.Page.Messages[len(history.Page.Messages)-1].Message != "evict poll-evict-039" {
		t.Fatalf("history eviction control mismatch: page=%+v", history.Page)
	}
	for _, message := range history.Page.Messages {
		if message.ID == pollID {
			t.Fatal("poll remained in retained history after eviction")
		}
	}
	status, body := pollBearerGET(t, f, "/v1/poll?kind=room&target=%23gateway-fixture&poll_id="+url.QueryEscape(pollID))
	var got core.PollResult
	if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("evicted poll result mismatch: status=%d err=%v got=%+v want=%+v", status, err, got, want)
	}
	checkPollTransports(t, f, want, pollID)
	short, shortID := seedPollOnly(t, f, 1)
	deadline := time.Until(short.ExpiresAt) + 150*time.Millisecond
	if deadline > 0 {
		time.Sleep(deadline)
	}
	status, body = pollBearerGET(t, f, "/v1/poll?kind=room&target=%23gateway-fixture&poll_id="+url.QueryEscape(shortID))
	short.Closed = true
	if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, short) {
		t.Fatalf("expired poll result mismatch: status=%d err=%v got=%+v", status, err, got)
	}
}

func TestPollResultsOpenThenVoteAndClose(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	open, pollID := seedPollOnly(t, f, 60)
	query := "/v1/poll?kind=room&target=%23gateway-fixture&poll_id=" + url.QueryEscape(pollID)
	status, body := pollBearerGET(t, f, query)
	var got core.PollResult
	if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, open) || got.Closed {
		t.Fatalf("open poll result mismatch: status=%d err=%v got=%+v want=%+v", status, err, got, open)
	}
	want := applyPollVoteAndClose(t, f.socket, pollID)
	status, body = pollBearerGET(t, f, query)
	if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("closed poll result mismatch: status=%d err=%v got=%+v", status, err, got)
	}
}
