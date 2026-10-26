//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

// An MQTT 5 will is a contract mutation the gateway runs as the
// connection's author when the link dies: at once, or after its delay
// unless the session resumes first, and never after a normal DISCONNECT.
// A native observer sees what the wills post.
func TestMQTT5Wills(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "mqtt", Protocol: "mqtt"})
	g := startAuthorGateway(t, c)
	address := g.addresses["mqtt"]
	idle := mqtt5Connections()
	observer := dial(t, socket, "m6-observer", "", false, false)
	if err := observer.Join("#m6"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "m6-observer" })
	poster := dial(t, socket, "m6-poster", "", false, true)
	// seen is the next message in #m6 the observer sees.
	seen := func(want, from string) {
		t.Helper()
		m := next(t, observer, func(e *irc.MessageEvent) bool { return e.Target == "#m6" })
		if m.Message != want || m.From != from {
			t.Fatalf("saw %q from %s, want %q from %s", m.Message, m.From, want, from)
		}
	}
	// nothing proves no will was posted: a marker posted now is the next
	// message.
	nothing := func(marker string) {
		t.Helper()
		if err := poster.Send("#m6", marker); err != nil {
			t.Fatal(err)
		}
		seen(marker, "m6-poster")
	}
	gone := &mqttWill{topic: "airc/requests", payload: mqttRequest("m6-will", "send", "#m6", "m6-will is gone")}
	connect := func(id string, clean bool, expiry uint32, will *mqttWill) (*mqtt5Client, *mqtt5Packet) {
		t.Helper()
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		return connectMQTT5(t, conn, id, clean, expiry, will, "airc-nick=m6-will")
	}

	// A dropped link runs the will; each connection's will is its own job,
	// so the second drop posts again.
	for range 2 {
		client, connack := connect("m6-a", true, 0, gone)
		if connack.reason != 0 {
			t.Fatalf("CONNACK %#x %q", connack.reason, connack.props[0x1F])
		}
		_ = client.conn.Close()
		seen("m6-will is gone", "m6-will")
	}

	// A normal DISCONNECT drops the will; DISCONNECT 0x04 asks for it.
	client, _ := connect("m6-a", true, 0, gone)
	client.send(0xe0, []byte{0, 0})
	time.Sleep(300 * time.Millisecond)
	nothing("after a normal disconnect")
	client, _ = connect("m6-a", true, 0, gone)
	client.send(0xe0, []byte{0x04, 0})
	seen("m6-will is gone", "m6-will")

	// A delayed will waits; the session resuming within the delay drops it.
	delayed := *gone
	delayed.delay = 2
	client, _ = connect("m6-b", true, 600, &delayed)
	_ = client.conn.Close()
	// Once every connection so far has ended, the gateway has scheduled the
	// dropped link's will, and the session resumes within its delay.
	testwait.Until(t, 5*time.Second, "the gateway to end every MQTT connection", func() bool { return mqtt5Connections() == idle })
	resumed, connack := connect("m6-b", false, 600, nil)
	if connack.body[0] != 1 {
		t.Fatal("the session did not resume")
	}
	time.Sleep(2500 * time.Millisecond)
	nothing("after a resume within the delay")
	// The resumed connection has no will; one that stays away past the
	// delay posts.
	resumed.send(0xe0, []byte{0, 0})
	delayed.delay = 1
	client, _ = connect("m6-b", true, 600, &delayed)
	_ = client.conn.Close()
	began := time.Now()
	seen("m6-will is gone", "m6-will")
	if waited := time.Since(began); waited < 800*time.Millisecond {
		t.Fatal("the will did not wait out its delay", waited)
	}

	// Refused wills, with MQTT 5 reason codes.
	for _, bad := range []struct {
		name   string
		will   mqttWill
		user   []string
		reason byte
	}{
		{"not a request topic", mqttWill{topic: "elsewhere", payload: gone.payload}, []string{"airc-nick=m6-will"}, 0x90},
		{"a read", mqttWill{topic: "airc/requests", payload: mqttRequest("m6-will", "history.read", "#m6", "")}, []string{"airc-nick=m6-will"}, 0x99},
		{"retained", mqttWill{topic: "airc/requests", payload: gone.payload, retain: true}, []string{"airc-nick=m6-will"}, 0x9A},
		{"no author", mqttWill{topic: "airc/requests", payload: `{"version":1,"request_id":"w","operation":"send","target":{"kind":"room","name":"#m6"},"message":"x"}`}, nil, 0x87},
	} {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		if _, connack := connectMQTT5(t, conn, "m6-bad", true, 0, &bad.will, bad.user...); connack.reason != bad.reason {
			t.Fatalf("%s: CONNACK %#x, want %#x", bad.name, connack.reason, bad.reason)
		}
	}
}

// MQTT over WebSocket: the subprotocol "mqtt" at /mqtt carries the same
// MQTT 5 sessions, subscriptions and requests as the TCP listener.
func TestMQTTOverWebSocket(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "mqttws", Protocol: "mqtt_websocket"})
	g := startAuthorGateway(t, c)
	url := "ws://" + g.addresses["mqttws"] + "/mqtt"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, response, err := websocket.Dial(ctx, url, nil); err == nil || response == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatal("a WebSocket without the mqtt subprotocol was not refused", err)
	}
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"mqtt"}})
	if err != nil {
		t.Fatal(err)
	}
	client, connack := connectMQTT5(t, websocket.NetConn(t.Context(), ws, websocket.MessageBinary), "m7-reader", true, 600, nil, "airc-nick=m7-reader")
	if connack.reason != 0 {
		t.Fatalf("CONNACK %#x", connack.reason)
	}
	if got := client.subscribe("airc/room/m7", 1); got[0] != 1 {
		t.Fatalf("SUBACK %v", got)
	}
	poster := dial(t, socket, "m7-poster", "", false, false)
	if err := poster.Join("#m7"); err != nil {
		t.Fatal(err)
	}
	next(t, poster, func(e *irc.JoinEvent) bool { return e.Agent == "m7-poster" })
	if err := poster.Send("#m7", "over websocket"); err != nil {
		t.Fatal(err)
	}
	delivered := client.next(3)
	if delivered.topic != "airc/room/m7" || mqttBody(delivered.payload) != "over websocket" {
		t.Fatalf("delivery on %q: %s", delivered.topic, delivered.payload)
	}
	client.ack(delivered.id)
	if puback := client.publish("airc/requests", 1, mqttRequest("m7-reader", "send", "#m7", "from a websocket")); puback.reason != 0 {
		t.Fatalf("PUBACK %#x %q", puback.reason, puback.props[0x1F])
	}
	next(t, poster, func(e *irc.MessageEvent) bool { return e.Message == "from a websocket" && e.From == "m7-reader" })
}

// mqttBody is the text of a delivered message's JSON.
func mqttBody(payload []byte) string {
	var m struct {
		Body string `json:"body_base64"`
	}
	if json.Unmarshal(payload, &m) != nil {
		return ""
	}
	body, _ := base64.RawStdEncoding.DecodeString(strings.TrimRight(m.Body, "="))
	return string(body)
}
