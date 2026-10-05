//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message/pool"
)

func TestCoAPShutdownJoinsAndRebindsUDPAddress(t *testing.T) {
	f := newCoAPFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	registration := coapObserveRegister(t, f, coapWatchRequest("coaps-shutdown-watch"), true)
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := f.conn.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	request.SetToken(token)
	request.SetObserve(0)
	ready := make(chan struct{}, 1)
	observation, err := f.conn.DoObserve(request, func(response *pool.Message) {
		if _, err := response.ReadBody(); err == nil {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancelCoAPSObservation(observation)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("CoAPS watch did not become active before shutdown")
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("CoAPS gateway shutdown=%v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("CoAPS gateway did not join after SIGINT")
	}
	conn, err := net.ListenPacket("udp", f.address)
	if err != nil {
		t.Fatalf("CoAPS UDP address was not released after joined shutdown: %v", err)
	}
	_ = conn.Close()
	waitNativeObserverCountAtMost(t, f.config.Bindings[0].Address, baseline)
}

func coapWatchRequest(id string) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
}
