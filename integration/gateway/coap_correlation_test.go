//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
)

func TestCoAPCorrelationCollisionClosesPeerBeforeMutation(t *testing.T) {
	f := newCoAPFixture(t)
	registration := coapObserveRegister(t, f, coapWatchRequest("coaps-collision-pending"), true)
	attack := core.Request{Version: 1, RequestID: "coaps-collision-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "CoAPS collision positive control"}
	frame := adapter.FrameRequest{Version: 1, CorrelationID: registration.CorrelationID, Profile: "signed", Envelope: f.credentials.token(t, attack, false)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	response, err := f.conn.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
	if response != nil {
		f.conn.ReleaseMessage(response)
	}
	if err == nil {
		t.Fatal("active correlation collision did not close peer")
	}
	rpcNoJobs(t, f.config.StateDir)
	if records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, attack.Target.Name); len(records) != 0 {
		t.Fatalf("collision admitted native mutation: %+v", records)
	}
	// The same otherwise-valid intent succeeds with a fresh peer/correlation.
	fresh := f
	fresh.conn = dialCoAPS(t, f.address, f.cert)
	accepted := coapAccepted(t, fresh, attack, false)
	native := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == accepted.MessageID })
	if accepted.SendAttempts != 1 || native.Message != attack.Message {
		t.Fatalf("fresh control accepted=%+v native=%+v", accepted, native)
	}
}
