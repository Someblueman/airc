//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
)

func TestPlainCoAPTCPActiveLongPollJoinsOnSIGINTAndRebinds(t *testing.T) {
	f := newCoAPTCPProfileFixture(t, true)
	status := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, status)
	request := core.Request{Version: 1, RequestID: "plain-coap-shutdown", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 1}
	body, err := json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		_ = f.conn.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("CoAP caller did not join")
			}
		}
	}()
	go func() {
		response, err := f.conn.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
		if response != nil {
			f.conn.ReleaseMessage(response)
		}
		done <- err
	}()
	waitNativeObserverCount(t, status, baseline+1)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal("CoAP shutdown", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("CoAP gateway did not join")
	}
	select {
	case err := <-done:
		joined = true
		if err == nil {
			t.Fatal("interrupted poll unexpectedly completed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CoAP interrupted caller did not join")
	}
	listener, err := net.Listen("tcp", f.address)
	if err != nil {
		t.Fatal("CoAP TCP port retained", err)
	}
	_ = listener.Close()
	waitNativeObserverCountAtMost(t, status, baseline)
	rpcNoJobs(t, f.config.StateDir)
}
