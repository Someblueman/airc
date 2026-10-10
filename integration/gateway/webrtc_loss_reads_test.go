//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWebRTCAGD1IncompleteConflictThenValidRequest(t *testing.T) {
	f := newWebRTCFixture(t, "reliable_ordered")
	session := newWebRTCSession(t, f, "reliable_ordered")
	request := core.Request{Version: 1, RequestID: "webrtc-agd1-malformed", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: strings.Repeat("unsubmitted-🙂", 200)}
	body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "webrtc-ignored", Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	if err != nil {
		t.Fatal(err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	fragments := webTransportDatagrams(body, id)
	if len(fragments) < 2 {
		t.Fatal("test request did not fragment")
	}
	if err := session.channel.Send(fragments[0]); err != nil {
		t.Fatal(err)
	}
	conflict := append([]byte(nil), fragments[0]...)
	conflict[len(conflict)-1] ^= 1
	if err := session.channel.Send(conflict); err != nil {
		t.Fatal(err)
	}
	response := webRTCSessionFrame(t, f, session, core.Request{Version: 1, RequestID: "webrtc-agd1-valid", Operation: core.CapabilitiesGet}, true, false)
	if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("valid request after malformed AGD1=%+v", response)
	}
	if records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name); len(records) != 0 {
		t.Fatalf("incomplete/conflicting fragments reached native intake: %+v", records)
	}
	// The same valid intent must succeed once its complete, fresh assembly arrives.
	valid := webRTCSessionFrame(t, f, session, request, true, false)
	if valid.Result == nil || valid.Result.Result == nil || !valid.Result.Result.Durable {
		t.Fatalf("complete assembly was not admitted: %+v", valid)
	}
	native := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.From == "fixture-author" && e.Message == request.Message })
	if native.Target != request.Target.Name || native.ID == "" {
		t.Fatalf("complete assembly native facts=%+v", native)
	}
}

func TestWebRTCFragmentedHistoryPreservesLargeWholePage(t *testing.T) {
	f := newWebRTCFixture(t, "reliable_ordered")
	author := dial(t, f.config.Bindings[0].Address, "webrtc-large-history", "", false, true)
	defer author.Close()
	body := strings.Repeat("large-history-🙂", 220)
	for i := 0; i < 6; i++ {
		if err := author.SendWithID("#gateway-fixture", body, "webrtc-large-history-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
		next(t, f.observer, func(e *irc.MessageEvent) bool { return e.From == "webrtc-large-history" && e.Message == body })
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	session := newWebRTCSession(t, f, "reliable_ordered")
	response := webRTCSessionFrame(t, f, session, core.Request{Version: 1, RequestID: "webrtc-large-history-read", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "recent", Limit: 16}, true, false)
	if response.Event != "result" || response.Result == nil || response.Result.Page == nil || len(response.Result.Page.Messages) < 6 {
		t.Fatalf("large fragmented history=%+v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) <= adapter.MaxRequestFrameBytes || len(encoded) > adapter.MaxResponseFrameBytes || len(native) != 6 || !reflect.DeepEqual(response.Result.Page.Messages, native) {
		t.Fatalf("large page did not preserve whole native records: bytes=%d native=%+v page=%+v err=%v", len(encoded), native, response.Result.Page, err)
	}
}
