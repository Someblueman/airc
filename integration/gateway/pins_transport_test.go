//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type pinsWire struct {
	Target core.Target           `json:"target"`
	Pins   []irc.MessageMetadata `json:"pins"`
}

func TestPinsReadAcrossTransportsAndHeaders(t *testing.T) {
	f := newReactionTransportFixture(t)
	root := seedPinnedRoot(t, f)
	seedNativePin(t, f, root.ID)
	request := core.Request{Version: 1, RequestID: "pins-read", Operation: core.PinsRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	before := contextCheckSnapshot(t, f)
	var expected core.PinsResult
	for _, call := range []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "pins-framed")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "pins-websocket")
		}},
	} {
		status, result := call.fn(f.cred.token(t, request, true))
		if status != http.StatusOK || result.Operation != core.PinsRead {
			t.Fatalf("%s pins read failed: status=%d result=%+v", call.name, status, result)
		}
		if result.Pins == nil {
			t.Fatalf("%s pins result was absent", call.name)
		}
		pins := *result.Pins
		if expected.Pins == nil {
			expected = pins
		} else if !assertSamePins(expected, pins) {
			t.Fatalf("%s pin snapshot differs: got=%+v want=%+v", call.name, pins, expected)
		}
		if pins.Target.Name != "#gateway-fixture" || len(pins.Pins) != 1 || pins.Pins[0].ID != root.ID || pins.Pins[0].Message != root.Message {
			t.Fatalf("%s pin metadata mismatch: %+v", call.name, pins)
		}
	}
	query := "/v1/pins?kind=room&target=" + url.QueryEscape("#gateway-fixture")
	status, body := headerPins(t, f, query)
	if status != http.StatusOK {
		t.Fatalf("authorSpec pins failed: status=%d body=%s", status, body)
	}
	var pins pinsWire
	if err := json.Unmarshal(body, &pins); err != nil || len(pins.Pins) != 1 || pins.Pins[0].ID != root.ID {
		t.Fatalf("authorSpec pin mismatch: err=%v pins=%+v", err, pins)
	}
	h2 := *f
	h2transport := f.http.Transport.(*http.Transport).Clone()
	h2transport.ForceAttemptHTTP2 = true
	t.Cleanup(h2transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: h2transport, Timeout: f.http.Timeout}
	status, body = headerPins(t, &h2, query)
	var h2pins core.PinsResult
	if err := json.Unmarshal(body, &h2pins); status != http.StatusOK || err != nil || !assertSamePins(expected, h2pins) {
		t.Fatalf("HTTP/2 pins mismatch: status=%d body=%s err=%v", status, body, err)
	}
	signedRequest := request
	signedRequest.RequestID = "pins-signed-http2"
	status, signed := contextHTTPCall(t, &h2, f.cred.token(t, signedRequest, false), false)
	if status != http.StatusOK || signed.Pins == nil || !assertSamePins(expected, *signed.Pins) {
		t.Fatalf("signed HTTP/2 pins mismatch: status=%d result=%+v", status, signed)
	}
	if status, _ := headerPins(t, f, query+"&unknown=x"); status != http.StatusBadRequest {
		t.Fatalf("pins accepted unknown query: status=%d", status)
	}
	if status, _ := headerPins(t, f, query+"&target=%23other-private"); status != http.StatusBadRequest {
		t.Fatalf("pins accepted duplicate target query: status=%d", status)
	}
	if status, _ := headerPins(t, f, "/v1/pins?kind=direct&target=fixture-author"); status != http.StatusBadRequest {
		t.Fatalf("pins accepted direct target query: status=%d", status)
	}
	if status, _ := headerPins(t, f, "/v1/pins?kind=room&target=%23other-private"); status != http.StatusForbidden {
		t.Fatalf("pins crossed room permission: status=%d", status)
	}
	if status, _ := headerPinsAuth(t, f, query, wrongAuthorSpec); status != http.StatusUnauthorized {
		t.Fatalf("pins accepted invalid authorSpec: status=%d", status)
	}
	if after := contextCheckSnapshot(t, f); !sameSnapshot(before, after) {
		t.Fatalf("pins read mutated native history: before=%v after=%v", before, after)
	}
	if status, _ := headerPins(t, f, "/v1/requests/pins-read"); status != http.StatusNotFound {
		t.Fatalf("pins read created delivery receipt: status=%d", status)
	}

	removeNativePin(t, f, root.ID)
	status, result := reactionHTTPCall(t, f, f.cred.token(t, core.Request{Version: 1, RequestID: "pins-empty", Operation: core.PinsRead, Target: request.Target}, true))
	if status != http.StatusOK || result.Pins == nil || result.Pins.Pins == nil || len(result.Pins.Pins) != 0 {
		t.Fatalf("native unpin was not reflected: status=%d result=%+v", status, result)
	}
	status, body = headerPins(t, f, query)
	var empty core.PinsResult
	if err := json.Unmarshal(body, &empty); status != http.StatusOK || err != nil || empty.Pins == nil || len(empty.Pins) != 0 {
		t.Fatalf("authorSpec empty pins was not an array: status=%d body=%s err=%v", status, body, err)
	}
}

func TestPinnedRecordSurvivesHistoryEvictionWithCorrectionAnnotation(t *testing.T) {
	f := newReactionTransportFixture(t)
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
