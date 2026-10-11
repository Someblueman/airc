//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapClient "github.com/plgd-dev/go-coap/v3/tcp/client"
)

func TestCoAPTCPAdaptiveObservationRouteAdmission(t *testing.T) {
	f := newCoAPTCPFixture(t)
	_ = f.conn.Close()
	f.conn = dialCoAPTCPWithLimit(t, f.address, f.cert, 64, func(*coapClient.Conn, *pool.Message) (bool, error) { return false, nil })
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	registration := coapTCPObserveRegister(t, f, coapWatchRequest("adaptive-route"), true)
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, queries := range [][]string{nil, {"observation=0000000000000000"}, {"", "observation=" + registration.ObservationToken}} {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		req, err := f.conn.NewGetRequest(ctx, "/v1/coap/observe")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.SetToken(token)
		req.SetObserve(0)
		for _, query := range queries {
			req.AddOptionString(message.URIQuery, query)
		}
		response, err := f.conn.Do(req)
		f.conn.ReleaseMessage(req)
		if err != nil || response.Code() != codes.BadRequest {
			cancel()
			t.Fatalf("invalid observation route accepted: queries=%v response=%v err=%v", queries, response, err)
		}
		f.conn.ReleaseMessage(response)
		cancel()
		if got := nativeObserverCount(t, statusClient); got != baseline {
			t.Fatalf("invalid route opened a native lease: got=%d baseline=%d", got, baseline)
		}
	}
	// The very same registered intent succeeds once its route matches, proving
	// the preceding denials were route validation, not an unusable account.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	req, err := f.conn.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	req.SetToken(token)
	req.SetObserve(0)
	req.SetOptionString(message.URIQuery, "observation="+registration.ObservationToken)
	ready := make(chan string, 1)
	notifications := make(chan coapTCPNotification, 64)
	observation, err := f.conn.DoObserve(req, func(m *pool.Message) {
		var marker struct {
			Event string `json:"event"`
		}
		body, readErr := m.ReadBody()
		format, _ := m.ContentFormat()
		observe, observeErr := m.Observe()
		if format == message.AppOctets {
			select {
			case notifications <- coapTCPNotification{body: append([]byte(nil), body...), err: readErr, format: format, observe: observe, hasObserve: observeErr == nil}:
			default:
			}
		}
		if json.Unmarshal(body, &marker) == nil && marker.Event == "ready" {
			select {
			case ready <- marker.Event:
			default:
			}
		}
	})
	f.conn.ReleaseMessage(req)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelCoAPSTCPObservation(observation)
	select {
	case <-ready:
	case <-time.After(4 * time.Second):
		t.Fatal("valid observation route did not deliver readiness")
	}
	page := coapTCPObserveFrame(t, notifications, time.After(4*time.Second))
	if page.Page == nil || page.Sequence != 1 {
		t.Fatalf("valid route did not deliver a complete page: %+v", page)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	cancelCtx, finish := context.WithTimeout(t.Context(), 3*time.Second)
	err = observation.Cancel(cancelCtx)
	finish()
	if err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
}
