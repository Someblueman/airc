//go:build darwin || linux

package gateway_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
)

// The fallback client reaches the same AIRC over every carrier it speaks:
// native IRC on aircd's socket, and the gateway's framed Unix socket, HTTP and
// WebSocket. A post moves to the next carrier when one is unreachable and
// stays one message; history reads the same latest page on each.
func TestFallbackClientSpeaksEveryCarrier(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "web", Protocol: "http"}, config.Listener{ID: "ws", Protocol: "websocket"}, config.Listener{ID: "stream", Protocol: "unix"})
	g := startAuthorGateway(t, c)
	endpoint := func(name, address string) fallback.Endpoint {
		t.Helper()
		e, err := fallback.ParseEndpoint(name + ":" + address)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	native := endpoint("irc", socket)
	missing := endpoint("irc", filepath.Join(t.TempDir(), "gone.sock"))
	gateway := map[string]fallback.Endpoint{
		"unix":      endpoint("unix", g.addresses["stream"]),
		"http":      endpoint("http", g.addresses["web"]),
		"websocket": endpoint("websocket", g.addresses["ws"]),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &fallback.Client{Nick: "alice"}
	var first string
	for _, name := range []string{"unix", "http", "websocket"} {
		client.Endpoints = []fallback.Endpoint{missing, gateway[name]}
		receipt, err := client.Post(ctx, fallback.Message{Target: "#carriers", Body: "one message", RequestID: "every-carrier"})
		if err != nil || receipt.Endpoint != gateway[name] {
			t.Fatalf("%s: %+v %v", name, receipt, err)
		}
		if first == "" {
			first = receipt.MessageID
		} else if receipt.MessageID != first {
			t.Fatalf("%s posted %s, first carrier %s", name, receipt.MessageID, first)
		}
		if found, err := client.Lookup(ctx, "every-carrier"); err != nil || found.MessageID != first {
			t.Fatalf("%s lookup: %+v %v", name, found, err)
		}
	}
	client.Endpoints = []fallback.Endpoint{native}
	if receipt, err := client.Post(ctx, fallback.Message{Target: "#carriers", Body: "one message", RequestID: "every-carrier"}); err != nil || receipt.MessageID != first {
		t.Fatalf("native retry: %+v %v", receipt, err)
	}
	for _, body := range []string{"two", "three"} {
		if _, err := client.Post(ctx, fallback.Message{Target: "#carriers", Body: body, RequestID: "post-" + body}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(e fallback.Endpoint) []string {
		t.Helper()
		client.Endpoints = []fallback.Endpoint{e}
		page, err := client.History(ctx, "#carriers", 2)
		if err != nil {
			t.Fatalf("%s history: %v", e, err)
		}
		var bodies []string
		for _, m := range page.Messages {
			bodies = append(bodies, m.Message)
		}
		return bodies
	}
	want := read(native)
	if !slices.Equal(want, []string{"two", "three"}) {
		t.Fatalf("native latest page: %v", want)
	}
	for name, e := range gateway {
		if got := read(e); !slices.Equal(got, want) {
			t.Errorf("%s latest page %v, native %v", name, got, want)
		}
	}
	// A registered nickname without its token is a definite refusal: the
	// client stops there instead of trying the next carrier.
	token := registerAccount(t, "http://"+g.addresses["web"], "carol")
	client.Nick, client.Endpoints = "carol", []fallback.Endpoint{gateway["http"], gateway["websocket"]}
	tried := 0
	client.Attempted = func(fallback.Endpoint, error) { tried++ }
	_, err := client.Post(ctx, fallback.Message{Target: "#carriers", Body: "impostor", RequestID: "impostor"})
	if !fallback.Definite(err) || tried != 1 {
		t.Fatalf("guest on a registered nick: %v after %d attempts", err, tried)
	}
	client.Token = token
	if receipt, err := client.Post(ctx, fallback.Message{Target: "#carriers", Body: "registered", RequestID: "registered"}); err != nil || !protocol.ValidMessageID(receipt.MessageID) {
		t.Fatalf("registered: %+v %v", receipt, err)
	}
}
