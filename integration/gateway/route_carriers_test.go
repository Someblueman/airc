//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// routeCarrierListeners are plaintext MQTT, STOMP and AMQP listeners routing
// #lobby, beside an HTTP listener for receipts.
func routeCarrierListeners() []config.Listener {
	lobby := core.Target{Kind: "room", Name: "#lobby"}
	ops := []core.Operation{core.Send}
	return []config.Listener{
		{ID: "web", Protocol: "http"},
		{ID: "mqtt", Protocol: "mqtt", Operations: ops, MQTT: &config.MQTT{Routes: []config.MQTTRoute{{Topic: "airc/lobby", Target: lobby}}}},
		{ID: "stomp", Protocol: "stomp", Operations: ops, STOMP: &config.STOMP{Routes: []config.STOMPRoute{{RequestDestination: "/queue/airc-request", ResponseDestination: "/queue/airc-response", Target: lobby, Operations: ops}}}},
		{ID: "amqp", Protocol: "amqp", Operations: ops, AMQP: &config.AMQP{Routes: []config.AMQPRoute{{RequestAddress: "airc/request", ResponseAddress: "airc/response", Target: lobby, Operations: ops}}}},
	}
}

func routeCarrierBody(t *testing.T, request map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// MQTT, STOMP and AMQP need no TLS and no credential: each request names its
// author, a guest by nickname or a registered account with its token, and
// AIRC decides who may post. STOMP keeps an account.identify for its
// connection.
func TestRouteCarriersPostPlainGuestsAndAccounts(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	watcher := dial(t, socket, "watcher", "", false, false)
	if err := watcher.Join("#lobby"); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" })
	provision := dial(t, socket, "alice", aliceToken, true, true)
	_ = provision.Close()
	g := startAuthorGateway(t, authorConfig(t, socket, gatewayToken, routeCarrierListeners()...))
	base := "http://" + g.addresses["web"]
	expect := func(id, from, message string, token string) {
		t.Helper()
		result := waitReceipt(t, base, id, authorHeaders(from, token))
		if result.State != core.Accepted {
			t.Fatalf("%s: %+v %+v", id, result, result.Failure)
		}
		seen := next(t, watcher, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		if seen.From != from || seen.Message != message || seen.Target != "#lobby" {
			t.Fatalf("%s posted as %q: %q", id, seen.From, seen.Message)
		}
	}

	// MQTT: CONNECT without credentials; QoS 1 is acknowledged after durable
	// intake, QoS 0 is admitted at most once without acknowledgement.
	mq, err := net.DialTimeout("tcp", g.addresses["mqtt"], 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer mq.Close()
	mqttWrite(t, mq, mqttPacket(0x10, mqttString([]byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0x02, 0, 30}, "")))
	mqttReply(t, mq, []byte{0x20, 2, 0, 0})
	mqttWrite(t, mq, mqttPublication(5, "airc/lobby", string(routeCarrierBody(t, sendRequest("mqtt-guest", "#lobby", "MQTT guest", "mq-guest", ""))), false))
	mqttReply(t, mq, []byte{0x40, 2, 0, 5})
	expect("mqtt-guest", "mq-guest", "MQTT guest", "")
	qos0 := mqttPacket(0x30, append(mqttString(nil, "airc/lobby"), routeCarrierBody(t, sendRequest("mqtt-alice", "#lobby", "MQTT alice", "alice", aliceToken))...))
	mqttWrite(t, mq, qos0)
	expect("mqtt-alice", "alice", "MQTT alice", aliceToken)

	// STOMP: any host, no passcode; a SEND without a subscription is answered
	// by its receipt. After account.identify, requests need no author.
	st, err := net.DialTimeout("tcp", g.addresses["stomp"], 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(st)
	sqWrite(t, st, "CONNECT", []string{"accept-version:1.2", "host:any-host.example", "heart-beat:0,0"}, nil)
	if frame, err := sqDecode(reader); err != nil || frame.command != "CONNECTED" {
		t.Fatal(frame, err)
	}
	sqWrite(t, st, "SEND", []string{"destination:/queue/airc-request", "content-type:application/json", "receipt:guest"}, routeCarrierBody(t, sendRequest("stomp-guest", "#lobby", "STOMP guest", "stomp-guest", "")))
	if frame, err := sqDecode(reader); err != nil || frame.command != "RECEIPT" || frame.headers["receipt-id"] != "guest" {
		t.Fatal(frame, err)
	}
	expect("stomp-guest", "stomp-guest", "STOMP guest", "")
	sqWrite(t, st, "SUBSCRIBE", []string{"id:results", "destination:/queue/airc-response", "ack:client-individual"}, nil)
	identify := plainRequest(map[string]any{"request_id": "stomp-identify", "operation": "account.identify"}, "alice", aliceToken)
	sqWrite(t, st, "SEND", []string{"destination:/queue/airc-request", "content-type:application/json"}, routeCarrierBody(t, identify))
	result, err := sqDecode(reader)
	if err != nil || result.command != "MESSAGE" {
		t.Fatal(result, err)
	}
	var identified struct {
		Result struct {
			Account struct{ Nick string } `json:"account"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result.body, &identified); err != nil || identified.Result.Account.Nick != "alice" {
		t.Fatalf("identify: %s %v", result.body, err)
	}
	sqWrite(t, st, "ACK", []string{"id:" + result.headers["ack"]}, nil)
	sqWrite(t, st, "SEND", []string{"destination:/queue/airc-request", "content-type:application/json"}, routeCarrierBody(t, sendRequest("stomp-alice", "#lobby", "STOMP alice", "", "")))
	if frame, err := sqDecode(reader); err != nil || frame.command != "MESSAGE" {
		t.Fatal(frame, err)
	}
	expect("stomp-alice", "alice", "STOMP alice", aliceToken)

	// AMQP: no SASL layer at all, a Flow sent before the server's Begin, and
	// a send-only link: the disposition reports acceptance.
	am, err := net.DialTimeout("tcp", g.addresses["amqp"], 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer am.Close()
	_ = am.SetDeadline(time.Now().Add(15 * time.Second))
	header := []byte{'A', 'M', 'Q', 'P', 0, 1, 0, 0}
	aqWrite(t, am, header)
	echo := make([]byte, 8)
	if _, err := io.ReadFull(am, echo); err != nil || string(echo) != string(header) {
		t.Fatal(echo, err)
	}
	aqWrite(t, am, aqFrame(0x10, 0, [][]byte{aqBytes(0xb1, []byte("route-carrier-test")), {0x40}, aqUint(4096)}, nil))
	aqWrite(t, am, aqFrame(0x11, 0, [][]byte{{0x40}, aqUint(1), aqUint(8), aqUint(8)}, nil))
	// Pipelined before the server's Begin: next-incoming-id is absent.
	aqWrite(t, am, aqFrame(0x13, 0, [][]byte{{0x40}, aqUint(8), aqUint(1), aqUint(8)}, nil))
	if p := aqRead(t, am); p.kind != 0x10 {
		t.Fatal(p)
	}
	if p := aqRead(t, am); p.kind != 0x11 {
		t.Fatal(p)
	}
	attach := [][]byte{aqBytes(0xb1, []byte("sender")), aqUint(0), {0x42}, {0x50, 2}, {0x50, 0}, aqDescribed(0x28, aqList(aqBytes(0xb1, []byte("")))), aqDescribed(0x29, aqList(aqBytes(0xb1, []byte("airc/request")))), {0x40}, {0x42}, aqUint(0)}
	aqWrite(t, am, aqFrame(0x12, 0, attach, nil))
	if p := aqRead(t, am); p.kind != 0x12 {
		t.Fatal(p)
	}
	if p := aqRead(t, am); p.kind != 0x13 || aqField(p, 6).number != 1 {
		t.Fatal("send-only link got no credit", p)
	}
	for i, c := range []struct{ id, nick, token, message string }{{"amqp-guest", "amqp-guest", "", "AMQP guest"}, {"amqp-alice", "alice", aliceToken, "AMQP alice"}} {
		// An amqp-value string body, as clients send by default.
		message := aqDescribed(0x77, aqBytes(0xb1, routeCarrierBody(t, sendRequest(c.id, "#lobby", c.message, c.nick, c.token))))
		aqWrite(t, am, aqFrame(0x14, 0, [][]byte{aqUint(0), aqUint(uint32(i)), aqBytes(0xb0, []byte{byte(i)}), aqUint(0), {0x42}, {0x42}}, message))
		disposition := aqRead(t, am)
		if disposition.kind != 0x15 || aqField(disposition, 4).descriptor != 0x24 {
			t.Fatal(disposition)
		}
		for credit := uint64(0); ; {
			flow := aqRead(t, am)
			if flow.kind != 0x13 {
				t.Fatal(flow)
			}
			if credit = aqField(flow, 6).number; credit == 1 {
				break
			}
		}
		expect(c.id, c.nick, c.message, c.token)
	}
	if status := plainHTTP(t, http.MethodGet, base+"/v1/requests/amqp-alice", nil, authorHeaders("alice", ""), nil); status == http.StatusOK {
		t.Fatal("a registered author's receipt was readable as a guest")
	}
}
