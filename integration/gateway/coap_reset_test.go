//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapOptions "github.com/plgd-dev/go-coap/v3/options"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

type coapRequestMonitorOption func(*coapClient.Config)

func (o coapRequestMonitorOption) UDPClientApply(config *coapClient.Config) { o(config) }

type coapResetControl struct {
	captured <-chan int32
	release  chan<- struct{}
	written  <-chan error
}

func TestCoAPResetDropsNotificationAndReleasesLease(t *testing.T) {
	f := newCoAPFixture(t)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	watched, reset := dialCoAPResetMonitor(t, f.address, f.cert)
	f.conn = watched
	request := coapWatchRequest("coaps-reset-watch")
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
	defer cancelCoAPSObservation(observation)
	waitNativeObserverCount(t, statusClient, baseline+1)
	select {
	case mid := <-reset.captured:
		t.Logf("captured AGD1 notification MID=%d", mid)
	case <-ctx.Done():
		t.Fatal("CoAPS monitor did not observe an AGD1 notification")
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	reset.release <- struct{}{}
	select {
	case err := <-reset.written:
		if err != nil {
			t.Fatalf("RST write: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("CoAPS monitor did not write RST")
	}
	resetAt := time.Now()
	if err := waitNativeObserverCountBefore(t, statusClient, baseline, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(resetAt); elapsed >= 2*time.Second {
		t.Fatalf("RST did not release native lease promptly: %s", elapsed)
	}
	result := coapRPC(t, f, core.Request{Version: 1, RequestID: "coaps-reset-followup", Operation: core.TopicRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}, true)
	if result.Result == nil || result.Result.Topic == nil {
		t.Fatalf("post-RST RPC result=%+v", result)
	}
}

func dialCoAPResetMonitor(t *testing.T, address string, cert testtls.Certificate) (*coapClient.Conn, coapResetControl) {
	t.Helper()
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
	options := coapdtls.NewDTLSClientOptions(
		piondtls.WithRootCAs(cert.Client.RootCAs),
		piondtls.WithServerName("localhost"),
		piondtls.WithSupportedProtocols(coapALPN),
		piondtls.WithExtendedMasterSecret(piondtls.RequireExtendedMasterSecret),
		piondtls.WithCipherSuites(piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, piondtls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384),
		piondtls.WithMTU(1200),
	)
	// Blockwise rewrites outgoing Reset messages as fresh CON requests. Keep it
	// disabled here so the captured MID is preserved on the wire.
	conn, err := coapdtls.Dial(address, options, coapOptions.WithBlockwise(false, blockwise.SZX1024, 5*time.Second), coapRequestMonitorOption(func(config *coapClient.Config) { config.RequestMonitor = monitor }))
	if err != nil {
		t.Fatal(err)
	}
	dtlsConn, ok := conn.NetConn().(*piondtls.Conn)
	if !ok {
		_ = conn.Close()
		t.Fatalf("CoAPS connection is %T, want *dtls.Conn", conn.NetConn())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := dtlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("RST writer did not join after CoAPS close")
		}
	})
	return conn, coapResetControl{captured: captured, release: release, written: resetWritten}
}

func waitNativeObserverCountBefore(t *testing.T, statusClient *irc.Client, want int, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if nativeObserverCount(t, statusClient) <= want {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("native observer count remained above %d", want)
}
