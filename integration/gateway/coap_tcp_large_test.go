//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestCoAPSTCPBlockwiseLargeRequestAndWholeHistory(t *testing.T) {
	f := newCoAPTCPFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	large := strings.Repeat("coaps-tcp-block1-🙂", 160)
	if len(large) > 3400 {
		t.Fatalf("CoAPS TCP Block1 body bytes=%d exceeds native bound", len(large))
	}
	send := core.Request{Version: 1, RequestID: "coaps-tcp-block1-send", Operation: core.Send, Target: room, Message: large}
	frameBody, err := json.Marshal(frameRequest{Version: 1, CorrelationID: "coaps-tcp-" + send.RequestID, Profile: "plain", Request: f.credentials.token(t, send, true)})
	if err != nil || len(frameBody) <= 1024 {
		t.Fatalf("CoAPS Block1 request bytes=%d err=%v", len(frameBody), err)
	}
	accepted := coapTCPAccepted(t, f, send, true)
	if accepted.MessageID == "" || accepted.SendAttempts != 1 {
		t.Fatalf("CoAPS Block1 acceptance=%+v", accepted)
	}
	native := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == accepted.MessageID })
	if native.Message != large || native.Target != room.Name {
		t.Fatalf("CoAPS Block1 native=%+v", native)
	}

	author := dial(t, f.config.Upstream.Address, "coaps-tcp-history-author", "", false, true)
	defer author.Close()
	for i := 0; i < 5; i++ {
		body := strings.Repeat("coaps-tcp-block2-history-"+string(rune('a'+i))+"-🙂", 110)
		id := "coaps-tcp-history-" + string(rune('a'+i))
		if err := author.SendWithID(room.Name, body, id); err != nil {
			t.Fatal(err)
		}
		next(t, f.observer, func(event *irc.MessageEvent) bool {
			return event.From == "coaps-tcp-history-author" && event.Message == body
		})
	}
	nativeHistory := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	request := core.Request{Version: 1, RequestID: "coaps-tcp-block2-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16}
	response := coapTCPRPC(t, f, request, true)
	if response.Result == nil || response.Result.Page == nil || len(response.Result.Page.Messages) < 6 {
		t.Fatalf("CoAPS Block2 history=%+v", response)
	}
	if !reflect.DeepEqual(response.Result.Page.Messages, nativeHistory) {
		t.Fatalf("CoAPS Block2 changed native records: native=%+v gateway=%+v", nativeHistory, response.Result.Page.Messages)
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) <= adapter.MaxRequestFrameBytes {
		t.Fatalf("CoAPS Block2 response was not a large whole response: bytes=%d err=%v", len(encoded), err)
	}
}
