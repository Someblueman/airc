//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/pool"
)

func TestCoAPSObserveRegisterLiveNotificationAndCancel(t *testing.T) {
	f := newCoAPFixture(t)
	statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
	baseline := nativeObserverCount(t, statusClient)
	request := core.Request{Version: 1, RequestID: "coaps-observe", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	registration := coapObserveRegister(t, f, request, true)
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 8 {
		t.Fatalf("CoAPS observation token bytes=%d", len(token))
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	observeRequest, err := f.conn.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	observeRequest.SetToken(token)
	observeRequest.SetObserve(0)
	notifications := make(chan coapNotification, 32)
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
		t.Fatal(err)
	}
	defer cancelCoAPSObservation(observation)

	select {
	case ready := <-notifications:
		var marker struct {
			Version       int    `json:"version"`
			CorrelationID string `json:"correlation_id"`
			Event         string `json:"event"`
		}
		if ready.format != message.AppJSON || !ready.hasObserve || ready.observe != 0 || json.Unmarshal(ready.body, &marker) != nil || marker.Version != 1 || marker.CorrelationID != "coaps-observe-"+request.RequestID || marker.Event != "ready" {
			t.Fatalf("CoAPS observe ready marker=%q format=%v observe=%d parsed=%+v", ready.body, ready.format, ready.observe, marker)
		}
	case <-ctx.Done():
		t.Fatal("CoAPS observe did not deliver ready marker")
	}
	initial := coapObserveFrame(t, notifications, time.After(8*time.Second))
	if initial.Event != "page" || initial.Sequence != 1 || initial.Page == nil {
		t.Fatalf("CoAPS observe initial page=%+v", initial)
	}

	author := dial(t, f.config.Bindings[0].Address, "coaps-observe-author", "", false, true)
	defer author.Close()
	body := "CoAPS live observation 🙂"
	if err := author.SendWithID("#gateway-fixture", body, "coaps-observe-live"); err != nil {
		t.Fatal(err)
	}
	next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == body })
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	live := coapObserveFrame(t, notifications, deadline.C)
	if live.Event != "page" || live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) != 1 || live.Page.Messages[0].Message != body || live.Page.Messages[0].Target != "#gateway-fixture" {
		t.Fatalf("CoAPS live page=%+v", live)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if len(native) == 0 || live.Page.Messages[0].ID != native[len(native)-1].ID {
		t.Fatalf("CoAPS live native correlation page=%+v native=%+v", live.Page.Messages, native)
	}
	expected := *native[len(native)-1]
	expected.Seq, expected.Receipt = 0, nil
	if !reflect.DeepEqual(live.Page.Messages[0], &expected) {
		t.Fatalf("CoAPS whole live record=%+v native=%+v", live.Page.Messages[0], native[len(native)-1])
	}
	cancelCtx, cancelObservation := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelObservation()
	if err := observation.Cancel(cancelCtx); err != nil {
		t.Fatalf("CoAPS Observe1 cancellation: %v", err)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
}

func coapObserveFrame(t *testing.T, notifications <-chan coapNotification, done <-chan time.Time) adapter.FrameResponse {
	t.Helper()
	var id [16]byte
	var total, count int
	var parts [][]byte
	for {
		select {
		case notification := <-notifications:
			if notification.format != message.AppOctets || !notification.hasObserve {
				t.Fatalf("CoAPS notification format=%v observe=%v", notification.format, notification.hasObserve)
			}
			fragmentID, fragmentTotal, index, fragmentCount, payload, err := webRTCResponseFragment(notification.body)
			if err != nil {
				continue
			}
			if count == 0 {
				id, total, count = fragmentID, fragmentTotal, fragmentCount
				parts = make([][]byte, count)
			} else if fragmentID != id || fragmentTotal != total || fragmentCount != count {
				t.Fatalf("CoAPS notification assembly header mismatch")
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
				t.Fatalf("CoAPS notification frame bytes=%d want=%d", len(body), total)
			}
			return response
		case <-done:
			t.Fatal("CoAPS observe live notification timeout")
			return adapter.FrameResponse{}
		}
	}
}

func TestCoAPSObservationTokenIsLowercaseHex(t *testing.T) {
	f := newCoAPFixture(t)
	registration := coapObserveRegister(t, f, core.Request{Version: 1, RequestID: "coaps-observe-token", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}, false)
	if _, err := hex.DecodeString(registration.ObservationToken); err != nil || len(registration.ObservationToken) != 16 {
		t.Fatalf("observation token is not canonical: %q err=%v", registration.ObservationToken, err)
	}
}
