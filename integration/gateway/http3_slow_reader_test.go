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

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func TestHTTP3SlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	author := dial(t, f.config.Bindings[0].Address, "http3-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
	baseline := nativeObserverCount(t, statusClient)
	transport := &http3.Transport{TLSClientConfig: f.cert.Client.Clone(), QUICConfig: &quic.Config{InitialStreamReceiveWindow: 65536, MaxStreamReceiveWindow: 65536, InitialConnectionReceiveWindow: 65536, MaxConnectionReceiveWindow: 65536}}
	defer transport.Close()
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	request := core.Request{Version: 1, RequestID: "http3-slow", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/rpc/events", bytes.NewReader(rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
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
	for i := 0; i < 40; i++ {
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("slow-%d %s", i, strings.Repeat("x", 3000)), fmt.Sprintf("http3-slow-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	// The fixed QUIC receive window prevents background transport reads from
	// concealing the blocked application stream. Cleanup must precede lease expiry.
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	if elapsed := time.Since(started); elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("write-bound cleanup elapsed=%s", elapsed)
	}
}
