//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
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

func startCoAPObserve(t *testing.T, f coapFixture, request core.Request) (coapClient.Observation, <-chan coapNotification, string) {
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
		cancelCoAPObservation(observation)
	})
	return observation, notifications, registration.ObservationToken
}

// A quiet watch over CoAP Observe heartbeats an empty page whose cursor holds
// its position, Observe 1 cancels it and releases its lease, and the heartbeat
// cursor resumes the watch as a plain request.
func TestCoAPUDPQuietHeartbeatCursorResumeAndCancel(t *testing.T) {
	f := newCoAPFixture(t)
	socket := f.config.Upstream.Address
	seedPinMutationMessage(t, socket, "#gateway-fixture", "coap-quiet-retained")
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	request := core.Request{Version: 1, RequestID: "coap-quiet-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	observation, notifications, _ := startCoAPObserve(t, f, request)
	ready := <-notifications
	var marker struct {
		Version       int    `json:"version"`
		CorrelationID string `json:"correlation_id"`
		Event         string `json:"event"`
	}
	if ready.format != message.AppJSON || !ready.hasObserve || ready.observe != 0 || json.Unmarshal(ready.body, &marker) != nil || marker.Version != 1 || marker.CorrelationID != "coap-observe-"+request.RequestID || marker.Event != "ready" {
		t.Fatalf("CoAP observe ready=%q format=%v observe=%d marker=%+v", ready.body, ready.format, ready.observe, marker)
	}
	first := coapObserveFrame(t, notifications, time.After(20*time.Second))
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || first.Page.Cursor == "" {
		t.Fatalf("CoAP first watch=%+v", first)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)

	codec, err := auth.NewCursorCodec(f.config.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	scope := auth.CursorScope{Realm: f.config.Realm, Author: fixtureIdentity(t, f.config).Author.Scope(), Operation: core.Watch, Target: *request.Target}
	initialPosition, err := codec.Open(scope, first.Page.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := coapObserveFrame(t, notifications, time.After(20*time.Second))
	if heartbeat.Event != "page" || heartbeat.Sequence != 2 || heartbeat.Page == nil || len(heartbeat.Page.Messages) != 0 || len(heartbeat.Page.Events) != 0 {
		t.Fatalf("CoAP quiet heartbeat=%+v", heartbeat)
	}
	position, err := codec.Open(scope, heartbeat.Page.Cursor)
	if err != nil || position != initialPosition {
		t.Fatalf("CoAP heartbeat cursor=%q want=%q err=%v", position, initialPosition, err)
	}

	cancelCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	if err := observation.Cancel(cancelCtx); err != nil {
		cancel()
		t.Fatalf("CoAP Observe1 cancellation: %v", err)
	}
	cancel()
	waitNativeObserverCountAtMost(t, statusClient, baseline)

	resume := coapRPC(t, f, core.Request{Version: 1, RequestID: "coap-quiet-resume", Operation: core.Watch, Target: request.Target, Cursor: heartbeat.Page.Cursor, Limit: 8}, true)
	if resume.Result == nil || resume.Result.Page == nil || resume.Result.Page.Cursor == "" || len(resume.Result.Page.Messages) != 0 {
		t.Fatalf("CoAP cursor resume=%+v", resume)
	}
	if !reflect.DeepEqual(resume.Result.Page.Messages, heartbeat.Page.Messages) {
		t.Fatalf("CoAP cursor resume changed retained messages=%+v heartbeat=%+v", resume.Result.Page.Messages, heartbeat.Page.Messages)
	}
}

// coapObserveFrame reassembles the next notified frame from its AGD1
// fragments.
func coapObserveFrame(t *testing.T, notifications <-chan coapNotification, done <-chan time.Time) adapter.FrameResponse {
	t.Helper()
	var assembly agd1Assembly
	for {
		select {
		case notification := <-notifications:
			if notification.format != message.AppOctets || !notification.hasObserve {
				t.Fatalf("CoAP notification format=%v observe=%v", notification.format, notification.hasObserve)
			}
			body := assembly.add(t, notification.body)
			if body == nil {
				continue
			}
			var response adapter.FrameResponse
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatalf("CoAP notification frame: %v", err)
			}
			return response
		case <-done:
			t.Fatal("CoAP observe live notification timeout")
			return adapter.FrameResponse{}
		}
	}
}
