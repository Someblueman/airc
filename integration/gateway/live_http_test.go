package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func liveSignalObserver(t *testing.T, socket string) *irc.Client {
	t.Helper()
	observer := dial(t, socket, "live-observer", "", false, true)
	if err := observer.ObserveSignalsRaw("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(event *irc.RawEvent) bool {
		return event.Command == "765" && len(event.Params) == 2 && event.Params[1] == "#gateway-fixture"
	})
	t.Cleanup(func() { _ = observer.Close() })
	return observer
}

func nativeObserverCount(t *testing.T, socket string) int {
	t.Helper()
	client := dial(t, socket, "live-status", "", false, true)
	defer client.Close()
	if err := client.Raw("STATUS"); err != nil {
		t.Fatal(err)
	}
	raw := next(t, client, func(event *irc.RawEvent) bool { return event.Command == "770" })
	var status protocol.ServerStatus
	if err := json.Unmarshal([]byte(raw.Trailing), &status); err != nil {
		t.Fatal(err)
	}
	return status.Observers
}

func waitNativeObserverCount(t *testing.T, socket string, want int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if nativeObserverCount(t, socket) >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("native observer count did not reach %d", want)
}

func waitNativeObserverCountAtMost(t *testing.T, socket string, want int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if nativeObserverCount(t, socket) <= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("native observer count did not fall to %d or below", want)
}

func nextRawLiveSignal(t *testing.T, observer *irc.Client, action string) irc.ChatEntry {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("native signal observer closed")
			}
			raw, ok := event.(*irc.RawEvent)
			if !ok || raw.Command != "780" {
				continue
			}
			var signal irc.ChatEntry
			if json.Unmarshal([]byte(raw.Trailing), &signal) == nil && (action == "" || signal.Action == action) {
				return signal
			}
		case <-deadline:
			t.Fatalf("native raw signal %q missing", action)
		}
	}
}

func TestEventsWatchBearerLongPollReturnsNativeSignalWithoutCursor(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", http2), func(t *testing.T) {
			f := newReactionTransportFixture(t)
			f.http.Transport.(*http.Transport).ForceAttemptHTTP2 = http2
			native := liveSignalObserver(t, f.socket)
			author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
			t.Cleanup(func() { _ = author.Close() })
			baseline := nativeObserverCount(t, f.socket)
			done, _, cancel := liveHTTPCall(t, f.http, f.httpURL+"/v1/live?"+liveQuery(), f.bearer)
			waitNativeObserverCount(t, f.socket, baseline+1)
			triggerLiveSignal(t, author, "typing")
			nativeSignal := nextRawLiveSignal(t, native, "typing")
			if nativeSignal.Target != "#gateway-fixture" || nativeSignal.From != "fixture-author" || nativeSignal.Text != "live typing" {
				t.Fatalf("native signal proof=%+v", nativeSignal)
			}
			result := <-done
			if result.err != nil || result.status != http.StatusOK || result.proto != map[bool]int{false: 1, true: 2}[http2] {
				t.Fatalf("long-poll live response: %+v", result)
			}
			event := nextLiveEvent(t, result.page, "typing")
			if event.Target != "#gateway-fixture" || result.page.Cursor != "" {
				t.Fatalf("live signal changed retained cursor: event=%+v page=%+v", event, result.page)
			}
			var signal irc.ChatEntry
			if err := json.Unmarshal(event.Signal, &signal); err != nil || signal.Action != "typing" || signal.Text != "live typing" {
				t.Fatalf("live signal lost native fields: %+v raw=%s err=%v", event, event.Signal, err)
			}
			cancel()
			waitNativeObserverCountAtMost(t, f.socket, baseline)
		})
	}
}

func TestEventsWatchSignedAndEncryptedSSEMixedSnapshotAndLiveSignals(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprintf("encrypted=%t", encrypted), func(t *testing.T) {
			f := newReactionTransportFixture(t)
			native := liveSignalObserver(t, f.socket)
			author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
			t.Cleanup(func() { _ = author.Close() })
			parentID := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "live-retained-parent")
			request := core.Request{Version: 1, RequestID: "live-sse-" + fmt.Sprint(encrypted), Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8, Start: "retained"}
			response, reader := liveDetachedStream(t, f, request, encrypted)
			if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
				t.Fatalf("SSE negotiation: %s proto=%s", response.Status, response.Proto)
			}
			initial := nextLiveSSEPage(t, reader)
			if len(initial.Messages) != 1 || len(initial.Events) != 0 || initial.Messages[0] == nil || initial.Cursor == "" {
				t.Fatalf("mixed stream lost retained snapshot: %+v", initial)
			}
			var retained struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(initial.Messages[0], &retained); err != nil || retained.ID != parentID {
				t.Fatalf("mixed stream retained wrong parent: id=%q want=%q err=%v", retained.ID, parentID, err)
			}
			triggerLiveSignal(t, author, "thinking")
			signal := nextRawLiveSignal(t, native, "thinking")
			if signal.Target != "#gateway-fixture" || signal.From != "fixture-author" {
				t.Fatalf("native thinking proof=%+v", signal)
			}
			live := nextLiveSSEPage(t, reader)
			nextLiveEvent(t, live, "thinking")
			if live.Cursor != initial.Cursor {
				t.Fatalf("signal-only SSE changed retained cursor: initial=%q live=%q", initial.Cursor, live.Cursor)
			}
		})
	}
}
