//go:build darwin || linux

package gateway_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

// MQTT 5 sessions outlive a gateway restart, over TCP and WebSocket alike:
// a client that comes back to the restarted gateway finds Session Present,
// its unacknowledged message again with its original packet ID, and then
// what was posted while the gateway was down. The sessions files are
// private, in the state directory.
func TestMQTT5SessionsSurviveARestart(t *testing.T) {
	t.Parallel()
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "mqtt", Protocol: "mqtt"}, config.Listener{ID: "mqttws", Protocol: "mqtt_websocket"})
	poster := dial(t, socket, "m8-poster", "", false, true)
	post := func(text string) string {
		t.Helper()
		if err := poster.Send("#m8", text); err != nil {
			t.Fatal(err)
		}
		return next(t, poster, func(e *irc.SendReceiptEvent) bool { return e.Message == text }).ID
	}
	dialers := map[string]func(*authorGateway) net.Conn{
		"mqtt": func(g *authorGateway) net.Conn {
			conn, err := net.Dial("tcp", g.addresses["mqtt"])
			if err != nil {
				t.Fatal(err)
			}
			return conn
		},
		"mqttws": func(g *authorGateway) net.Conn {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws://"+g.addresses["mqttws"]+"/mqtt", &websocket.DialOptions{Subprotocols: []string{"mqtt"}})
			if err != nil {
				t.Fatal(err)
			}
			return websocket.NetConn(context.Background(), conn, websocket.MessageBinary)
		},
	}
	listeners := []string{"mqtt", "mqttws"}

	g := startAuthorGateway(t, c)
	clients := map[string]*mqtt5Client{}
	for _, l := range listeners {
		client, connack := connectMQTT5(t, dialers[l](g), "m8-"+l, true, 600, nil, "airc-nick=m8-"+l)
		if connack.reason != 0 {
			t.Fatalf("%s CONNACK %#x", l, connack.reason)
		}
		if got := client.subscribe("airc/room/m8", 1); got[0] != 1 {
			t.Fatalf("%s SUBACK %v", l, got)
		}
		clients[l] = client
	}
	// Each receives a post and leaves it unacknowledged.
	unacked := post("before the restart")
	pending := map[string]uint16{}
	for _, l := range listeners {
		delivery := clients[l].next(3)
		if !strings.Contains(string(delivery.payload), unacked) {
			t.Fatalf("%s delivery %s", l, delivery.payload)
		}
		pending[l] = delivery.id
	}

	g.stop(t)
	for _, l := range listeners {
		info, err := os.Lstat(filepath.Join(c.StateDir, "mqtt-"+l+".sessions"))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s sessions file %v %v", l, info, err)
		}
	}
	missed := []string{post("while the gateway was down 1"), post("while the gateway was down 2")}

	g = startAuthorGateway(t, c)
	for _, l := range listeners {
		client, connack := connectMQTT5(t, dialers[l](g), "m8-"+l, false, 600, nil, "airc-nick=m8-"+l)
		if connack.reason != 0 || connack.body[0] != 1 {
			t.Fatalf("%s after the restart: CONNACK %#x, session present %d", l, connack.reason, connack.body[0])
		}
		again := client.next(3)
		if again.id != pending[l] || again.flags&8 == 0 || !strings.Contains(string(again.payload), unacked) {
			t.Fatalf("%s redelivery id %d (want %d) flags %d: %s", l, again.id, pending[l], again.flags, again.payload)
		}
		client.ack(again.id)
		for _, want := range missed {
			got := client.next(3)
			if !strings.Contains(string(got.payload), want) {
				t.Fatalf("%s missed message %s, want %s", l, got.payload, want)
			}
			client.ack(got.id)
		}
	}
}
