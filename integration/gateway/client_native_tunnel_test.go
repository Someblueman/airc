//go:build darwin || linux

package gateway_test

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/pkg/fallback"
)

// The client library (pkg/fallback) through this gateway's native tunnels,
// served in process.

// tunnelTo serves one native tunnel carrier in front of aircd at upstream
// and returns its endpoint.
func tunnelTo(t *testing.T, name, upstream string) fallback.Endpoint {
	t.Helper()
	u := adapter.IRCUpstream{Network: "tcp", Address: upstream, Listener: name}
	var srv *http.Server
	var err error
	switch name {
	case "irc_websocket":
		srv, err = adapter.IRCWebSocketServer(u, nil)
	case "irc_connect":
		srv, err = adapter.IRCConnectServer(u)
	}
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv.BaseContext = func(net.Listener) context.Context { return ctx }
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	return fallback.Endpoint{Carrier: protocol.Carrier{Name: name, Port: p}}
}

// A native tunnel is native IRC: a post, its retry, a receipt lookup, a
// history read and discovery all run as they do on aircd's own port, and
// the answering endpoint is the tunnel.
func TestNativeTunnelsCarryNativeIRC(t *testing.T) {
	advertised := []protocol.Carrier{{Name: "irc", Port: 6667}, {Name: "irc_websocket", Port: 6689}}
	addr := testserver.Start(t, server.Config{HistoryLimit: 100, Carriers: advertised}, nil)
	for _, name := range []string{"irc_websocket", "irc_connect"} {
		t.Run(name, func(t *testing.T) {
			e := tunnelTo(t, name, addr)
			c := &fallback.Client{Host: "127.0.0.1", Nick: "alice", Endpoints: []fallback.Endpoint{e}}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			first, err := c.Post(ctx, fallback.Message{Target: "#room", Body: "through " + name, RequestID: name + "-1"})
			if err != nil || !protocol.ValidMessageID(first.MessageID) || first.Endpoint != e {
				t.Fatal(first, err)
			}
			again, err := c.Post(ctx, fallback.Message{Target: "#room", Body: "through " + name, RequestID: name + "-1"})
			if err != nil || again.MessageID != first.MessageID {
				t.Fatal("retry posted again", again, err)
			}
			found, err := c.Lookup(ctx, name+"-1")
			if err != nil || found.MessageID != first.MessageID {
				t.Fatal(found, err)
			}
			page, err := c.History(ctx, "#room", 10)
			if err != nil || len(page.Messages) == 0 || page.Messages[len(page.Messages)-1].ID != first.MessageID {
				t.Fatal(page, err)
			}
			carriers, from, err := c.Carriers(ctx)
			if err != nil || protocol.FormatCarriers(carriers) != "irc:6667,irc_websocket:6689" || from != e {
				t.Fatal(carriers, from, err)
			}
			// A long line and a wide body cross the tunnel whole.
			long := strings.Repeat("x", 3000)
			receipt, err := c.Post(ctx, fallback.Message{Target: "#room", Body: long, RequestID: name + "-2"})
			if err != nil {
				t.Fatal(err)
			}
			page, err = c.History(ctx, "#room", 1)
			if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != receipt.MessageID || page.Messages[0].Message != long {
				t.Fatal("the long body did not survive the tunnel", err)
			}
		})
	}
}
