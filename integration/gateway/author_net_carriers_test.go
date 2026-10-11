//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// freeUDP reserves a loopback UDP address for a listener.
func freeUDP(t *testing.T) string {
	t.Helper()
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	return probe.LocalAddr().String()
}

func mustBody(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// seenFrom waits for an independent IRC observer to see text, and returns
// who AIRC says posted it.
func seenFrom(t *testing.T, watcher *irc.Client, text string) string {
	t.Helper()
	return next(t, watcher, func(e *irc.MessageEvent) bool { return e.Message == text }).From
}

// Every network carrier takes the same plain contract JSON with no
// gateway credential: a nickname alone posts as an
// AIRC guest and a nickname with its account token posts as the account.
// Connection carriers keep an identified author for the connection. An
// independent IRC client observes who AIRC says posted each message.
// Carriers registered in TestCarrierSuite are covered there instead.
func TestAuthorNetworkCarriersPostAsAuthors(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	watcher := dial(t, socket, "watcher", "", false, false)
	if err := watcher.Join("#lobby"); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" })
	provision := dial(t, socket, "alice", aliceToken, true, true)
	_ = provision.Close()

	lobby := core.Target{Kind: "room", Name: "#lobby"}
	c := authorConfig(t, socket, gatewayToken,
		config.Listener{ID: "web", Protocol: "http"},
		config.Listener{ID: "sn", Protocol: "mqtt_sn", Address: freeUDP(t), Operations: []core.Operation{core.Send}, MQTTSN: &config.MQTTSN{Routes: []config.MQTTSNRoute{{TopicID: 7, Target: lobby}}}},
	)
	g := startAuthorGateway(t, c)

	t.Run("mqtt-sn", func(t *testing.T) {
		address, err := net.ResolveUDPAddr("udp", g.addresses["sn"])
		if err != nil {
			t.Fatal(err)
		}
		client, err := net.DialUDP("udp", nil, address)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		mqttSNSend(t, client, mqttSNPublication(7, string(mustBody(t, sendRequest("sn-guest", "#lobby", "guest over MQTT-SN", "sn-guest", ""))), false))
		if from := seenFrom(t, watcher, "guest over MQTT-SN"); from != "sn-guest" {
			t.Fatal("MQTT-SN post authored as", from)
		}
	})
}
