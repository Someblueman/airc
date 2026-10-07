//go:build darwin || linux

package adapter

import (
	"bufio"
	"encoding/json"
	"net"
	"strconv"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSListenerStableInfoIdentity(t *testing.T) {
	slots := make(chan struct{}, 2)
	l, dial, _, cancel, done := natsListenerFixture(t, slots, 2)
	host, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	var first natsInfo
	for i := range 2 {
		client := dial()
		f, err := natswire.Read(bufio.NewReader(client.NetConn()))
		if err != nil || f.Command != "INFO" {
			t.Fatal("initial INFO", err)
		}
		var info natsInfo
		if err := json.Unmarshal(f.JSON, &info); err != nil {
			t.Fatal(err)
		}
		if info.ServerID == "" || info.ServerName != "fixture" || info.Host != host || info.Port != n || info.MaxPayload != natswire.MaxPayload || !info.AuthRequired || !info.TLSRequired || info.Headers || info.Proto != 1 {
			t.Fatal("invalid public INFO")
		}
		if i == 0 {
			first = info
		} else if info != first {
			t.Fatal("server identity changed between connections")
		}
		closeMQTTConnection(client)
	}
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}
