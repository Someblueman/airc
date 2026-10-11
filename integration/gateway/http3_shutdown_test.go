//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestHTTP3GatewayShutdownJoinsActiveNativeWatch(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	request := core.Request{Version: 1, RequestID: "http3-shutdown", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/rpc/events", strings.NewReader(string(rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request)))))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(httpRequest, f.token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	response, err := f.client.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		t.Fatalf("HTTP/3 stream status/protocol/content=%d/%d/%q body=%s", response.StatusCode, response.ProtoMajor, response.Header.Get("Content-Type"), data)
	}
	reader := bufio.NewReader(response.Body)
	if _, page := rpcSSEPage(t, readRPCSSE(t, reader)); page.Status == "" {
		t.Fatal("HTTP/3 stream returned an unmarked initial page")
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("gateway did not shut down cleanly: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("gateway did not join active HTTP/3 handler")
	}
	waitNativeObserverCount(t, statusClient, baseline)
	cancel()
	_ = response.Body.Close()
}
