package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testwait"
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

// The STATUS reader is an independently owned, non-observing connection.
// Its idle bound covers real lease-expiry checks; each STATUS response remains
// bounded by next's five-second deadline. It never joins or observes a room.
func nativeStatusClient(t *testing.T, socket string) *irc.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: socket, Nick: "live-status", Ephemeral: true, ReadTimeout: 2 * time.Minute, WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func nativeStatus(t *testing.T, client *irc.Client) protocol.ServerStatus {
	t.Helper()
	if err := client.Raw("STATUS"); err != nil {
		t.Fatal(err)
	}
	raw := next(t, client, func(event *irc.RawEvent) bool { return event.Command == "770" })
	var status protocol.ServerStatus
	if err := json.Unmarshal([]byte(raw.Trailing), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func nativeObserverCount(t *testing.T, client *irc.Client) int {
	t.Helper()
	return nativeStatus(t, client).Observers
}

func waitNativeObserverCount(t *testing.T, statusClient *irc.Client, want int) {
	t.Helper()
	testwait.Every(t, 4*time.Second, 50*time.Millisecond, fmt.Sprintf("the native observer count to reach %d", want), func() bool {
		return nativeObserverCount(t, statusClient) >= want
	})
}

func waitNativeObserverCountAtMost(t *testing.T, statusClient *irc.Client, want int) {
	t.Helper()
	testwait.Every(t, 4*time.Second, 50*time.Millisecond, fmt.Sprintf("the native observer count to fall to %d or below", want), func() bool {
		return nativeObserverCount(t, statusClient) <= want
	})
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

func TestEventsWatchHeaderLongPollReturnsNativeSignalWithoutCursor(t *testing.T) {
	t.Parallel()
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", http2), func(t *testing.T) {
			f := newReactionTransportFixture(t)
			f.http.Transport.(*http.Transport).ForceAttemptHTTP2 = http2
			native := liveSignalObserver(t, f.socket)
			author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
			t.Cleanup(func() { _ = author.Close() })
			statusClient := nativeStatusClient(t, f.socket)
			baseline := nativeObserverCount(t, statusClient)
			done, _, cancel := liveHTTPCall(t, f.http, f.httpURL+"/v1/live?"+liveQuery(), f.authorSpec)
			waitNativeObserverCount(t, statusClient, baseline+1)
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
			waitNativeObserverCountAtMost(t, statusClient, baseline)
		})
	}
}

func TestEventsWatchSSEMixedSnapshotAndLiveSignals(t *testing.T) {
	t.Parallel()
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
