//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func activityRequest(id, kind, note string, ttl int64) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: core.ActivitySet, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Activity: &core.ActivityInput{Kind: kind, Note: note, TTLSeconds: ttl}}
}

// TestActivitySetHTTPThrottleAndDirectTarget maps activity.set to the native
// typing/thinking signal: the saved ACK equals the broadcast, a request inside
// the native two-second throttle saves the prior signal unchanged (across
// kinds) without broadcasting, and direct targets signal the recipient.
func TestActivitySetHTTPThrottleAndDirectTarget(t *testing.T) {
	direct := core.Target{Kind: "direct", Name: "activity-recipient"}
	f := newReactionFixture(t, false, []core.Target{direct}, true)
	request := activityRequest("activity-http", "typing", "first note", 15)
	observer := newActivityObserver(t, f.socket, *request.Target)
	headerPost(t, f, request)
	first := waitHTTPPinResult(t, f, request.RequestID, core.Accepted)
	assertActivityMutationFacts(t, first, request, nextActivitySignal(t, observer))
	request.RequestID = "activity-http-throttled"
	request.Activity = &core.ActivityInput{Kind: "thinking", Note: "different note", TTLSeconds: 1}
	headerPost(t, f, request)
	throttled := waitHTTPPinResult(t, f, request.RequestID, core.Accepted)
	assertActivityMutationFacts(t, throttled, request, nil)
	if *throttled.Activity != *first.Activity {
		t.Fatalf("native cross-kind throttle was not preserved: first=%+v later=%+v", first.Activity, throttled.Activity)
	}
	assertNoActivitySignal(t, observer)
	recipient := newActivityObserver(t, f.socket, direct)
	request = activityRequest("activity-http-direct", "typing", "direct note", 15)
	request.Target = &direct
	status, admission := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil || !admission.Result.Durable {
		t.Fatalf("direct activity admission: %d %+v", status, admission)
	}
	assertActivityMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request, nextActivitySignal(t, recipient))
}

func newActivityObserver(t *testing.T, socket string, target core.Target) *irc.Client {
	t.Helper()
	nick := target.Name
	if target.Kind == "room" {
		nick = fmt.Sprintf("activity-reader-%d", roomReaderSequence.Add(1))
	}
	observer := dial(t, socket, nick, "", false, false)
	t.Cleanup(func() { _ = observer.Close() })
	if target.Kind == "room" {
		if err := observer.Join(target.Name); err != nil {
			t.Fatal(err)
		}
		next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == nick && e.Channel == target.Name })
	}
	return observer
}

func nextActivitySignal(t *testing.T, observer *irc.Client) *irc.SignalEvent {
	t.Helper()
	return next(t, observer, func(e *irc.SignalEvent) bool { return e.Action == "typing" || e.Action == "thinking" })
}

func assertActivityMutationFacts(t *testing.T, result core.Result, request core.Request, signal *irc.SignalEvent) {
	t.Helper()
	if result.State != core.Accepted || !result.Durable || result.Accepted == nil || !*result.Accepted || result.Activity == nil || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil || result.Card != nil || result.Topic != nil || result.Room != nil || result.Pin != nil {
		t.Fatalf("activity mutation facts: %+v", result)
	}
	if _, err := core.ActivityAckBytes(result.Activity, request, "fixture-author"); err != nil {
		t.Fatal(err)
	}
	if signal != nil {
		ack := result.Activity
		if ack.Kind != signal.Action || ack.Target.Name != signal.Target || ack.From != signal.From || ack.AccountID != signal.AccountID || ack.Note != signal.Text || !ack.ExpiresAt.Equal(signal.ExpiresAt) || signal.ID != "" {
			t.Fatalf("native observer differs from saved ACK: ack=%+v signal=%+v", ack, signal)
		}
	}
}

func assertNoActivitySignal(t *testing.T, observer *irc.Client) {
	t.Helper()
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("native activity observer disconnected")
			}
			if signal, ok := event.(*irc.SignalEvent); ok {
				t.Fatalf("unexpected native activity broadcast: %+v", signal)
			}
		case <-timer.C:
			return
		}
	}
}
