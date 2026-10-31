//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapOptions "github.com/plgd-dev/go-coap/v3/options"
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

type coapRequestMonitorOption func(*coapClient.Config)

func (o coapRequestMonitorOption) UDPClientApply(config *coapClient.Config) { o(config) }

type coapResetControl struct {
	captured <-chan int32
	release  chan<- struct{}
	written  <-chan error
}

// A Reset of a notification ends its watch and releases its native lease.
func TestCoAPUDPResetDropsNotificationAndReleasesLease(t *testing.T) {
	t.Parallel()
	p := newPacketFixture(t, "coap_udp", nil)
	monitor, reset, done := coapResetMonitor()
	// Blockwise rewrites outgoing Reset messages as fresh CON requests. Keep it
	// disabled here so the captured MID is preserved on the wire.
	conn, err := coapUDP.Dial(p.address, coapOptions.WithBlockwise(false, blockwise.SZX1024, 5*time.Second), coapRequestMonitorOption(func(config *coapClient.Config) { config.RequestMonitor = monitor }))
	if err != nil {
		t.Fatal(err)
	}
	joinCoAPResetMonitor(t, conn, done)
	testCoAPResetReleasesLease(t, coapFixture{config: p.config, credentials: p.credentials, conn: conn}, reset, "coap-udp-reset-watch")
}

// testCoAPResetReleasesLease watches over f.conn, whose monitor drops the
// first AGD1 notification and answers it with a Reset when released.
func testCoAPResetReleasesLease(t *testing.T, f coapFixture, reset coapResetControl, requestID string) {
	t.Helper()
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	watched := f.conn
	request := coapWatchRequest(requestID)
	registration := coapObserveRegister(t, f, request, true)
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	observe, err := watched.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	observe.SetToken(token)
	observe.SetObserve(0)
	observation, err := watched.DoObserve(observe, func(response *pool.Message) { _, _ = response.ReadBody() })
	if err != nil {
		t.Fatal(err)
	}
	defer cancelCoAPObservation(observation)
	waitNativeObserverCount(t, statusClient, baseline+1)
	select {
	case mid := <-reset.captured:
		t.Logf("captured AGD1 notification MID=%d", mid)
	case <-ctx.Done():
		t.Fatal("CoAP monitor did not observe an AGD1 notification")
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	reset.release <- struct{}{}
	select {
	case err := <-reset.written:
		if err != nil {
			t.Fatalf("RST write: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("CoAP monitor did not write RST")
	}
	resetAt := time.Now()
	if err := waitNativeObserverCountBefore(t, statusClient, baseline, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(resetAt); elapsed >= 2*time.Second {
		t.Fatalf("RST did not release native lease promptly: %s", elapsed)
	}
	result := coapRPC(t, f, core.Request{Version: 1, RequestID: "coap-reset-followup", Operation: core.TopicRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}, true)
	if result.Result == nil || result.Result.Topic == nil {
		t.Fatalf("post-RST RPC result=%+v", result)
	}
}

// coapResetMonitor is a client request monitor that drops the first AGD1
// notification and, once released, answers it with a Reset of its MID. done
// closes when that Reset writer returns.
func coapResetMonitor() (func(*coapClient.Conn, *pool.Message) (bool, error), coapResetControl, <-chan struct{}) {
	captured := make(chan int32, 1)
	release := make(chan struct{})
	resetWritten := make(chan error, 1)
	done := make(chan struct{})
	var once sync.Once
	monitor := func(conn *coapClient.Conn, req *pool.Message) (bool, error) {
		format, _ := req.ContentFormat()
		if req.Code() != codes.Content || format != message.AppOctets {
			return false, nil
		}
		once.Do(func() {
			mid := req.MessageID()
			captured <- mid
			go func() {
				defer close(done)
				select {
				case <-release:
				case <-conn.Context().Done():
					resetWritten <- conn.Context().Err()
					return
				}
				resetCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				reset := conn.AcquireMessage(resetCtx)
				reset.SetType(message.Reset)
				reset.SetCode(codes.Empty)
				reset.SetMessageID(mid)
				writeErr := conn.WriteMessage(reset)
				conn.ReleaseMessage(reset)
				resetWritten <- writeErr
			}()
		})
		return true, nil
	}
	return monitor, coapResetControl{captured: captured, release: release, written: resetWritten}, done
}

// joinCoAPResetMonitor closes conn at cleanup and joins its Reset writer.
func joinCoAPResetMonitor(t *testing.T, conn *coapClient.Conn, done <-chan struct{}) {
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("RST writer did not join after CoAP close")
		}
	})
}

func waitNativeObserverCountBefore(t *testing.T, statusClient *irc.Client, want int, timeout time.Duration) error {
	t.Helper()
	if testwait.Poll(timeout, 50*time.Millisecond, func() bool { return nativeObserverCount(t, statusClient) <= want }) {
		return nil
	}
	return fmt.Errorf("native observer count remained above %d", want)
}
