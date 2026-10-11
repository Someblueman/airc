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
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

func TestCoAPUDPActiveWatchJoinsOnSIGINT(t *testing.T) {
	f := newPacketFixture(t, "coap_udp", nil)
	client := coapUDPClient(t, f)
	socket := f.config.Upstream.Address
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	// A quiet long-poll watch holds a native lease while it waits.
	watch := core.Request{Version: 1, RequestID: "udp-shutdown-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "recent", Limit: 8}
	body, err := json.Marshal(frameRequest{Version: 1, CorrelationID: watch.RequestID, Profile: "plain", Request: f.credentials.token(t, watch, true)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if response, err := client.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body)); err == nil {
			client.ReleaseMessage(response)
		}
	}()
	waitNativeObserverCount(t, statusClient, baseline+1)

	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("UDP SIGINT: %v %s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UDP SIGINT did not join workers")
	}
	cancel()
	<-done
	bound, err := net.ListenPacket("udp", f.address)
	if err != nil {
		t.Fatalf("UDP listener retained after SIGINT: %v", err)
	}
	bound.Close()
	waitNativeLeaseGone(t, statusClient, baseline)
	rpcNoJobs(t, f.config.StateDir)
}

func coapUDPClient(t *testing.T, f udpFixture) *coapClient.Conn {
	t.Helper()
	client, err := coapUDP.Dial(f.address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
