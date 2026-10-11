//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"
)

func TestMQTTTLSRefusesUntrustedLegacyAndCleartext(t *testing.T) {
	f := newMQTTFixture(t)
	wrongName, legacy := f.cert.Client.Clone(), f.cert.Client.Clone()
	wrongName.ServerName = "wrong.example"
	legacy.MinVersion, legacy.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	for name, trust := range map[string]*tls.Config{"untrusted": {MinVersion: tls.VersionTLS13}, "wrong_name": wrongName, "legacy": legacy} {
		t.Run(name, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, trust)
			if err == nil {
				_ = conn.Close()
				t.Fatal("MQTT accepted invalid TLS")
			}
		})
	}
	clear, err := net.DialTimeout("tcp", f.address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer clear.Close()
	// A complete plaintext CONNECT must not authenticate or receive CONNACK.
	body := []byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0xc2, 0, 10}
	for _, field := range []string{fixtureAuthor, fixtureAuthor, f.authorSpec} {
		body = mqttString(body, field)
	}
	mqttWrite(t, clear, mqttPacket(0x10, body))
	if err := clear.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(clear)
	if err != nil || len(response) > 0 && response[0] != 0x15 {
		t.Fatal("plaintext MQTT was not refused by TLS", response, err)
	}
	noMailboxJob(t, f.fileMailboxFixture, "mqtt-cleartext")
	healthy := f.connect(t)
	mqttWrite(t, healthy, []byte{0xc0, 0})
	mqttReply(t, healthy, []byte{0xd0, 0})
	f.stop(t)
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(native) != 0 {
		t.Fatal(native)
	}
}

func TestMQTTGlobalAcceptedSocketCapacityAndJoinedPreauth(t *testing.T) {
	f := prepareMQTTFixture(t)
	f.config.Limits.Workers, f.config.Limits.Sessions, f.config.Limits.SessionsPerAuthor = 1, 2, 1
	f.config.Limits.Watches, f.config.Limits.WatchesPerAuthor = 0, 0
	f.start(t)
	owner := f.connect(t)
	preauth, err := net.DialTimeout("tcp", f.address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer preauth.Close()
	refused, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", f.address, f.cert.Client.Clone())
	if err == nil {
		_ = refused.Close()
		t.Fatal("MQTT exceeded accepted socket capacity")
	}
	mqttWrite(t, owner, []byte{0xc0, 0})
	mqttReply(t, owner, []byte{0xd0, 0})
	f.stop(t)
	mqttPeerClosed(t, owner)
	mqttPeerClosed(t, preauth)
}
