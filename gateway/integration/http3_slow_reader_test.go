//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/qlogwriter"
)

func TestHTTP3SlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	t.Parallel()
	f := newHTTP3GatewayFixture(t)
	author := dial(t, f.config.Upstream.Address, "http3-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	pressure := make(chan quicPressureEvent, 1)
	trace := &quicPressureTrace{events: pressure}
	transport := &http3.Transport{TLSClientConfig: f.cert.Client.Clone(), QUICConfig: &quic.Config{InitialStreamReceiveWindow: 65536, MaxStreamReceiveWindow: 65536, InitialConnectionReceiveWindow: 65536, MaxConnectionReceiveWindow: 65536, Tracer: func(context.Context, bool, quic.ConnectionID) qlogwriter.Trace { return trace }}}
	defer transport.Close()
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	request := core.Request{Version: 1, RequestID: "http3-slow", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/rpc/events", bytes.NewReader(rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request))))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(req, f.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 {
		t.Fatalf("status/protocol=%d/%d", response.StatusCode, response.ProtoMajor)
	}
	readRPCSSE(t, bufio.NewReader(response.Body))
	waitNativeObserverCount(t, statusClient, baseline+1)
	started := time.Now()
	var event quicPressureEvent
	seeded := 0
	for seeded < 40 {
		select {
		case event = <-pressure:
		default:
		}
		id := fmt.Sprintf("http3-slow-%d", seeded)
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("slow-%d %s", seeded, strings.Repeat("x", 3000)), id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(receipt *irc.SendReceiptEvent) bool { return receipt.RequestID == id })
		if receipt.Receipt == nil || !receipt.Receipt.Accepted {
			t.Fatalf("native pressure seed was not accepted: %+v", receipt)
		}
		seeded++
		// QUIC can accept a final packet into its send buffer after reporting
		// pressure. One more accepted 3000-byte post exceeds that buffer and
		// forces the application write to wait for its unchanged deadline.
		if !event.at.IsZero() {
			break
		}
	}
	if event.at.IsZero() {
		select {
		case event = <-pressure:
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP/3 peer did not report real flow-control pressure")
		}
	}
	if event.limit < 65536 || event.at.Before(started) {
		t.Fatalf("unexpected peer pressure: %+v", event)
	}
	t.Logf("peer blocked on %s credit at %d bytes after %d accepted native posts", event.kind, event.limit, seeded)
	// The fixed QUIC receive window prevents background transport reads from
	// concealing the blocked application stream. Cleanup must precede lease expiry.
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	if ctx.Err() != nil || time.Since(event.at) < 2*time.Second {
		t.Fatalf("cleanup did not follow peer write pressure: elapsed=%s ctx=%v", time.Since(event.at), ctx.Err())
	}
	if elapsed := time.Since(started); elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("write-bound cleanup elapsed=%s", elapsed)
	}
}
