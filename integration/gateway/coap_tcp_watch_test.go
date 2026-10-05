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

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapClient "github.com/plgd-dev/go-coap/v3/net/client"
)

type coapTCPNotification struct {
	body       []byte
	err        error
	format     message.MediaType
	observe    uint32
	hasObserve bool
}

func startCoAPSTCPObserve(t *testing.T, f coapTCPFixture, request core.Request) (coapClient.Observation, <-chan coapTCPNotification, string) {
	t.Helper()
	registration := coapTCPObserveRegister(t, f, request, true)
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
	notifications := make(chan coapTCPNotification, 64)
	observation, err := f.conn.DoObserve(observeRequest, func(response *pool.Message) {
		body, readErr := response.ReadBody()
		format, _ := response.ContentFormat()
		observe, observeErr := response.Observe()
		select {
		case notifications <- coapTCPNotification{body: append([]byte(nil), body...), err: readErr, format: format, observe: observe, hasObserve: observeErr == nil}:
		default:
		}
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		cancelCoAPSTCPObservation(observation)
	})
	return observation, notifications, registration.ObservationToken
}

func coapTCPObserveFrame(t *testing.T, notifications <-chan coapTCPNotification, done <-chan time.Time) adapter.FrameResponse {
	t.Helper()
	var id [16]byte
	var total, count int
	var parts [][]byte
	for {
		select {
		case notification := <-notifications:
			if notification.err != nil {
				t.Fatalf("CoAPS TCP notification read: %v", notification.err)
			}
			if notification.format != message.AppOctets || !notification.hasObserve {
				t.Fatalf("CoAPS TCP notification format=%v observe=%v", notification.format, notification.hasObserve)
			}
			fragmentID, fragmentTotal, index, fragmentCount, payload, err := webRTCResponseFragment(notification.body)
			if err != nil {
				t.Fatalf("CoAPS TCP malformed AGD1 notification: %v", err)
			}
			if count == 0 {
				id, total, count = fragmentID, fragmentTotal, fragmentCount
				parts = make([][]byte, count)
			} else if fragmentID != id || fragmentTotal != total || fragmentCount != count {
				t.Fatalf("CoAPS TCP notification assembly header mismatch")
			}
			if parts[index] == nil {
				parts[index] = append([]byte(nil), payload...)
			}
			complete := true
			for _, part := range parts {
				if part == nil {
					complete = false
					break
				}
			}
			if !complete {
				continue
			}
			body := make([]byte, 0, total)
			for _, part := range parts {
				body = append(body, part...)
			}
			var response adapter.FrameResponse
			if len(body) != total || json.Unmarshal(body, &response) != nil {
				t.Fatalf("CoAPS TCP notification frame bytes=%d want=%d", len(body), total)
			}
			return response
		case <-done:
			t.Fatal("CoAPS TCP observe notification timeout")
			return adapter.FrameResponse{}
		}
	}
}

func TestCoAPSTCPQuietHeartbeatCursorResumeAndCancel(t *testing.T) {
	f := newCoAPTCPFixture(t)
	socket := f.config.Bindings[0].Address
	seedPinMutationMessage(t, socket, "#gateway-fixture", "coaps-tcp-quiet-retained")
	baseline := nativeObserverCount(t, socket)
	request := core.Request{Version: 1, RequestID: "coaps-tcp-quiet-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	observation, notifications, _ := startCoAPSTCPObserve(t, f, request)
	var ready coapTCPNotification
	select {
	case ready = <-notifications:
	case <-time.After(10 * time.Second):
		t.Fatal("CoAPS TCP observe ready timeout")
	}
	var marker struct {
		Version       int    `json:"version"`
		CorrelationID string `json:"correlation_id"`
		Event         string `json:"event"`
	}
	if ready.format != message.AppJSON || !ready.hasObserve || ready.observe != 0 || json.Unmarshal(ready.body, &marker) != nil || marker.Version != 1 || marker.CorrelationID != "coaps-observe-"+request.RequestID || marker.Event != "ready" {
		t.Fatalf("CoAPS observe ready=%q format=%v observe=%d marker=%+v", ready.body, ready.format, ready.observe, marker)
	}
	first := coapTCPObserveFrame(t, notifications, time.After(20*time.Second))
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || first.Page.Cursor == "" {
		t.Fatalf("CoAPS first watch=%+v", first)
	}
	waitNativeObserverCount(t, socket, baseline+1)

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
	heartbeat := coapTCPObserveFrame(t, notifications, time.After(20*time.Second))
	if heartbeat.Event != "page" || heartbeat.Sequence != 2 || heartbeat.Page == nil || len(heartbeat.Page.Messages) != 0 || len(heartbeat.Page.Events) != 0 {
		t.Fatalf("CoAPS quiet heartbeat=%+v", heartbeat)
	}
	position, err := codec.Open(scope, heartbeat.Page.Cursor)
	if err != nil || position != initialPosition {
		t.Fatalf("CoAPS heartbeat cursor=%q want=%q err=%v", position, initialPosition, err)
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := observation.Cancel(cancelCtx); err != nil {
		cancel()
		t.Fatalf("CoAPS TCP Observe1 cancellation: %v", err)
	}
	cancel()
	waitNativeObserverCountAtMost(t, socket, baseline)

	resume := coapTCPRPC(t, f, core.Request{Version: 1, RequestID: "coaps-tcp-quiet-resume", Operation: core.Watch, Target: request.Target, Cursor: heartbeat.Page.Cursor, Limit: 8}, true)
	if resume.Result == nil || resume.Result.Page == nil || resume.Result.Page.Cursor == "" || len(resume.Result.Page.Messages) != 0 {
		t.Fatalf("CoAPS cursor resume=%+v", resume)
	}
	if !reflect.DeepEqual(resume.Result.Page.Messages, heartbeat.Page.Messages) {
		t.Fatalf("CoAPS cursor resume changed retained messages=%+v heartbeat=%+v", resume.Result.Page.Messages, heartbeat.Page.Messages)
	}
}

func TestCoAPSTCPObserveReassemblesLargeWholePage(t *testing.T) {
	f := newCoAPTCPFixture(t)
	author := dial(t, f.config.Bindings[0].Address, "coaps-tcp-large-observe-author", "", false, true)
	defer author.Close()
	for i := 0; i < 8; i++ {
		body := strings.Repeat("coaps-tcp-large-observe-"+string(rune('a'+i))+"-🙂", 110)
		if err := author.SendWithID("#gateway-fixture", body, "coaps-tcp-large-observe-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
		next(t, f.observer, func(event *irc.MessageEvent) bool {
			return event.From == "coaps-tcp-large-observe-author" && event.Message == body
		})
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	_, notifications, _ := startCoAPSTCPObserve(t, f, core.Request{Version: 1, RequestID: "coaps-tcp-large-observe", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 16})
	var ready coapTCPNotification
	select {
	case ready = <-notifications:
	case <-time.After(10 * time.Second):
		t.Fatal("CoAPS TCP large observe ready timeout")
	}
	var marker struct {
		Event string `json:"event"`
	}
	if ready.format != message.AppJSON || !ready.hasObserve || ready.observe != 0 || json.Unmarshal(ready.body, &marker) != nil || marker.Event != "ready" {
		t.Fatalf("CoAPS large observe ready=%q format=%v observe=%d", ready.body, ready.format, ready.observe)
	}
	page := coapTCPObserveFrame(t, notifications, time.After(20*time.Second))
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
