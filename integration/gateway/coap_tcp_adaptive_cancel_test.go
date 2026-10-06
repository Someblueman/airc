//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapClient "github.com/plgd-dev/go-coap/v3/tcp/client"
)

func TestCoAPTCPAdaptiveObserveCancelDuringBlock2(t *testing.T) {
	for _, mode := range []string{"carrier-close", "continuation-timeout"} {
		t.Run(mode, func(t *testing.T) { testCoAPTCPAdaptivePartial(t, mode) })
	}
}

func testCoAPTCPAdaptivePartial(t *testing.T, mode string) {
	f := newCoAPTCPFixture(t)
	seedCoAPTCPAdaptiveHistory(t, f)
	statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
	baseline := nativeObserverCount(t, statusClient)
	_ = f.conn.Close()
	firstMore := make(chan struct{}, 1)
	var seen atomic.Bool
	f.conn = dialCoAPTCPWithLimit(t, f.address, f.cert, 256, func(_ *coapClient.Conn, m *pool.Message) (bool, error) {
		observe, observeErr := m.Observe()
		format, _ := m.ContentFormat()
		if m.Code() == codes.Content && observeErr == nil && observe > 0 && format == message.AppOctets && m.HasOption(message.Block2) {
			block, err := m.GetOptionUint32(message.Block2)
			if err == nil && block&8 != 0 && seen.CompareAndSwap(false, true) {
				firstMore <- struct{}{}
				// Drop the first notification block before reassembly, so the
				// client cannot request the remaining blocks before closure.
				return true, nil
			}
		}
		return false, nil
	})
	request := coapWatchRequest("adaptive-cancel-watch")
	observation, notifications, token := startCoAPSTCPObserve(t, f, request)
	select {
	case ready := <-notifications:
		var marker struct {
			Event string `json:"event"`
		}
		if ready.err != nil || !ready.hasObserve || ready.observe != 0 || ready.format != message.AppJSON || json.Unmarshal(ready.body, &marker) != nil || marker.Event != "ready" {
			t.Fatalf("adaptive ready=%+v", ready)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("adaptive watch ready timeout")
	}
	select {
	case <-firstMore:
	case <-time.After(10 * time.Second):
		t.Fatal("adaptive watch did not expose a partial Block2 response")
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	// A copied observation token cannot retrieve this partial snapshot on a
	// different verified TLS association, even with valid CSM/blockwise setup.
	foreign := dialCoAPTCPWithLimit(t, f.address, f.cert, 256, func(*coapClient.Conn, *pool.Message) (bool, error) { return false, nil })
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	req, err := foreign.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.SetOptionString(message.URIQuery, "observation="+token)
	req.SetOptionUint32(message.Block2, 17)
	denied, err := foreign.Do(req)
	foreign.ReleaseMessage(req)
	if err != nil || denied.Code() != codes.BadRequest {
		cancel()
		t.Fatalf("foreign snapshot query was not denied: response=%v err=%v", denied, err)
	}
	foreign.ReleaseMessage(denied)
	cancel()
	_ = foreign.Close()
	closedAt := time.Now()
	if mode == "carrier-close" {
		if err := f.conn.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		select {
		case <-f.conn.Context().Done():
		case <-time.After(4 * time.Second):
			t.Fatal("unfetched notification did not close its carrier")
		}
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	if elapsed := time.Since(closedAt); elapsed >= 4*time.Second {
		t.Fatalf("adaptive cancellation did not release native lease promptly: %s", elapsed)
	}
	cancelCoAPSTCPObservation(observation)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("adaptive shutdown=%v", f.process.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("adaptive gateway did not join")
	}
	listener, err := net.Listen("tcp", f.address)
	if err != nil {
		t.Fatalf("adaptive TCP address did not rebind: %v", err)
	}
	_ = listener.Close()
}
