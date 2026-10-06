//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapClient "github.com/plgd-dev/go-coap/v3/net/client"
)

type coapNotification struct {
	body       []byte
	format     message.MediaType
	observe    uint32
	hasObserve bool
}

func startCoAPSObserve(t *testing.T, f coapFixture, request core.Request) (coapClient.Observation, <-chan coapNotification, string) {
	t.Helper()
	registration := coapObserveRegister(t, f, request, true)
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	observeRequest, err := f.conn.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	observeRequest.SetToken(token)
	observeRequest.SetObserve(0)
	notifications := make(chan coapNotification, 64)
	observation, err := f.conn.DoObserve(observeRequest, func(response *pool.Message) {
		body, readErr := response.ReadBody()
		if readErr != nil {
			return
		}
		format, _ := response.ContentFormat()
		observe, observeErr := response.Observe()
		select {
		case notifications <- coapNotification{body: append([]byte(nil), body...), format: format, observe: observe, hasObserve: observeErr == nil}:
		default:
		}
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		cancelCoAPSObservation(observation)
	})
	return observation, notifications, registration.ObservationToken
}

func TestCoAPSQuietHeartbeatCursorResumeAndCancel(t *testing.T) {
	f := newCoAPFixture(t)
	socket := f.config.Bindings[0].Address
	seedPinMutationMessage(t, socket, "#gateway-fixture", "coaps-quiet-retained")
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	request := core.Request{Version: 1, RequestID: "coaps-quiet-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	_, notifications, _ := startCoAPSObserve(t, f, request)
	ready := <-notifications
	var marker struct {
		Version       int    `json:"version"`
		CorrelationID string `json:"correlation_id"`
		Event         string `json:"event"`
	}
	if ready.format != message.AppJSON || !ready.hasObserve || ready.observe != 0 || json.Unmarshal(ready.body, &marker) != nil || marker.Version != 1 || marker.CorrelationID != "coaps-observe-"+request.RequestID || marker.Event != "ready" {
		t.Fatalf("CoAPS observe ready=%q format=%v observe=%d marker=%+v", ready.body, ready.format, ready.observe, marker)
	}
	first := coapObserveFrame(t, notifications, time.After(20*time.Second))
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || first.Page.Cursor == "" {
		t.Fatalf("CoAPS first watch=%+v", first)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)

	codec, err := auth.NewCursorCodec(f.config.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	principal := f.config.Principals[0]
	scope := auth.CursorScope{Realm: f.config.Realm, Principal: principal.ID, Binding: principal.Binding, Operation: core.Watch, Target: *request.Target}
	initialPosition, err := codec.Open(scope, first.Page.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := coapObserveFrame(t, notifications, time.After(20*time.Second))
	if heartbeat.Event != "page" || heartbeat.Sequence != 2 || heartbeat.Page == nil || len(heartbeat.Page.Messages) != 0 || len(heartbeat.Page.Events) != 0 {
		t.Fatalf("CoAPS quiet heartbeat=%+v", heartbeat)
	}
	position, err := codec.Open(scope, heartbeat.Page.Cursor)
	if err != nil || position != initialPosition {
		t.Fatalf("CoAPS heartbeat cursor=%q want=%q err=%v", position, initialPosition, err)
	}

	resume := coapRPC(t, f, core.Request{Version: 1, RequestID: "coaps-quiet-resume", Operation: core.Watch, Target: request.Target, Cursor: heartbeat.Page.Cursor, Limit: 8}, true)
	if resume.Result == nil || resume.Result.Page == nil || resume.Result.Page.Cursor == "" || len(resume.Result.Page.Messages) != 0 {
		t.Fatalf("CoAPS cursor resume=%+v", resume)
	}
	if !reflect.DeepEqual(resume.Result.Page.Messages, heartbeat.Page.Messages) {
		t.Fatalf("CoAPS cursor resume changed retained messages=%+v heartbeat=%+v", resume.Result.Page.Messages, heartbeat.Page.Messages)
	}
}

func TestCoAPSObserveReassemblesLargeWholePage(t *testing.T) {
	f := newCoAPFixture(t)
	author := dial(t, f.config.Bindings[0].Address, "coaps-large-observe-author", "", false, true)
	defer author.Close()
	for i := 0; i < 8; i++ {
		body := strings.Repeat("coaps-large-observe-"+string(rune('a'+i))+"-🙂", 110)
		if err := author.SendWithID("#gateway-fixture", body, "coaps-large-observe-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
		next(t, f.observer, func(event *irc.MessageEvent) bool {
			return event.From == "coaps-large-observe-author" && event.Message == body
		})
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	_, notifications, _ := startCoAPSObserve(t, f, core.Request{Version: 1, RequestID: "coaps-large-observe", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 16})
	ready := <-notifications
	var marker struct {
		Event string `json:"event"`
	}
	if ready.format != message.AppJSON || !ready.hasObserve || ready.observe != 0 || json.Unmarshal(ready.body, &marker) != nil || marker.Event != "ready" {
		t.Fatalf("CoAPS large observe ready=%q format=%v observe=%d", ready.body, ready.format, ready.observe)
	}
	page := coapObserveFrame(t, notifications, time.After(20*time.Second))
	if page.Event != "page" || page.Page == nil || len(page.Page.Messages) < 8 {
		t.Fatalf("CoAPS large observe page=%+v", page)
	}
	if !reflect.DeepEqual(page.Page.Messages, native) {
		t.Fatalf("CoAPS large observe changed native page: native=%+v page=%+v", native, page.Page.Messages)
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded) <= 16<<10 {
		t.Fatalf("CoAPS large observe response bytes=%d err=%v", len(encoded), err)
	}
}
