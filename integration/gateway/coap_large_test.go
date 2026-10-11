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

func TestCoAPSBlockwiseLargeRequestAndWholeHistory(t *testing.T) {
	f := newCoAPFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	large := strings.Repeat("coaps-block1-🙂", 200)
	send := core.Request{Version: 1, RequestID: "coaps-block1-send", Operation: core.Send, Target: room, Message: large}
	frameBody, err := json.Marshal(frameRequest{Version: 1, CorrelationID: "coaps-" + send.RequestID, Profile: "plain", Request: f.credentials.token(t, send, true)})
	if err != nil || len(frameBody) <= 1024 {
		t.Fatalf("CoAPS Block1 request bytes=%d err=%v", len(frameBody), err)
	}
	accepted := coapAccepted(t, f, send, true)
	if accepted.MessageID == "" || accepted.SendAttempts != 1 {
		t.Fatalf("CoAPS Block1 acceptance=%+v", accepted)
	}
	native := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == accepted.MessageID })
	if native.Message != large || native.Target != room.Name {
		t.Fatalf("CoAPS Block1 native=%+v", native)
	}

	author := dial(t, f.config.Upstream.Address, "coaps-history-author", "", false, true)
	defer author.Close()
	for i := 0; i < 8; i++ {
		body := strings.Repeat("coaps-block2-history-"+string(rune('a'+i))+"-🙂", 110)
		id := "coaps-history-" + string(rune('a'+i))
		if err := author.SendWithID(room.Name, body, id); err != nil {
			t.Fatal(err)
		}
		next(t, f.observer, func(event *irc.MessageEvent) bool {
			return event.From == "coaps-history-author" && event.Message == body
		})
	}
	nativeHistory := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	request := core.Request{Version: 1, RequestID: "coaps-block2-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16}
	response := coapRPC(t, f, request, true)
	if response.Result == nil || response.Result.Page == nil || len(response.Result.Page.Messages) < 9 {
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
