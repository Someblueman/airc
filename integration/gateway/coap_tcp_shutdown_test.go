//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"testing"
	"time"

	"github.com/plgd-dev/go-coap/v3/message/pool"
)

func TestCoAPTCPShutdownJoinsActiveWatchAndRebinds(t *testing.T) {
	f := newCoAPTCPFixture(t)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	registration := coapTCPObserveRegister(t, f, coapWatchRequest("coaps-tcp-shutdown-watch"), true)
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
	defer cancelCoAPSTCPObservation(observation)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("CoAPS TCP watch did not become active")
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("CoAPS TCP shutdown=%v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("CoAPS TCP gateway did not join after SIGINT")
	}
	listener, err := net.Listen("tcp", f.address)
	if err != nil {
		t.Fatalf("CoAPS TCP address was not released after joined shutdown: %v", err)
	}
	_ = listener.Close()
	waitNativeObserverCountAtMost(t, statusClient, baseline)
}
