//go:build darwin || linux

package gateway_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// The native tunnels carry aircd's own protocol through the gateway service:
// a guest and a registered account register, post and read through each,
// after the gateway asserted their source with WEBIRC, and an independent
// native reader sees the posts.
func TestNativeTunnelsThroughTheService(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	account := dial(t, socket, "carol", aliceToken, true, false)
	_ = account.Close()
	watcher := dial(t, socket, "watcher", "", false, false)
	if err := watcher.Join("#lobby"); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" })
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "wsirc", Protocol: "irc_websocket"}, config.Listener{ID: "connect", Protocol: "irc_connect"})
	g := startAuthorGateway(t, c)
	for _, l := range []struct{ id, name string }{{"wsirc", "irc_websocket"}, {"connect", "irc_connect"}} {
		t.Run(l.name, func(t *testing.T) {
			_, port, _ := net.SplitHostPort(g.addresses[l.id])
			p, _ := strconv.Atoi(port)
			e := fallback.Endpoint{Carrier: protocol.Carrier{Name: l.name, Port: p}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			guest := &fallback.Client{Host: "127.0.0.1", Nick: "guest-" + l.id, Endpoints: []fallback.Endpoint{e}}
			receipt, err := guest.Post(ctx, fallback.Message{Target: "#lobby", Body: "guest via " + l.name, RequestID: l.id + "-guest"})
			if err != nil || receipt.Endpoint != e {
				t.Fatal(receipt, err)
			}
			next(t, watcher, func(m *irc.MessageEvent) bool { return m.ID == receipt.MessageID && m.From == "guest-"+l.id })
			registered := &fallback.Client{Host: "127.0.0.1", Nick: "carol", Token: aliceToken, Endpoints: []fallback.Endpoint{e}}
			receipt, err = registered.Post(ctx, fallback.Message{Target: "#lobby", Body: "account via " + l.name, RequestID: l.id + "-account"})
			if err != nil {
				t.Fatal(err)
			}
			seen := next(t, watcher, func(m *irc.MessageEvent) bool { return m.ID == receipt.MessageID })
			if seen.From != "carol" || seen.AccountID == "" {
				t.Fatalf("account post: %+v", seen)
			}
			impostor := &fallback.Client{Host: "127.0.0.1", Nick: "carol", Endpoints: []fallback.Endpoint{e}}
			if _, err := impostor.Post(ctx, fallback.Message{Target: "#lobby", Body: "impostor", RequestID: l.id + "-impostor"}); !fallback.Definite(err) {
				t.Fatalf("a guest on the registered nickname was not refused by aircd: %v", err)
			}
			page, err := guest.History(ctx, "#lobby", 5)
			if err != nil || len(page.Messages) < 2 {
				t.Fatal(page, err)
			}
		})
	}
}
