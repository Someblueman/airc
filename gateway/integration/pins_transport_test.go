//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestPinsReadReturnsNativePinsAndEmptyArray maps pins.read to the room's
// native pin list with whole pinned bodies, and an empty array once unpinned.
func TestPinsReadReturnsNativePinsAndEmptyArray(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	root := seedPinnedRoot(t, f)
	seedNativePin(t, f, root.ID)
	read := func(label string, want int) {
		t.Helper()
		request := core.Request{Version: 1, RequestID: "pins-read-" + label, Operation: core.PinsRead, Target: contractRoom()}
		status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
		if status != http.StatusOK || result.Pins == nil || result.Pins.Pins == nil || result.Pins.Target != *request.Target || len(result.Pins.Pins) != want || want == 1 && (result.Pins.Pins[0].ID != root.ID || result.Pins.Pins[0].Message != root.Message) {
			t.Fatalf("%s pins read: status=%d result=%+v", label, status, result.Pins)
		}
		status, body := headerPins(t, f, "/v1/pins?kind=room&target="+url.QueryEscape("#gateway-fixture"))
		var pins core.PinsResult
		if err := json.Unmarshal(body, &pins); status != http.StatusOK || err != nil || pins.Pins == nil || !assertSamePins(*result.Pins, pins) {
			t.Fatalf("%s GET pins mismatch: status=%d body=%s", label, status, body)
		}
	}
	read("pinned", 1)
	removeNativePin(t, f, root.ID)
	read("empty", 0)
}

func TestPinnedRecordSurvivesHistoryEvictionWithCorrectionAnnotation(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	root := seedReactionParent(t, f)
	seedNativePin(t, f, root.ID)
	correction := seedContextCorrection(t, f, root.ID)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	for i := 0; i < 32; i++ {
		id := "pins-eviction-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if err := author.SendWithID("#gateway-fixture", "pin eviction filler "+id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	_ = author.Close()
	retained := contextCheckSnapshot(t, f)
	if _, exists := retained[root.ID]; exists {
		t.Fatal("history eviction did not remove pinned root")
	}
	if _, exists := retained[correction.ID]; exists {
		t.Fatal("history eviction did not remove correction")
	}
	r := core.Request{Version: 1, RequestID: "pins-after-eviction", Operation: core.PinsRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
	if status != http.StatusOK || result.Pins == nil || len(result.Pins.Pins) != 1 {
		t.Fatalf("evicted pinned record unavailable: status=%d result=%+v", status, result)
	}
	pin := result.Pins.Pins[0]
	if pin.ID != root.ID || pin.SupersededBy != correction.ID || pin.Message != root.Message {
		t.Fatalf("pinned correction annotation mismatch: %+v correction=%s", pin, correction.ID)
	}
}

func assertSamePins(a, b core.PinsResult) bool {
	return a.Target == b.Target && reflect.DeepEqual(a.Pins, b.Pins)
}

func seedPinnedRoot(t *testing.T, f *reactionTransportFixture) *irc.MessageEvent {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	const body = "pinned whole body\nUnicode: café ☃"
	if err := author.SendWithID("#gateway-fixture", body, "pins-seed"); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "pins-seed" })
	parent := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	if parent.Message != body {
		t.Fatalf("native pinned body = %q, want %q", parent.Message, body)
	}
	return parent
}

func seedNativePin(t *testing.T, f *reactionTransportFixture, id string) {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "pin", Target: "#gateway-fixture", ID: id}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "pin" && e.ID == id })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
}

func removeNativePin(t *testing.T, f *reactionTransportFixture, id string) {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "unpin", Target: "#gateway-fixture", ID: id}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "unpin" && e.ID == id })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
}

func headerPins(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	return headerPinsAuth(t, f, path, f.authorSpec)
}

func headerPinsAuth(t *testing.T, f *reactionTransportFixture, path, token string) (int, []byte) {
	t.Helper()
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
		t.Fatalf("pins HTTP protocol = %s, want major %d", response.Proto, wantProtocol)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}
