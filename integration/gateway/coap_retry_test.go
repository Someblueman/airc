//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
)

func TestCoAPSStableRequestReplayKeepsSingleNativeIntake(t *testing.T) {
	f := newCoAPFixture(t)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "coaps-replay-send", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "CoAPS stable replay 🙂"}
	first := coapAccepted(t, f, request, true)
	second := coapRPC(t, f, request, true)
	if second.Result == nil || second.Result.Result == nil || second.Result.Result.State != core.Accepted || second.Result.Result.MessageID != first.MessageID || second.Result.Result.SendAttempts != 1 {
		t.Fatalf("CoAPS stable replay first=%+v second=%+v", first, second)
	}
	records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != first.MessageID || records[0].Message != request.Message || records[0].Target != request.Target.Name {
		t.Fatalf("CoAPS stable replay native records=%+v", records)
	}
}

func TestCoAPSResponseLossReplaysStableRequestWithoutDuplicateNativeIntake(t *testing.T) {
	f := newCoAPFixture(t)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "coaps-response-loss", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "CoAPS response intentionally lost 🙂"}
	frame := frameRequest{Version: 1, CorrelationID: "coaps-loss", Profile: "plain", Request: f.credentials.token(t, request, true)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	dropped := make(chan struct{}, 1)
	dropConn := dialCoAPSWithResponseDrop(t, f.address, f.cert, dropped)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	req, err := dropConn.NewPostRequest(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.SetType(message.Confirmable)
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		_ = dropConn.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("CoAPS response-loss request cleanup timed out")
			}
		}
		dropConn.ReleaseMessage(req)
	}()
	go func() {
		response, doErr := dropConn.Do(req)
		if response != nil {
			dropConn.ReleaseMessage(response)
		}
		done <- doErr
	}()
	select {
	case <-dropped:
	case <-time.After(5 * time.Second):
		t.Fatal("CoAPS response-loss monitor did not observe a real content response")
	}
	if err := dropConn.Close(); err != nil {
		t.Logf("closing response-loss connection: %v", err)
	}
	select {
	case doErr := <-done:
		joined = true
		if doErr == nil {
			t.Fatal("CoAPS response-loss request unexpectedly succeeded after dropping its response")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CoAPS response-loss request did not stop after connection close")
	}
	native := next(t, f.observer, func(event *irc.MessageEvent) bool {
		return event.Message == request.Message && event.Target == request.Target.Name
	})
	retry := f
	retry.conn = dialCoAPS(t, f.address, f.cert)
	response := coapRPC(t, retry, request, true)
	if response.Result == nil || response.Result.Result == nil || response.Result.Result.State != core.Accepted || response.Result.Result.MessageID != native.ID || response.Result.Result.SendAttempts != 1 {
		t.Fatalf("CoAPS response-loss replay=%+v native=%+v", response, native)
	}
	records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != native.ID || records[0].From != native.From || records[0].Target != request.Target.Name || records[0].Message != request.Message {
		t.Fatalf("CoAPS response-loss native records=%+v native=%+v", records, native)
	}
}
